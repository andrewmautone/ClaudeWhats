package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	var since, kind string
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Lista conversas (do banco local)",
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
			chats, err := s.ListChats(sv, kind)
			if err != nil {
				return err
			}
			kick(cfg)
			return emit(chats, func() {
				for _, c := range chats {
					printf("%-6s %-40s %5d msgs  último: %s  %s\n", c.Kind, c.Name, c.Count, fmtTS(c.LastMsgAt), c.JID)
				}
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "janela: 24h, 7d, 2026-09-21")
	cmd.Flags().StringVar(&kind, "kind", "", "dm ou group")
	root.AddCommand(cmd)
}
