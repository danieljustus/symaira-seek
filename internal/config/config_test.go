package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig_StableValues(t *testing.T) {
	got := DefaultConfig()
	if got.OllamaURL == "" {
		t.Error("default OllamaURL must be non-empty")
	}
	if got.Model == "" {
		t.Error("default Model must be non-empty")
	}
	if got.TimeoutSeconds <= 0 {
		t.Error("default TimeoutSeconds must be positive")
	}
	if got.RetryCount < 0 {
		t.Error("default RetryCount must be non-negative")
	}
	if got.RetryBackoffMS <= 0 {
		t.Error("default RetryBackoffMS must be positive")
	}
	got2 := DefaultConfig()
	if *got != *got2 {
		t.Error("DefaultConfig must be deterministic")
	}
}

func TestOllamaConfig(t *testing.T) {
	c := Config{
		OllamaURL:      "http://x.test/api",
		Model:          "m",
		TimeoutSeconds: 30,
		RetryCount:     4,
		RetryBackoffMS: 250,
	}
	oc := c.OllamaConfig()
	if oc.URL != c.OllamaURL {
		t.Errorf("URL: got %q, want %q", oc.URL, c.OllamaURL)
	}
	if oc.Model != c.Model {
		t.Errorf("Model: got %q, want %q", oc.Model, c.Model)
	}
	if oc.Timeout.Seconds() != 30 {
		t.Errorf("Timeout: got %v, want 30s", oc.Timeout)
	}
	if oc.RetryCount != 4 {
		t.Errorf("RetryCount: got %d, want 4", oc.RetryCount)
	}
	if oc.RetryBackoff.Milliseconds() != 250 {
		t.Errorf("RetryBackoff: got %v, want 250ms", oc.RetryBackoff)
	}
}

func TestSetValue_AcceptsTimeoutSeconds(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	if err := SetValue(cfgFile, "timeout_seconds", "45", cfg); err != nil {
		t.Fatalf("SetValue(timeout_seconds): %v", err)
	}
	if cfg.TimeoutSeconds != 45 {
		t.Errorf("expected TimeoutSeconds=45, got %d", cfg.TimeoutSeconds)
	}
}

func TestSetValue_AcceptsRetryCount(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	if err := SetValue(cfgFile, "retry_count", "5", cfg); err != nil {
		t.Fatalf("SetValue(retry_count): %v", err)
	}
	if cfg.RetryCount != 5 {
		t.Errorf("expected RetryCount=5, got %d", cfg.RetryCount)
	}
}

func TestSetValue_RejectsInvalidTimeout(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	if err := SetValue(cfgFile, "timeout_seconds", "zero", cfg); err == nil {
		t.Error("expected error for non-numeric timeout")
	}
}

func TestSetValue_RejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	if err := SetValue(cfgFile, "not-a-key", "x", cfg); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestSetValue_RejectsEmptyModel(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	err := SetValue(cfgFile, "model", "", cfg)
	if err == nil {
		t.Error("expected error for empty model value")
	} else if err.Error() != `--set-value is required for key "model"` {
		t.Errorf("expected actionable error message, got %q", err.Error())
	}
}

func TestSetValue_RejectsEmptyOllamaURL(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	err := SetValue(cfgFile, "ollama_url", "", cfg)
	if err == nil {
		t.Error("expected error for empty ollama_url value")
	} else if err.Error() != `--set-value is required for key "ollama_url"` {
		t.Errorf("expected actionable error message, got %q", err.Error())
	}
}

func TestSetValue_AcceptsValidModel(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	if err := SetValue(cfgFile, "model", "mxbai-embed-large", cfg); err != nil {
		t.Fatalf("SetValue(model): %v", err)
	}
	if cfg.Model != "mxbai-embed-large" {
		t.Errorf("expected Model=mxbai-embed-large, got %q", cfg.Model)
	}
}

func TestSetValue_PersistsToFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")
	cfg := DefaultConfig()

	if err := SetValue(cfgFile, "model", "test-persist", cfg); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !filepath.IsAbs(cfgFile) {
		t.Fatal("expected absolute config file path")
	}
	if len(data) == 0 {
		t.Error("expected non-empty config file after SetValue")
	}
}

func TestSave_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "subdir", "config.toml")
	cfg := DefaultConfig()

	if err := Save(cfgFile, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		t.Error("expected config file to exist after Save")
	}
}

func TestMigrateJSONToTOML_MigratesWhenTOMLMissing(t *testing.T) {
	dir := t.TempDir()

	configDir := filepath.Join(dir, ".config", "symseek")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(configDir, "config.json")
	jsonData := `{"ollama_url":"http://migrated.test/api","model":"migrated-model","timeout_seconds":60,"retry_count":3,"retry_backoff_ms":100}`
	if err := os.WriteFile(jsonPath, []byte(jsonData), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", dir)

	MigrateJSONToTOML()

	tomlPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("expected TOML file to exist: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty TOML file")
	}
}

func TestMigrateJSONToTOML_SkipsWhenTOMLExists(t *testing.T) {
	dir := t.TempDir()

	configDir := filepath.Join(dir, ".config", "symseek")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	tomlPath := filepath.Join(configDir, "config.toml")
	existingTOML := `ollama_url = "http://existing.test/api"
model = "existing-model"
`
	if err := os.WriteFile(tomlPath, []byte(existingTOML), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", dir)

	MigrateJSONToTOML()

	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existingTOML {
		t.Errorf("existing TOML was modified: %q", string(data))
	}
}

func TestMigrateJSONToTOML_SkipsWhenNeitherExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	MigrateJSONToTOML()

	tomlPath := filepath.Join(dir, ".config", "symseek", "config.toml")
	if _, err := os.Stat(tomlPath); !os.IsNotExist(err) {
		t.Error("expected no TOML file when neither config exists")
	}
}
