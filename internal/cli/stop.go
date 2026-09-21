package cli

import (
	"context"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Encerra o daemon em background",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := daemon.NewClient(cfg.Port).Shutdown(ctx); err != nil {
				return emit(map[string]bool{"stopped": false}, func() { printf("daemon já estava parado\n") })
			}
			return emit(map[string]bool{"stopped": true}, func() { printf("daemon encerrado\n") })
		},
	})
}
