package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
	"github.com/danieljustus/symaira-seek/internal/mcp"
	"github.com/danieljustus/symaira-seek/internal/server"
)

const version = "0.1.0"

// Config holds user configuration.
type Config struct {
	OllamaURL string `json:"ollama_url"`
	Model     string `json:"model"`
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

			embedder := &engine.EmbeddingsGenerator{
				OllamaURL: cfg.OllamaURL,
				Model:     cfg.Model,
			}

			results, err := engine.SearchHybrid(dbClient, embedder, query, limitFlag)
			if err != nil {
				return err
			}

			if jsonFlag {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			if len(results) == 0 {
				fmt.Fprintln(os.Stderr, "No matching documents found.")
				return nil
			}

			for idx, r := range results {
				fmt.Printf("[%d] Path: %s (Chunk Index: %d)\n", idx+1, r.Chunk.DocumentPath, r.Chunk.ChunkIndex)
				fmt.Printf("    Score: RRF=%.4f Cosine=%.4f (Ranks: BM25=%d Vector=%d)\n", r.RRFScore, r.CosineScore, r.BM25Rank, r.VectorRank)
				fmt.Println("    --- Content ---")
				lines := stringsSplitLines(r.Chunk.Content)
				for _, line := range lines {
					fmt.Printf("    %s\n", line)
				}
				fmt.Println("    ----------------")
				fmt.Println()
			}
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

			embedder := &engine.EmbeddingsGenerator{
				OllamaURL: cfg.OllamaURL,
				Model:     cfg.Model,
			}

			if watchFlag {
				fmt.Fprintf(os.Stderr, "Starting watch daemon on: %s (Sync every 5 seconds)\n", dirPath)
				for {
					err := engine.IndexDirectory(dbClient, embedder, dirPath)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Sync error: %v\n", err)
					}
					time.Sleep(5 * time.Second)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Indexing folder: %s...\n", dirPath)
				return engine.IndexDirectory(dbClient, embedder, dirPath)
			}
		},
	}
	indexCmd.Flags().BoolVarP(&watchFlag, "watch", "w", false, "Run in background and monitor folder for changes")
	rootCmd.AddCommand(indexCmd)

	// 3. Status Command
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

			fmt.Printf("Indexed Documents: %d\n", stats.DocumentCount)
			fmt.Printf("Indexed Chunks:    %d\n", stats.ChunkCount)
			fmt.Printf("Database Size:     %s\n", formatBytes(stats.DatabaseSize))
			return nil
		},
	}
	rootCmd.AddCommand(statusCmd)

	// 4. Config Command
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "View and edit settings",
		Run: func(cmd *cobra.Command, args []string) {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(cfg)
		},
	}
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
			fmt.Fprintln(os.Stderr, "Error locating home directory:", err)
			os.Exit(1)
		}
		cfgDir := filepath.Join(home, ".config", "symaira-seek")
		os.MkdirAll(cfgDir, 0755)
		cfgFile = filepath.Join(cfgDir, "config.json")
	}

	// Set defaults
	cfg = Config{
		OllamaURL: "http://localhost:11434/api/embeddings",
		Model:     "nomic-embed-text",
	}

	data, err := os.ReadFile(cfgFile)
	if err == nil {
		json.Unmarshal(data, &cfg)
	} else {
		// Save default configuration
		data, _ := json.MarshalIndent(cfg, "", "  ")
		os.WriteFile(cfgFile, data, 0644)
	}
}

func stringsSplitLines(s string) []string {
	var lines []string
	var line []rune
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, string(line))
			line = nil
		} else {
			line = append(line, r)
		}
	}
	if len(line) > 0 {
		lines = append(lines, string(line))
	}
	return lines
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func startHTTPServer(port int) error {
	return server.StartHTTPServer(port, cfg.OllamaURL, cfg.Model)
}

func startMCPServer() error {
	return mcp.StartServer(cfg.OllamaURL, cfg.Model)
}
