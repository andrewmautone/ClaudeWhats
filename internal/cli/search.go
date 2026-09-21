package cli

import (
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	var since, until, chat string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <texto>",
		Short: "Busca full-text em texto e transcrições",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			chatJID := ""
			if chat != "" {
				c, err := s.ResolveChat(chat)
				if err != nil {
					return err
				}
				chatJID = c.JID
			}
			sv, err := parseSince(since)
			if err != nil {
				return err
			}
			uv, err := parseUntil(until)
			if err != nil {
				return err
			}
			q := ""
			for i, a := range args {
				if i > 0 {
					q += " "
				}
				q += a
			}
			msgs, err := s.Search(q, chatJID, sv, uv, limit)
			if err != nil {
				return err
			}
			kick(cfg)
			return emit(msgs, func() {
				for _, m := range msgs {
					printf("[%s] ", s.ChatName(m.ChatJID))
					printMessages([]store.Message{m})
				}
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "janela")
	cmd.Flags().StringVar(&until, "until", "", "limite superior: 24h, 7d, 2026-09-21")
	cmd.Flags().StringVar(&chat, "chat", "", "limitar a um chat")
	cmd.Flags().IntVar(&limit, "limit", 50, "máximo de resultados; 0 = todos")
	root.AddCommand(cmd)
}
