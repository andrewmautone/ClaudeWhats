package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/memory"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// configKeys are the settings config set knows how to change.
var configKeys = map[string]bool{
	"gemini_api_key": true,
	"gemini_model":   true,
	"port":           true,
	"idle_timeout":   true,
}

// configFilePath is <home>/config.yaml.
func configFilePath() string {
	return filepath.Join(config.Home(), "config.yaml")
}

// readConfigFile loads config.yaml into a generic map, preserving keys this
// binary doesn't know about. A missing file yields an empty map.
func readConfigFile() (map[string]any, error) {
	b, err := os.ReadFile(configFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// writeConfigFile writes m to config.yaml atomically, mode 0600.
func writeConfigFile(m map[string]any) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	home := config.Home()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	path := configFilePath()
	tmp, err := os.CreateTemp(home, ".config-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// maskAPIKey returns k as "AIza…last4", or "(not set)" when empty.
func maskAPIKey(k string) string {
	if k == "" {
		return "(not set)"
	}
	if len(k) <= 8 {
		return "…" + k[len(k)-min(4, len(k)):]
	}
	return k[:4] + "…" + k[len(k)-4:]
}

// configSource reports where key comes from: env (only meaningful for
// gemini_api_key), config.yaml (present in the file), or default.
func configSource(fileMap map[string]any, key string) string {
	if key == "gemini_api_key" && os.Getenv("GEMINI_API_KEY") != "" {
		return "env"
	}
	if _, ok := fileMap[key]; ok {
		return "config.yaml"
	}
	return "default"
}

func init() {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Configuração do claudewhats",
	}

	configCmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Mostra a configuração efetiva",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fileMap, err := readConfigFile()
			if err != nil {
				return err
			}
			res := map[string]any{
				"home":       cfg.Home,
				"db_path":    cfg.DBPath,
				"export_dir": cfg.ExportDir,
				"memory_dir": memory.DefaultDir(),
				"port": map[string]any{
					"value":  cfg.Port,
					"source": configSource(fileMap, "port"),
				},
				"idle_timeout": map[string]any{
					"value":  cfg.IdleTimeout.String(),
					"source": configSource(fileMap, "idle_timeout"),
				},
				"gemini_model": map[string]any{
					"value":  cfg.GeminiModel,
					"source": configSource(fileMap, "gemini_model"),
				},
				"gemini_api_key": map[string]any{
					"value":  maskAPIKey(cfg.GeminiAPIKey),
					"source": configSource(fileMap, "gemini_api_key"),
				},
			}
			return emit(res, func() {
				printf("home: %s\n", cfg.Home)
				printf("db_path: %s\n", cfg.DBPath)
				printf("export_dir: %s\n", cfg.ExportDir)
				printf("memory_dir: %s\n", memory.DefaultDir())
				printf("port: %d (%s)\n", cfg.Port, configSource(fileMap, "port"))
				printf("idle_timeout: %s (%s)\n", cfg.IdleTimeout, configSource(fileMap, "idle_timeout"))
				printf("gemini_model: %s (%s)\n", cfg.GeminiModel, configSource(fileMap, "gemini_model"))
				printf("gemini_api_key: %s (%s)\n", maskAPIKey(cfg.GeminiAPIKey), configSource(fileMap, "gemini_api_key"))
			})
		},
	})

	configCmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Mostra o caminho do config.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configFilePath()
			return emit(map[string]any{"path": path}, func() {
				printf("%s\n", path)
			})
		},
	})

	configCmd.AddCommand(&cobra.Command{
		Use:   "set <key> <value>",
		Short: "Altera uma configuração (gemini_api_key, gemini_model, port, idle_timeout)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if !configKeys[key] {
				return fmt.Errorf("chave desconhecida: %q (use gemini_api_key, gemini_model, port ou idle_timeout)", key)
			}
			if value == "" {
				return fmt.Errorf("valor vazio para %q", key)
			}
			var stored any = value
			switch key {
			case "port":
				p, err := strconv.Atoi(value)
				if err != nil || p < 1 || p > 65535 {
					return fmt.Errorf("porta inválida: %q (use um inteiro entre 1 e 65535)", value)
				}
				stored = p
			case "idle_timeout":
				if _, err := time.ParseDuration(value); err != nil {
					return fmt.Errorf("idle_timeout inválido: %q (use algo como 30m ou 1h)", value)
				}
			}
			m, err := readConfigFile()
			if err != nil {
				return err
			}
			m[key] = stored
			if err := writeConfigFile(m); err != nil {
				return err
			}
			envNote := ""
			if key == "gemini_api_key" && os.Getenv("GEMINI_API_KEY") != "" {
				envNote = " (nota: a variável de ambiente GEMINI_API_KEY tem prioridade sobre o arquivo)"
			}
			return emit(map[string]any{"ok": true, "key": key}, func() {
				printf("ok: %s definido%s\n", key, envNote)
			})
		},
	})

	root.AddCommand(configCmd)
}
