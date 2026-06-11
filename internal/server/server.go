package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
	"github.com/danieljustus/symaira-seek/internal/pathutil"
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

// isLocalhostHost reports whether host is a localhost address (127.0.0.1 or
// localhost), optionally with a port suffix.
func isLocalhostHost(host string) bool {
	// Strip port if present.
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	return hostname == "127.0.0.1" || hostname == "localhost" || hostname == "::1"
}

// isLocalhostOrigin reports whether origin is a localhost origin (http or
// https scheme with a localhost host).
func isLocalhostOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return isLocalhostHost(u.Host)
}

// hostValidation wraps an http.Handler and rejects requests whose Host header
// does not point to a localhost address (DNS-rebinding protection).
func hostValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalhostHost(r.Host) {
			http.Error(w, "forbidden: non-localhost host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originValidation wraps an http.Handler and rejects requests whose Origin
// header, when present, does not point to a localhost origin (cross-origin
// protection).
func originValidation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if !isLocalhostOrigin(origin) {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// contentTypeEnforcement wraps an http.Handler and rejects POST requests to
// /index that lack Content-Type: application/json.
func contentTypeEnforcement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/index" {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				http.Error(w, "unsupported media type: Content-Type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// bearerTokenAuth wraps an http.Handler and checks the SEEK_API_TOKEN
// environment variable. If set, requests must include a matching Bearer token
// in the Authorization header.
func bearerTokenAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := os.Getenv("SEEK_API_TOKEN")
		if expected == "" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != expected {
			http.Error(w, "unauthorized: invalid or missing Bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// StartHTTPServer runs the local HTTP REST daemon.
func StartHTTPServer(port int, ollamaCfg engine.OllamaConfig) error {
	dbClient, err := db.Open()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer dbClient.Close()

	embedder := engine.NewEmbeddingsGeneratorWithOllamaConfig(ollamaCfg)

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

		results, err := engine.SearchHybrid(dbClient, embedder, query, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(results)
	})

	// 3a. SSE streaming search endpoint
	mux.HandleFunc("/search/stream", func(w http.ResponseWriter, r *http.Request) {
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

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		results, err := engine.SearchHybrid(dbClient, embedder, query, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, res := range results {
			select {
			case <-r.Context().Done():
				return
			default:
			}

			data, err := json.Marshal(res)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: result\ndata: %s\n\n", data)
			flusher.Flush()
		}

		doneData, _ := json.Marshal(map[string]int{"count": len(results)})
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
		flusher.Flush()
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

		absPath, err := pathutil.RestrictToHome(req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

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
		Handler:        hostValidation(originValidation(contentTypeEnforcement(bearerTokenAuth(mux)))),
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


