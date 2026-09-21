package cli

import (
	"context"
	"time"

	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	var count int
	cmd := &cobra.Command{
		Use:   "sync <chat>",
		Short: "Pede ao WhatsApp mensagens antigas desta conversa",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			chat, err := s.ResolveChat(args[0])
			closeStore(s)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cl, err := daemon.EnsureRunning(ctx, cfg, 20*time.Second)
			if err != nil {
				return err
			}
			if err := cl.Sync(ctx, chat.JID, count); err != nil {
				return err
			}
			return emit(map[string]any{"chat": chat.JID, "requested": count}, func() {
				printf("pedido enviado ao celular; as mensagens chegam no banco em alguns segundos (rode `read` de novo)\n")
			})
		},
	}
	cmd.Flags().IntVar(&count, "count", 50, "quantas mensagens antigas pedir")
	root.AddCommand(cmd)
}
