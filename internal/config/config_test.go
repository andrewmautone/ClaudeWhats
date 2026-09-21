package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsAndYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDEWHATS_HOME", home)
	t.Setenv("GEMINI_API_KEY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7411 || cfg.IdleTimeout != 30*time.Minute || cfg.GeminiModel != "gemini-2.5-flash" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.DBPath != filepath.Join(home, "data.db") {
		t.Fatalf("dbpath %s", cfg.DBPath)
	}
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("gemini_api_key: abc\nport: 8000\nidle_timeout: 5m\n"), 0o600)
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GeminiAPIKey != "abc" || cfg.Port != 8000 || cfg.IdleTimeout != 5*time.Minute {
		t.Fatalf("yaml not applied: %+v", cfg)
	}
	t.Setenv("GEMINI_API_KEY", "env")
	cfg, _ = Load()
	if cfg.GeminiAPIKey != "env" {
		t.Fatal("env should override yaml")
	}
}
