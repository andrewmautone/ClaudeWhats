package cli

import (
	"strconv"
	"strings"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/spf13/cobra"
)

// parseIntArg parses a wake part/T argument; empty means "unset" (0).
func parseIntArg(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

// splitNote splits Note's output ("Saved as #N.\n" plus an optional nap
// prompt) into the id and the rest, trimmed.
func splitNote(text string) (id int, rest string) {
	first, tail, _ := strings.Cut(text, "\n")
	first = strings.TrimSuffix(strings.TrimPrefix(first, "Saved as #"), ".")
	id, _ = strconv.Atoi(first)
	return id, strings.TrimSpace(tail)
}

// memErr prints text (already Python-compatible, verbatim) to out and
// returns the sentinel errSilent so Execute exits 1 without adding
// "erro:" or printing again.
func memErr(text string) error {
	printf("%s", text)
	return errSilent
}

func init() {
	mem := &cobra.Command{
		Use:   "memory",
		Short: "Memória permanente do agente (compatível com o OptMem memo)",
	}

	wake := &cobra.Command{
		Use:   "wake [part] [T]",
		Short: "Lê a memória (cobertura mais recente primeiro)",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := memoryStore(cfg)
			if err != nil {
				return err
			}
			part, T := 0, 0
			if len(args) > 0 {
				if part, err = parseIntArg(args[0]); err != nil {
					return err
				}
			}
			if len(args) > 1 {
				if T, err = parseIntArg(args[1]); err != nil {
					return err
				}
			}
			text, err := s.Wake(part, T)
			if err != nil {
				return memErr(err.Error())
			}
			printf("%s", text)
			return nil
		},
	}

	note := &cobra.Command{
		Use:   "note <text>",
		Short: "Registra uma memória",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := memoryStore(cfg)
			if err != nil {
				return err
			}
			text, err := s.Note(args[0])
			if err != nil {
				return memErr(err.Error())
			}
			id, nap := splitNote(text)
			return emit(map[string]any{"id": id, "nap": nap}, func() {
				printf("%s", text)
			})
		},
	}

	nap := &cobra.Command{
		Use:   "nap [a-b] [text]",
		Short: "Salva a compressão de um bloco e mostra a próxima pendente",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := memoryStore(cfg)
			if err != nil {
				return err
			}
			var id, text string
			if len(args) > 0 {
				id = args[0]
			}
			if len(args) > 1 {
				text = args[1]
			}
			out, err := s.Nap(id, text)
			if err != nil {
				return memErr(err.Error())
			}
			printf("%s", out)
			return nil
		},
	}

	recall := &cobra.Command{
		Use:   "recall <regex>",
		Short: "Busca em toda a memória",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := memoryStore(cfg)
			if err != nil {
				return err
			}
			if jsonOut {
				lines, err := s.RecallLines(args[0], 0)
				if err != nil {
					return memErr(err.Error())
				}
				return emit(map[string]any{"lines": lines}, func() {})
			}
			text, err := s.Recall(args[0])
			if err != nil {
				return memErr(err.Error())
			}
			printf("%s", text)
			return nil
		},
	}

	zoom := &cobra.Command{
		Use:   "zoom <a-b>",
		Short: "Abre um nó da árvore: suas duas metades",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := memoryStore(cfg)
			if err != nil {
				return err
			}
			text, err := s.Zoom(args[0])
			if err != nil {
				return memErr(err.Error())
			}
			printf("%s", text)
			return nil
		},
	}

	forget := &cobra.Command{
		Use:   "forget <a-b>",
		Short: "Descarta uma compressão e tudo construído sobre ela",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			s, err := memoryStore(cfg)
			if err != nil {
				return err
			}
			text, err := s.Forget(args[0])
			if err != nil {
				return memErr(err.Error())
			}
			printf("%s", text)
			return nil
		},
	}

	mem.AddCommand(wake, note, nap, recall, zoom, forget)
	root.AddCommand(mem)
}
