package cli

import (
	"context"
	"os"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
)

func init() {
	var background bool
	var idle time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Roda o daemon (foreground mostra QR na primeira vez)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("idle") {
				cfg.IdleTimeout = idle
			}
			var showQR func(string)
			if !background {
				showQR = func(code string) {
					printf("Escaneie no WhatsApp > Aparelhos conectados:\n")
					qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
				}
			}
			return daemon.Run(context.Background(), cfg, background, showQR)
		},
	}
	cmd.Flags().BoolVar(&background, "background", false, "modo detached: log em arquivo, idle timeout")
	cmd.Flags().DurationVar(&idle, "idle", 30*time.Minute, "encerra após este tempo sem comandos (0 = nunca; só com --background)")
	root.AddCommand(cmd)
}
