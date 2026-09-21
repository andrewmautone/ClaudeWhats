package cli

import (
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/spf13/cobra"
)

func printMessages(msgs []store.Message) {
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
		printf("%s %s (%s): %s\n", fmtTS(m.TS), m.Sender, m.Type, body)
	}
}

func init() {
	var since string
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
			msgs, err := s.ReadMessages(chat.JID, sv, limit)
			if err != nil {
				return err
			}
			kick(cfg)
			return emit(msgs, func() {
				printf("# %s (%s)\n", chat.Name, chat.JID)
				printMessages(msgs)
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "janela: 24h, 7d, 2026-09-21")
	cmd.Flags().IntVar(&limit, "limit", 200, "máximo de mensagens (as mais recentes)")
	root.AddCommand(cmd)
}
