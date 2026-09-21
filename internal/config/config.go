package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GeminiAPIKey string        `yaml:"gemini_api_key"`
	GeminiModel  string        `yaml:"gemini_model"`
	Port         int           `yaml:"port"`
	IdleTimeout  time.Duration `yaml:"idle_timeout"`

	Home     string `yaml:"-"`
	DBPath   string `yaml:"-"`
	MediaDir string `yaml:"-"`
	LogPath  string `yaml:"-"`
	PidPath  string `yaml:"-"`
	// QR files written by the daemon while pairing (see `claudewhats pair`).
	QRPNGPath  string `yaml:"-"`
	QRTxtPath  string `yaml:"-"`
	QRHTMLPath string `yaml:"-"`
}

// Home returns the data directory (CLAUDEWHATS_HOME or ~/.claudewhats).
func Home() string {
	if h := os.Getenv("CLAUDEWHATS_HOME"); h != "" {
		return h
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return ".claudewhats"
	}
	return filepath.Join(u, ".claudewhats")
}

func Load() (*Config, error) {
	home := Home()
	if err := os.MkdirAll(filepath.Join(home, "media"), 0o700); err != nil {
		return nil, err
	}
	cfg := &Config{GeminiModel: "gemini-2.5-flash", Port: 7411, IdleTimeout: 30 * time.Minute}
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err == nil {
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		cfg.GeminiAPIKey = k
	}
	cfg.Home = home
	cfg.DBPath = filepath.Join(home, "data.db")
	cfg.MediaDir = filepath.Join(home, "media")
	cfg.LogPath = filepath.Join(home, "daemon.log")
	cfg.PidPath = filepath.Join(home, "daemon.pid")
	cfg.QRPNGPath = filepath.Join(home, "qr.png")
	cfg.QRTxtPath = filepath.Join(home, "qr.txt")
	cfg.QRHTMLPath = filepath.Join(home, "qr.html")
	return cfg, nil
}
