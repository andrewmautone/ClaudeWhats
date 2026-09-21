package cli

import (
	"context"
	"time"

	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Estado do banco e do daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			msgs, jobs, err := s.Stats()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			st, derr := daemon.NewClient(cfg.Port).Status(ctx)
			res := map[string]any{"messages": msgs, "pending_jobs": jobs, "daemon_running": derr == nil, "connected": st.Connected, "pid": st.PID, "home": cfg.Home}
			return emit(res, func() {
				printf("mensagens: %d  jobs pendentes: %d\n", msgs, jobs)
				if derr != nil {
					printf("daemon: parado\n")
				} else {
					printf("daemon: rodando (pid %d), whatsapp conectado: %v\n", st.PID, st.Connected)
				}
			})
		},
	})
}
