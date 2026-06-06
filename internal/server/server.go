package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
)

const (
	maxHeaderBytes    = 1 << 20
	maxIndexBodyBytes = 1 << 20
	indexCooldown     = 5 * time.Second
)

type rateLimiter struct {
	mu       sync.Mutex
	cooldown time.Duration
	lastSeen map[string]time.Time
}

func newRateLimiter(cooldown time.Duration) *rateLimiter {
	return &rateLimiter{
		cooldown: cooldown,
		lastSeen: make(map[string]time.Time),
	}
}

func (r *rateLimiter) Allow(key string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.lastSeen[key]; ok && now.Sub(t) < r.cooldown {
		return false
	}
	r.lastSeen[key] = now
	if len(r.lastSeen) > 1024 {
		r.evictStaleLocked(now)
	}
	return true
}

func (r *rateLimiter) evictStaleLocked(now time.Time) {
	for k, t := range r.lastSeen {
		if now.Sub(t) >= r.cooldown {
			delete(r.lastSeen, k)
		}
	}
}

// StartHTTPServer runs the local HTTP REST daemon.
func StartHTTPServer(port int, ollamaURL, model string) error {
	dbClient, err := db.Open()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer dbClient.Close()

	mux := http.NewServeMux()

	// 1. Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// 2. Status endpoint
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats, err := dbClient.GetStats()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(stats)
	})

	// 3. Hybrid search endpoint
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "missing query parameter 'q'", http.StatusBadRequest)
			return
		}

		limit := 5
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
				limit = parsed
			}
		}

		embedder := engine.NewEmbeddingsGeneratorWithConfig(ollamaURL, model)

		results, err := engine.SearchHybrid(dbClient, embedder, query, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(results)
	})

	// 4. Index folder endpoint
	indexLimiter := newRateLimiter(indexCooldown)
	mux.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !indexLimiter.Allow(r.RemoteAddr, time.Now()) {
			http.Error(w, "rate limit exceeded for /index", http.StatusTooManyRequests)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxIndexBodyBytes)
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "bad request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		if req.Path == "" {
			http.Error(w, "missing 'path' field", http.StatusBadRequest)
			return
		}

		absPath, status, err := validateIndexPath(req.Path)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}

		embedder := engine.NewEmbeddingsGeneratorWithConfig(ollamaURL, model)

		if err := engine.IndexDirectory(dbClient, embedder, absPath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"status": "indexed",
			"path":   absPath,
		})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Fprintf(os.Stderr, "HTTP daemon listening on http://%s...\n", addr)

	srv := &http.Server{
		Addr:           addr,
		Handler:        mux,
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   15 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: maxHeaderBytes,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP server error: %w", err)
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "Received %s, shutting down gracefully...\n", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		srv.Close()
		return fmt.Errorf("HTTP server forced shutdown: %w", err)
	}

	fmt.Fprintf(os.Stderr, "HTTP server stopped.\n")
	return nil
}

// validateIndexPath normalizes a user-supplied /index path and checks that
// it is an existing directory under the user's home. The home-boundary
// check is the security boundary — the daemon is localhost-only, but a
// malicious local caller must not be able to instruct the indexer to crawl
// /etc, ~/.ssh, or other sensitive roots.
func validateIndexPath(reqPath string) (string, int, error) {
	absPath, err := filepath.Abs(reqPath)
	if err != nil {
		return "", http.StatusBadRequest, fmt.Errorf("invalid path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", http.StatusBadRequest, fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return "", http.StatusBadRequest, fmt.Errorf("path is not a directory: %s", absPath)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", http.StatusInternalServerError, fmt.Errorf("cannot determine home directory: %w", err)
	}
	rel, err := filepath.Rel(home, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", http.StatusForbidden, fmt.Errorf("path is outside the allowed root (%s)", home)
	}
	return absPath, http.StatusOK, nil
}
