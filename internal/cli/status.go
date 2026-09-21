package cli

import (
	"context"
	"time"

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
			st, derr := daemonClient(cfg).Status(ctx)
			res := map[string]any{"messages": msgs, "pending_jobs": jobs, "daemon_running": derr == nil, "connected": st.Connected, "paired": st.Paired, "pairing": st.Pairing, "qr_png": st.QRPNG, "pid": st.PID, "home": cfg.Home}
			return emit(res, func() {
				printf("mensagens: %d  jobs pendentes: %d\n", msgs, jobs)
				if derr != nil {
					printf("daemon: parado\n")
					return
				}
				printf("daemon: rodando (pid %d), whatsapp conectado: %v\n", st.PID, st.Connected)
				printf("pareado: %s\n", simNao(st.Paired))
				if st.Pairing {
					printf("pareando (QR em %s)\n", st.QRPNG)
				}
			})
		},
	})
}

func simNao(b bool) string {
	if b {
		return "sim"
	}
	return "não"
}
