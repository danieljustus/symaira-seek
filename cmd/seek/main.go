package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
	"github.com/danieljustus/symaira-seek/internal/mcp"
	"github.com/danieljustus/symaira-seek/internal/server"
)

// version is the build-time version string, intended to be
// overridden via -ldflags at build time (issue #48). The default
// reports a development build marker so an un-overridden binary
// still prints a meaningful value.
var version = "0.1.0-dev"

// Config holds user configuration.
type Config struct {
	OllamaURL      string `json:"ollama_url"`
	Model          string `json:"model"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	RetryCount     int    `json:"retry_count"`
	RetryBackoffMS int    `json:"retry_backoff_ms"`
}

var (
	cfgFile   string
	cfg       Config
	limitFlag int
	jsonFlag  bool
	watchFlag bool
	portFlag  int
)

func main() {
	cobra.OnInitialize(initConfig)

	rootCmd := &cobra.Command{
		Use:   "seek",
		Short: "Symaira-Seek: A local hybrid document retrieval CLI and MCP tool",
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/symaira-seek/config.json)")

	// 1. Search Command
	searchCmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Perform keyword and vector hybrid search over indexed documents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			dbClient, err := db.Open()
			if err != nil {
				return err
			}
			defer dbClient.Close()

			embedder := engine.NewEmbeddingsGeneratorWithOllamaConfig(cfg.ollamaConfig())

			results, err := engine.SearchHybrid(dbClient, embedder, query, limitFlag)
			if err != nil {
				return err
			}

			if jsonFlag {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			writeSearchHuman(os.Stderr, results)
			return nil
		},
	}
	searchCmd.Flags().IntVarP(&limitFlag, "limit", "l", 5, "Number of search results to return")
	searchCmd.Flags().BoolVar(&jsonFlag, "json", false, "Output results in JSON format")
	rootCmd.AddCommand(searchCmd)

	// 2. Index Command
	indexCmd := &cobra.Command{
		Use:   "index [folder_path]",
		Short: "Crawl and index a local directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dirPath := args[0]
			dbClient, err := db.Open()
			if err != nil {
				return err
			}
			defer dbClient.Close()

			embedder := engine.NewEmbeddingsGeneratorWithOllamaConfig(cfg.ollamaConfig())

			if watchFlag {
				ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
				defer cancel()

				fmt.Fprintf(os.Stderr, "Starting watch daemon on: %s (fsnotify event-based)\n", dirPath)
				return engine.WatchDirectory(ctx, dbClient, embedder, dirPath)
			}
			fmt.Fprintf(os.Stderr, "Indexing folder: %s...\n", dirPath)
			return engine.IndexDirectory(dbClient, embedder, dirPath)
		},
	}
	indexCmd.Flags().BoolVarP(&watchFlag, "watch", "w", false, "Run in background and monitor folder for changes")
	rootCmd.AddCommand(indexCmd)

	// 3. Delete Command
	deleteCmd := &cobra.Command{
		Use:   "delete [document_path]",
		Short: "Remove a document and its chunks from the index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			docPath := args[0]
			dbClient, err := db.Open()
			if err != nil {
				return err
			}
			defer dbClient.Close()

			existing, err := dbClient.GetDocument(docPath)
			if err != nil {
				return err
			}
			if existing == nil {
				fmt.Fprintf(os.Stderr, "Document not found in index: %s\n", docPath)
				return nil
			}

			if err := dbClient.DeleteDocument(docPath); err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Removed from index: %s\n", docPath)
			return nil
		},
	}
	rootCmd.AddCommand(deleteCmd)

	// 4. Status Command
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Display statistics about the local search index",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbClient, err := db.Open()
			if err != nil {
				return err
			}
			defer dbClient.Close()

			stats, err := dbClient.GetStats()
			if err != nil {
				return err
			}

			if jsonFlag {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(stats)
			}

			fmt.Printf("Indexed Documents: %d\n", stats.DocumentCount)
			fmt.Printf("Indexed Chunks:    %d\n", stats.ChunkCount)
			fmt.Printf("Database Size:     %s\n", humanize.Bytes(uint64(stats.DatabaseSize)))
			return nil
		},
	}
	statusCmd.Flags().BoolVar(&jsonFlag, "json", false, "Output stats in JSON format")
	rootCmd.AddCommand(statusCmd)

	// 4. Config Command
	var configSetKey string
	var configSetValue string
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "View and edit settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configSetKey != "" {
				if err := setConfigValue(configSetKey, configSetValue); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Set %s = %s in %s\n", configSetKey, configSetValue, cfgFile)
				return nil
			}
			fmt.Fprintf(os.Stderr, "Config file: %s\n", cfgFile)
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(cfg)
		},
	}
	configCmd.Flags().StringVar(&configSetKey, "set-key", "", "Set a config key (e.g. ollama_url, model)")
	configCmd.Flags().StringVar(&configSetValue, "set-value", "", "Value for the config key set via --set-key")
	rootCmd.AddCommand(configCmd)

	// 5. Version Command
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version number of Symaira-Seek",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("seek version %s\n", version)
		},
	}
	rootCmd.AddCommand(versionCmd)

	// 6. Serve Command (Implemented in Phase 5)
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Launch the MCP server or HTTP REST daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if portFlag > 0 {
				// Run HTTP server
				fmt.Fprintf(os.Stderr, "HTTP REST Server implementation starting on port %d...\n", portFlag)
				return startHTTPServer(portFlag)
			}
			// Run MCP server over stdio
			fmt.Fprintln(os.Stderr, "MCP server starting over stdio...")
			return startMCPServer()
		},
	}
	serveCmd.Flags().IntVarP(&portFlag, "port", "p", 0, "Launch HTTP REST server on this port instead of stdio MCP")
	rootCmd.AddCommand(serveCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initConfig() {
	if cfgFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.TempDir()
			fmt.Fprintf(os.Stderr, "Warning: $HOME not set, using %s for config directory\n", home)
		}
		cfgDir := filepath.Join(home, ".config", "symaira-seek")
		if err := os.MkdirAll(cfgDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "seek: could not create config directory %s: %v\n", cfgDir, err)
		}
		cfgFile = filepath.Join(cfgDir, "config.json")
	}

	cfg = loadOrInitConfig(cfgFile)
}

func defaultConfig() Config {
	return Config{
		OllamaURL:      "http://localhost:11434/api/embeddings",
		Model:          "nomic-embed-text",
		TimeoutSeconds: 120,
		RetryCount:     2,
		RetryBackoffMS: 500,
	}
}

func (c Config) ollamaConfig() engine.OllamaConfig {
	return engine.OllamaConfig{
		URL:          c.OllamaURL,
		Model:        c.Model,
		Timeout:      time.Duration(c.TimeoutSeconds) * time.Second,
		RetryCount:   c.RetryCount,
		RetryBackoff: time.Duration(c.RetryBackoffMS) * time.Millisecond,
	}
}

func loadOrInitConfig(path string) Config {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "seek: could not read config %s: %v; using built-in defaults\n", path, err)
			return cfg
		}
		writeDefaultConfig(path, cfg)
		return cfg
	}

	if uErr := json.Unmarshal(data, &cfg); uErr != nil {
		fmt.Fprintf(os.Stderr, "seek: config %s is malformed (%v); using built-in defaults\n", path, uErr)
		return defaultConfig()
	}
	return cfg
}

func writeDefaultConfig(path string, cfg Config) {
	out, mErr := json.MarshalIndent(cfg, "", "  ")
	if mErr != nil {
		fmt.Fprintf(os.Stderr, "seek: could not marshal default config: %v\n", mErr)
		return
	}
	if wErr := os.WriteFile(path, out, 0600); wErr != nil {
		fmt.Fprintf(os.Stderr, "seek: could not write default config to %s: %v\n", path, wErr)
	}
}

func setConfigValue(key, value string) error {
	switch key {
	case "ollama_url":
		cfg.OllamaURL = value
	case "model":
		cfg.Model = value
	case "timeout_seconds":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid %s value %q (must be a positive integer)", key, value)
		}
		cfg.TimeoutSeconds = n
	case "retry_count":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid %s value %q (must be a non-negative integer)", key, value)
		}
		cfg.RetryCount = n
	case "retry_backoff_ms":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid %s value %q (must be a positive integer)", key, value)
		}
		cfg.RetryBackoffMS = n
	default:
		return fmt.Errorf("unknown config key %q (supported: ollama_url, model, timeout_seconds, retry_count, retry_backoff_ms)", key)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgFile, data, 0600)
}

func startHTTPServer(port int) error {
	return server.StartHTTPServer(port, cfg.ollamaConfig())
}

func startMCPServer() error {
	mcp.ServerVersion = version
	return mcp.StartServer(cfg.ollamaConfig())
}

func writeSearchHuman(w io.Writer, results []*db.SearchResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "No matching documents found.")
		return
	}
	for idx, r := range results {
		fmt.Fprintf(w, "[%d] Path: %s (Chunk Index: %d)\n", idx+1, r.Chunk.DocumentPath, r.Chunk.ChunkIndex)
		fmt.Fprintf(w, "    Score: RRF=%.4f Cosine=%.4f (Ranks: BM25=%d Vector=%d)\n", r.RRFScore, r.CosineScore, r.BM25Rank, r.VectorRank)
		fmt.Fprintln(w, "    --- Content ---")
		for _, line := range strings.Split(r.Chunk.Content, "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
		fmt.Fprintln(w, "    ----------------")
		fmt.Fprintln(w)
	}
}
