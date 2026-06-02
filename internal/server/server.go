package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
)

// StartHTTPServer runs the local HTTP REST daemon.
func StartHTTPServer(port int, ollamaURL, model string) error {
	mux := http.NewServeMux()

	// 1. Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// 2. Status endpoint
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbClient, err := db.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer dbClient.Close()

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

		dbClient, err := db.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer dbClient.Close()

		embedder := &engine.EmbeddingsGenerator{
			OllamaURL: ollamaURL,
			Model:     model,
		}

		results, err := engine.SearchHybrid(dbClient, embedder, query, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(results)
	})

	// 4. Index folder endpoint
	mux.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		if req.Path == "" {
			http.Error(w, "missing 'path' field", http.StatusBadRequest)
			return
		}

		dbClient, err := db.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer dbClient.Close()

		embedder := &engine.EmbeddingsGenerator{
			OllamaURL: ollamaURL,
			Model:     model,
		}

		// Run crawl
		err = engine.IndexDirectory(dbClient, embedder, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"status": "indexed",
			"path":   req.Path,
		})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("HTTP daemon listening on http://%s...\n", addr)
	return http.ListenAndServe(addr, mux)
}
