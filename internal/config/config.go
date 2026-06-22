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
	"github.com/danieljustus/symaira-seek/internal/engine"
)

// Config holds user configuration for symseek.
type Config struct {
	OllamaURL            string `json:"ollama_url" toml:"ollama_url"`
	Model                string `json:"model" toml:"model"`
	TimeoutSeconds       int    `json:"timeout_seconds" toml:"timeout_seconds"`
	RetryCount           int    `json:"retry_count" toml:"retry_count"`
	RetryBackoffMS       int    `json:"retry_backoff_ms" toml:"retry_backoff_ms"`
	IndexCooldownSeconds int    `json:"index_cooldown_seconds" toml:"index_cooldown_seconds"`
}

// DefaultConfig returns the default configuration values.
func DefaultConfig() *Config {
	return &Config{
		OllamaURL:            "http://localhost:11434/api/embeddings",
		Model:                "nomic-embed-text",
		TimeoutSeconds:       120,
		RetryCount:           2,
		RetryBackoffMS:       500,
		IndexCooldownSeconds: 5,
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
		Timeout:      time.Duration(c.TimeoutSeconds) * time.Second,
		RetryCount:   c.RetryCount,
		RetryBackoff: time.Duration(c.RetryBackoffMS) * time.Millisecond,
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
	default:
		return fmt.Errorf("unknown config key %q (supported: ollama_url, model, timeout_seconds, retry_count, retry_backoff_ms, index_cooldown_seconds)", key)
	}
	return Save(cfgFile, cfg)
}
