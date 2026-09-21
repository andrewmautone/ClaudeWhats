package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var jsonOut bool

// Version is set via ldflags at release build time (see .goreleaser.yaml).
var Version = "dev"

var root = &cobra.Command{
	Use:           "claudewhats",
	Short:         "WhatsApp local archive + Gemini for Claude",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "output JSON")
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Mostra a versão do binário",
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(map[string]any{"version": Version}, func() {
				printf("claudewhats %s\n", Version)
			})
		},
	})
}

// emit prints v as JSON when --json, otherwise calls text().
func emit(v any, text func()) error {
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	text()
	return nil
}

// errSilent marks an error whose text was already written to out by the
// command itself (verbatim, Python-compatible text); Execute must not
// prepend "erro:" or print it again.
var errSilent = errors.New("silent")

func Execute() {
	if err := root.Execute(); err != nil {
		if err != errSilent {
			fmt.Fprintln(os.Stderr, "erro:", err)
		}
		os.Exit(1)
	}
}
