package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/spf13/cobra"
)

func printMessages(msgs []store.Message) {
	printMessagesTo(printf, msgs)
}

// printMessagesTo renders msgs as "ts sender (type): body" lines, using the
// given print function (so callers can redirect to a file).
func printMessagesTo(pf func(format string, a ...any), msgs []store.Message) {
	for _, m := range msgs {
		body := m.Text
		switch {
		case m.Transcript != "":
			body = m.Transcript
			if m.Text != "" {
				body += " [legenda: " + m.Text + "]"
			}
		case m.TranscriptStatus == "pending":
			body = "[transcrição pendente]: " + m.Text
		case m.TranscriptStatus == "failed":
			body = "[transcrição falhou]: " + m.Text
		}
		pf("%s %s (%s): %s\n", fmtTS(m.TS), m.Sender, m.Type, body)
	}
}

func init() {
	var since, until, outPath string
	var limit int
	cmd := &cobra.Command{
		Use:   "read <chat>",
		Short: "Mensagens de uma conversa (jid, número, nome de contato ou de grupo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			chat, err := s.ResolveChat(args[0])
			if err != nil {
				return err
			}
			sv, err := parseSince(since)
			if err != nil {
				return err
			}
			uv, err := parseUntil(until)
			if err != nil {
				return err
			}
			msgs, err := s.ReadMessages(chat.JID, sv, uv, limit)
			if err != nil {
				return err
			}

			if outPath != "" {
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
					return err
				}
				f, err := os.Create(outPath)
				if err != nil {
					return err
				}
				defer f.Close()
				if jsonOut {
					enc := json.NewEncoder(f)
					enc.SetIndent("", "  ")
					if err := enc.Encode(map[string]any{"chat": chat, "messages": msgs}); err != nil {
						return err
					}
				} else {
					pf := func(format string, a ...any) { fmt.Fprintf(f, format, a...) }
					pf("# %s (%s)\n", chat.Name, chat.JID)
					printMessagesTo(pf, msgs)
				}
				return emit(map[string]any{"saved": outPath, "count": len(msgs)}, func() {
					printf("salvo: %s (%d mensagens)\n", outPath, len(msgs))
				})
			}

			var mem []string
			if !noMemory {
				if ms, err := memoryStore(cfg); err == nil {
					mem = memoryBlock(ms, chat)
				}
			}
			kick(cfg)
			return emit(map[string]any{"chat": chat, "memory": mem, "messages": msgs}, func() {
				printf("# %s (%s)\n", chat.Name, chat.JID)
				if len(mem) > 0 {
					printf("## memória\n")
					for _, l := range mem {
						printf("%s\n", l)
					}
					printf("\n")
				}
				printMessages(msgs)
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "janela: 24h, 7d, 2026-09-21")
	cmd.Flags().StringVar(&until, "until", "", "limite superior: 24h, 7d, 2026-09-21")
	cmd.Flags().IntVar(&limit, "limit", 200, "máximo de mensagens (as mais recentes); 0 = todas")
	cmd.Flags().StringVar(&outPath, "out", "", "salva a saída neste arquivo em vez de imprimir")
	root.AddCommand(cmd)
}
