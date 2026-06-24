// Package config provides configuration management for symseek using corekit's configkit.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/danieljustus/symaira-corekit/configkit"
	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
)

// Config holds user configuration for symseek.
type Config struct {
	OllamaURL            string `json:"ollama_url" toml:"ollama_url"`
	Model                string `json:"model" toml:"model"`
	EmbeddingDim         int    `json:"embedding_dim" toml:"embedding_dim"`
	TimeoutSeconds       int    `json:"timeout_seconds" toml:"timeout_seconds"`
	RetryCount           int    `json:"retry_count" toml:"retry_count"`
	RetryBackoffMS       int    `json:"retry_backoff_ms" toml:"retry_backoff_ms"`
	IndexCooldownSeconds int    `json:"index_cooldown_seconds" toml:"index_cooldown_seconds"`
	VectorBackend        string `json:"vector_backend" toml:"vector_backend"`

	// Quantized vector search (opt-in, off by default).
	VectorQuantization       string `json:"vector_quantization" toml:"vector_quantization"`             // "off" | "turbo-prod"
	VectorQuantBits          int    `json:"vector_quant_bits" toml:"vector_quant_bits"`                 // 2, 3, or 4
	VectorQuantizedShortlist int    `json:"vector_quantized_shortlist" toml:"vector_quantized_shortlist"` // approximate shortlist size
	VectorExactRerank        bool   `json:"vector_exact_rerank" toml:"vector_exact_rerank"`             // exact cosine rerank on shortlist

	// LLM re-ranking (opt-in, off by default).
	RerankQuery          bool   `json:"rerank_query" toml:"rerank_query"`                       // enable Ollama re-ranking of search results
	RerankModel          string `json:"rerank_model" toml:"rerank_model"`                       // chat model for re-ranking; empty = reuse embedding model
	RerankTimeoutSeconds int    `json:"rerank_timeout_seconds" toml:"rerank_timeout_seconds"`   // per-request timeout for reranking

	// HyDE query expansion (opt-in, off by default).
	ExpandQuery          bool   `json:"expand_query" toml:"expand_query"`                       // enable HyDE query expansion via Ollama chat
	ExpandModel          string `json:"expand_model" toml:"expand_model"`                       // chat model for expansion; empty = reuse embedding model
	ExpandTimeoutSeconds int    `json:"expand_timeout_seconds" toml:"expand_timeout_seconds"`   // per-request timeout for expansion
}

// DefaultConfig returns the default configuration values.
func DefaultConfig() *Config {
	return &Config{
		OllamaURL:                 "http://localhost:11434/api/embeddings",
		Model:                     "nomic-embed-text",
		TimeoutSeconds:            120,
		RetryCount:                2,
		RetryBackoffMS:            500,
		IndexCooldownSeconds:      5,
		VectorBackend:             "sqlite",
		VectorQuantization:        "off",
		VectorQuantBits:           4,
		VectorQuantizedShortlist:  200,
		VectorExactRerank:         true,
		RerankQuery:               false,
		RerankModel:               "",
		RerankTimeoutSeconds:      120,
		ExpandQuery:               false,
		ExpandModel:               "",
		ExpandTimeoutSeconds:      120,
	}
}

func Load() (*Config, error) {
	return LoadFromPath(GlobalPath())
}

func LoadFromPath(path string) (*Config, error) {
	MigrateJSONToTOMLAt(path)

	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to decode config file: %w", err)
	}
	return cfg, nil
}

func Reload() (*Config, error) {
	return Load()
}

func GlobalPath() string {
	return configkit.DefaultPath("symseek")
}

// OllamaConfig converts a Config to the engine.OllamaConfig format.
func (c *Config) OllamaConfig() engine.OllamaConfig {
	return engine.OllamaConfig{
		URL:          c.OllamaURL,
		Model:        c.Model,
		Dim:          c.EmbeddingDim,
		Timeout:      time.Duration(c.TimeoutSeconds) * time.Second,
		RetryCount:   c.RetryCount,
		RetryBackoff: time.Duration(c.RetryBackoffMS) * time.Millisecond,
	}
}

// RerankConfig converts the config's rerank fields to an engine.RerankConfig.
func (c *Config) RerankConfig() engine.RerankConfig {
	model := c.RerankModel
	if model == "" {
		model = c.Model
	}
	timeout := time.Duration(c.RerankTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return engine.RerankConfig{
		Enabled: c.RerankQuery,
		URL:     c.OllamaURL,
		Model:   model,
		Timeout: timeout,
	}
}

// ExpandConfig converts the config's expansion fields to an engine.ExpandConfig.
func (c *Config) ExpandConfig() engine.ExpandConfig {
	model := c.ExpandModel
	if model == "" {
		model = c.Model
	}
	timeout := time.Duration(c.ExpandTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return engine.ExpandConfig{
		Enabled: c.ExpandQuery,
		URL:     c.OllamaURL,
		Model:   model,
		Timeout: timeout,
	}
}

// QuantDBConfig returns the QuantConfig for db.DB, or nil when quantization
// is disabled.
func (c *Config) QuantDBConfig() *db.QuantConfig {
	if c.VectorQuantization == "off" || c.VectorQuantization == "" {
		return nil
	}
	return &db.QuantConfig{
		Enabled:     true,
		BitWidth:    c.VectorQuantBits,
		Shortlist:   c.VectorQuantizedShortlist,
		ExactRerank: c.VectorExactRerank,
		Seed:        42,
	}
}

func MigrateJSONToTOMLAt(tomlPath string) {
	jsonPath := filepath.Join(filepath.Dir(tomlPath), "config.json")

	if _, err := os.Stat(tomlPath); err == nil {
		return
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}

	dir := filepath.Dir(tomlPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}

	f, err := os.OpenFile(tomlPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	defer f.Close()

	_ = toml.NewEncoder(f).Encode(cfg)
}

func MigrateJSONToTOML() {
	MigrateJSONToTOMLAt(GlobalPath())
}

// Save writes the config to the specified TOML file.
func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	return nil
}

// SetValue validates and sets a single config key, then saves to disk.
func SetValue(cfgFile string, key, value string, cfg *Config) error {
	switch key {
	case "ollama_url":
		if value == "" {
			return fmt.Errorf("--set-value is required for key %q", key)
		}
		cfg.OllamaURL = value
	case "model":
		if value == "" {
			return fmt.Errorf("--set-value is required for key %q", key)
		}
		cfg.Model = value
	case "embedding_dim":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid %s value %q (must be a non-negative integer; 0 means auto-detect)", key, value)
		}
		cfg.EmbeddingDim = n
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
	case "index_cooldown_seconds":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid %s value %q (must be a positive integer)", key, value)
		}
		cfg.IndexCooldownSeconds = n
	case "vector_backend":
		if value == "" {
			return fmt.Errorf("--set-value is required for key %q", key)
		}
		if value != "sqlite" {
			return fmt.Errorf("invalid vector_backend %q (only \"sqlite\" is currently supported)", value)
		}
		cfg.VectorBackend = value
	case "vector_quantization":
		if value == "" {
			return fmt.Errorf("--set-value is required for key %q", key)
		}
		if value != "off" && value != "turbo-prod" {
			return fmt.Errorf("invalid vector_quantization %q (supported: \"off\", \"turbo-prod\")", value)
		}
		cfg.VectorQuantization = value
	case "vector_quant_bits":
		n, err := strconv.Atoi(value)
		if err != nil || (n != 2 && n != 3 && n != 4) {
			return fmt.Errorf("invalid %s value %q (must be 2, 3, or 4)", key, value)
		}
		cfg.VectorQuantBits = n
	case "vector_quantized_shortlist":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid %s value %q (must be a positive integer)", key, value)
		}
		cfg.VectorQuantizedShortlist = n
	case "vector_exact_rerank":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid %s value %q (must be true or false)", key, value)
		}
		cfg.VectorExactRerank = b
	case "rerank_query":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid %s value %q (must be true or false)", key, value)
		}
		cfg.RerankQuery = b
	case "rerank_model":
		cfg.RerankModel = value
	case "rerank_timeout_seconds":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid %s value %q (must be a positive integer)", key, value)
		}
		cfg.RerankTimeoutSeconds = n
	case "expand_query":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid %s value %q (must be true or false)", key, value)
		}
		cfg.ExpandQuery = b
	case "expand_model":
		cfg.ExpandModel = value
	case "expand_timeout_seconds":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid %s value %q (must be a positive integer)", key, value)
		}
		cfg.ExpandTimeoutSeconds = n
	default:
		return fmt.Errorf("unknown config key %q (supported: ollama_url, model, embedding_dim, timeout_seconds, retry_count, retry_backoff_ms, index_cooldown_seconds, vector_backend, vector_quantization, vector_quant_bits, vector_quantized_shortlist, vector_exact_rerank, rerank_query, rerank_model, rerank_timeout_seconds, expand_query, expand_model, expand_timeout_seconds)", key)
	}
	return Save(cfgFile, cfg)
}
