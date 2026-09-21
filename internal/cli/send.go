package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	var yes bool
	cmd := &cobra.Command{
		Use:   "send <chat> <texto>",
		Short: "Envia texto (pede confirmação sem --yes)",
		Args:  cobra.MinimumNArgs(2),
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
			text := strings.Join(args[1:], " ")
			if !yes {
				fmt.Fprintf(os.Stderr, "Enviar para %s (%s):\n%s\nConfirma? [s/N] ", chat.Name, chat.JID, text)
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if l := strings.ToLower(strings.TrimSpace(line)); l != "s" && l != "sim" && l != "y" {
					return fmt.Errorf("cancelado")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cl, err := daemon.EnsureRunning(ctx, cfg, 20*time.Second)
			if err != nil {
				return err
			}
			id, err := cl.Send(ctx, chat.JID, text)
			if err != nil {
				return err
			}
			return emit(map[string]string{"id": id, "chat": chat.JID}, func() { printf("enviado (%s)\n", id) })
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "não pedir confirmação")
	root.AddCommand(cmd)
}
