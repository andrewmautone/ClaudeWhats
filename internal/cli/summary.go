package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/andrewmautone/claudewhats/internal/gemini"
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/spf13/cobra"
)

type summarizer interface {
	Summarize(ctx context.Context, instructions, text string) (string, error)
}

var summarizerOverride summarizer

const chatPrompt = "Resuma esta conversa de WhatsApp em português, em tópicos curtos: assuntos, decisões, pedidos e pendências. Cite quem falou quando importar. Se não houver nada relevante, diga 'nada relevante'."
const overallPrompt = "Estes são resumos de várias conversas de WhatsApp. Faça um resumo geral em português destacando o que exige ação ou resposta, em ordem de importância."

func buildTranscript(s *store.Store, msgs []store.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		body := m.Text
		if m.Transcript != "" {
			body = fmt.Sprintf("(%s) %s", m.Type, m.Transcript)
			if m.Text != "" {
				body += " [legenda: " + m.Text + "]"
			}
		} else if m.Type != "text" {
			body = fmt.Sprintf("(%s) %s", m.Type, m.Text)
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", fmtTS(m.TS), m.Sender, body)
	}
	return strings.TrimSpace(b.String())
}

type chatSummary struct {
	Chat    string `json:"chat"`
	JID     string `json:"jid"`
	Count   int    `json:"count"`
	Summary string `json:"summary"`
}

func init() {
	var since, until, chat string
	var limit int
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Resume conversas do período com Gemini",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			sv, err := parseSince(since)
			if err != nil {
				return err
			}
			uv, err := parseUntil(until)
			if err != nil {
				return err
			}
			var ai summarizer = summarizerOverride
			if ai == nil {
				ai = gemini.New(cfg.GeminiAPIKey, cfg.GeminiModel)
			}
			var chats []store.Chat
			var resolved store.Chat
			if chat != "" {
				c, err := s.ResolveChat(chat)
				if err != nil {
					return err
				}
				resolved = c
				chats = []store.Chat{c}
			} else if chats, err = s.ListChats(sv, ""); err != nil {
				return err
			}
			var mem []string
			if chat != "" && !noMemory {
				if ms, err := memoryStore(cfg); err == nil {
					mem = memoryBlock(ms, resolved)
				}
			}
			ctx := context.Background()
			var results []chatSummary
			for _, c := range chats {
				msgs, err := s.ReadMessages(c.JID, sv, uv, limit)
				if err != nil {
					return err
				}
				if len(msgs) == 0 {
					continue
				}
				sum, err := ai.Summarize(ctx, chatPrompt, buildTranscript(s, msgs))
				if err != nil {
					return fmt.Errorf("%s: %w", c.Name, err)
				}
				results = append(results, chatSummary{Chat: c.Name, JID: c.JID, Count: len(msgs), Summary: sum})
			}
			overall := ""
			if chat == "" && len(results) > 1 {
				var b strings.Builder
				for _, r := range results {
					fmt.Fprintf(&b, "## %s (%d msgs)\n%s\n\n", r.Chat, r.Count, r.Summary)
				}
				if overall, err = ai.Summarize(ctx, overallPrompt, b.String()); err != nil {
					return err
				}
			}
			kick(cfg)
			return emit(map[string]any{"chats": results, "overall": overall, "memory": mem}, func() {
				if len(mem) > 0 {
					printf("## memória\n")
					for _, l := range mem {
						printf("%s\n", l)
					}
					printf("\n")
				}
				for _, r := range results {
					printf("## %s (%d msgs)\n%s\n\n", r.Chat, r.Count, r.Summary)
				}
				if overall != "" {
					printf("## Geral\n%s\n", overall)
				}
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "24h", "janela: 24h, 7d, 2026-09-21")
	cmd.Flags().StringVar(&until, "until", "", "limite superior: 24h, 7d, 2026-09-21")
	cmd.Flags().StringVar(&chat, "chat", "", "só este chat")
	cmd.Flags().IntVar(&limit, "limit", 500, "máximo de mensagens por chat; 0 = todas")
	root.AddCommand(cmd)
}
