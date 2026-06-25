package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/danieljustus/symaira-corekit/exitcodes"
	"github.com/danieljustus/symaira-corekit/logkit"
	"github.com/danieljustus/symaira-corekit/updatecheck"
	"github.com/danieljustus/symaira-seek/internal/config"
	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
	"github.com/danieljustus/symaira-seek/internal/mcp"
	"github.com/danieljustus/symaira-seek/internal/server"
	"github.com/danieljustus/symaira-seek/internal/tui"
)

var version = "0.1.0-dev"

var (
	cfgFile     string
	cfg         config.Config
	limitFlag   int
	jsonFlag    bool
	tuiFlag     bool
	plainFlag   bool
	watchFlag   bool
	portFlag    int
	urlFlag     string
	stdinFlag   bool
	sourceFlag  string
	verboseFlag bool
	quietFlag   bool
)

func main() {
	cobra.OnInitialize(initConfig)

	rootCmd := newRootCmd()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, exitcodes.FormatCLIError(err))
		os.Exit(int(exitcodes.ExitCodeFromError(err)))
	}
}

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "symseek",
		Short: "Symaira-Seek: A local hybrid document retrieval CLI and MCP tool",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			level := slog.LevelInfo
			if verboseFlag {
				level = slog.LevelDebug
			} else if quietFlag {
				level = slog.LevelError
			}
			slog.SetDefault(logkit.New(os.Stderr, level, "text"))
		},
	}
	rootCmd.Version = version

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/symseek/config.toml)")
	rootCmd.PersistentFlags().BoolVar(&verboseFlag, "verbose", false, "enable debug-level logging")
	rootCmd.PersistentFlags().BoolVar(&quietFlag, "quiet", false, "suppress all output except errors")

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

			embedder := engine.NewEmbeddingsGeneratorWithOllamaConfig(cfg.OllamaConfig())
			searchOpts := engine.SearchOptions{ExpandCfg: cfg.ExpandConfig()}

			results, err := engine.SearchHybridWithOptions(dbClient, dbClient, embedder, query, limitFlag, searchOpts)
			if err != nil {
				return err
			}

			// JSON output — never launch TUI in JSON mode.
			if jsonFlag {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			useTUI := tuiFlag || (!plainFlag && !jsonFlag && isatty.IsTerminal(os.Stdout.Fd()))
			if useTUI {
				return tui.Run(query, results)
			}

			writeSearchHuman(os.Stdout, results)
			return nil
		},
	}
	searchCmd.Flags().IntVarP(&limitFlag, "limit", "l", 5, "Number of search results to return")
	searchCmd.Flags().BoolVar(&jsonFlag, "json", false, "Output results in JSON format")
	searchCmd.Flags().BoolVar(&tuiFlag, "tui", false, "Launch interactive TUI browser for results")
	searchCmd.Flags().BoolVar(&plainFlag, "plain", false, "Output plain human-readable results instead of launching the TUI")
	rootCmd.AddCommand(searchCmd)

	// 2. Index Command
	indexCmd := &cobra.Command{
		Use:   "index [folder_path]",
		Short: "Crawl and index a local directory, URL, or stdin",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dbClient, err := db.Open()
			if err != nil {
				return err
			}
			defer dbClient.Close()

			embedder := engine.NewEmbeddingsGeneratorWithOllamaConfig(cfg.OllamaConfig())

			if urlFlag != "" {
				fmt.Fprintf(os.Stderr, "Indexing URL: %s...\n", urlFlag)
				return engine.IndexURL(dbClient, embedder, urlFlag)
			}

			if stdinFlag {
				source := sourceFlag
				if source == "" {
					source = "stdin"
				}
				fmt.Fprintf(os.Stderr, "Indexing from stdin (source: %s)...\n", source)
				return engine.IndexStdin(dbClient, embedder, os.Stdin, source)
			}

			if len(args) == 0 {
				return fmt.Errorf("folder path, --url, or --stdin required")
			}
			dirPath := args[0]

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
	indexCmd.Flags().StringVar(&urlFlag, "url", "", "Index content from a URL")
	indexCmd.Flags().BoolVar(&stdinFlag, "stdin", false, "Index content from stdin")
	indexCmd.Flags().StringVar(&sourceFlag, "source", "", "Source label for stdin content (used with --stdin)")
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

	// 5. Config Command
	var configSetKey string
	var configSetValue string
	var configJSONFlag bool
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "View and edit settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configSetKey != "" {
				if err := config.SetValue(cfgFile, configSetKey, configSetValue, &cfg); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Set %s = %s in %s\n", configSetKey, configSetValue, cfgFile)
				return nil
			}
			if !configJSONFlag {
				fmt.Fprintf(os.Stderr, "Config file: %s\n", cfgFile)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(cfg)
		},
	}
	configCmd.Flags().StringVar(&configSetKey, "set-key", "", "Set a config key (e.g. ollama_url, model)")
	configCmd.Flags().StringVar(&configSetValue, "set-value", "", "Value for the config key set via --set-key")
	configCmd.Flags().BoolVar(&configJSONFlag, "json", false, "Output config in JSON format only (no file path)")
	rootCmd.AddCommand(configCmd)

	// 5b. Migrate Command
	migrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate configuration from JSON to TOML format",
		RunE: func(cmd *cobra.Command, args []string) error {
			config.MigrateJSONToTOML()
			fmt.Fprintf(os.Stderr, "Migration complete. Config file: %s\n", config.GlobalPath())
			return nil
		},
	}
	rootCmd.AddCommand(migrateCmd)

	// 5c. Quantize Command
	var quantBitsFlag int
	var quantSeedFlag int
	quantizeCmd := &cobra.Command{
		Use:   "quantize",
		Short: "Backfill quantized vector sidecars for approximate search",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbClient, err := db.Open()
			if err != nil {
				return err
			}
			defer dbClient.Close()

			count, err := engine.BackfillQuantSidecars(dbClient, quantBitsFlag, quantSeedFlag, func(processed, total int) {
				fmt.Fprintf(os.Stderr, "\rBackfilling sidecars: %d/%d", processed, total)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "\nDone: %d chunks backfilled\n", count)
			return nil
		},
	}
	quantizeCmd.Flags().IntVar(&quantBitsFlag, "bits", 4, "Quantization bit width (2, 3, or 4)")
	quantizeCmd.Flags().IntVar(&quantSeedFlag, "seed", 42, "Random rotation seed")
	rootCmd.AddCommand(quantizeCmd)

	// 6. Version Command
	var checkUpdate bool
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version number of Symaira-Seek",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("symseek version %s\n", version)
			if checkUpdate {
				ctx := context.Background()
				checker := updatecheck.NewChecker("danieljustus", "symaira-seek")
				release, err := checker.Check(ctx, version)
				if err != nil {
					return fmt.Errorf("update check failed: %w", err)
				}
				if release != nil {
					fmt.Printf("New version available: %s\n", release.TagName)
					fmt.Printf("Download: %s\n", release.HTMLURL)
				} else {
					fmt.Println("You are running the latest version.")
				}
			}
			return nil
		},
	}
	versionCmd.Flags().BoolVar(&checkUpdate, "check", false, "Check for updates on GitHub")
	rootCmd.AddCommand(versionCmd)

	// 7. Serve Command
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Launch the MCP server or HTTP REST daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if portFlag > 0 {
				fmt.Fprintf(os.Stderr, "HTTP REST Server implementation starting on port %d...\n", portFlag)
				return startHTTPServer(portFlag)
			}
			fmt.Fprintln(os.Stderr, "MCP server starting over stdio...")
			return startMCPServer()
		},
	}
	serveCmd.Flags().IntVarP(&portFlag, "port", "p", 0, "Launch HTTP REST server on this port instead of stdio MCP")
	rootCmd.AddCommand(serveCmd)

	return rootCmd
}

func initConfig() {
	if cfgFile == "" {
		cfgFile = config.GlobalPath()
	}

	loaded, err := config.LoadFromPath(cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "symseek: could not load config: %v; using built-in defaults\n", err)
		cfg = *config.DefaultConfig()
		return
	}
	cfg = *loaded
}

func startHTTPServer(port int) error {
	cooldown := time.Duration(cfg.IndexCooldownSeconds) * time.Second
	if cooldown <= 0 {
		cooldown = 5 * time.Second
	}
	return server.StartHTTPServer(port, cfg.OllamaConfig(), cooldown, cfg.QuantDBConfig(), cfg.RerankConfig(), cfg.ExpandConfig())
}

func startMCPServer() error {
	mcp.ServerVersion = version
	return mcp.StartServer(cfg.OllamaConfig(), cfg.QuantDBConfig(), cfg.RerankConfig(), cfg.ExpandConfig())
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
