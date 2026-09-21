package cli

import (
	"context"
	"errors"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pareia com o WhatsApp via arquivo QR (sem terminal do daemon)",
		Long: `Sobe o daemon em background se preciso e mostra onde ele gravou o QR
(~/.claudewhats/qr.png e qr.txt). O arquivo é regravado a cada QR novo.
Com --wait, bloqueia até o WhatsApp confirmar o pareamento ou --timeout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := context.Background()
			c, err := ensureDaemon(ctx, cfg, 20*time.Second)
			if err != nil {
				return err
			}
			st, ok, err := pollStatus(ctx, c, 20*time.Second, func(st daemon.StatusResponse) bool {
				return st.Paired || (st.Pairing && st.QRUpdatedAt > 0)
			})
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("daemon não gerou o QR a tempo; veja " + cfg.LogPath)
			}
			if st.Paired {
				return emit(map[string]any{"paired": true}, func() { printf("já pareado\n") })
			}
			showQR := func() {
				printf("QR em: %s\n", st.QRPNG)
				printf("Escaneie no WhatsApp > Aparelhos conectados > Conectar aparelho.\n")
			}
			if !wait {
				return emit(pairJSON(st), func() {
					showQR()
					printf("Depois rode: claudewhats pair --wait\n")
				})
			}
			if !jsonOut {
				showQR()
			}
			_, ok, err = pollStatus(ctx, c, timeout, func(st daemon.StatusResponse) bool { return st.Paired })
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("tempo esgotado; rode pair de novo")
			}
			return emit(map[string]any{"paired": true}, func() { printf("pareado\n") })
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "espera o WhatsApp confirmar o pareamento")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "tempo máximo do --wait")
	root.AddCommand(cmd)
}

func pairJSON(st daemon.StatusResponse) map[string]any {
	return map[string]any{"paired": st.Paired, "pairing": st.Pairing, "qr_png": st.QRPNG, "qr_txt": st.QRTxt, "qr_updated_at": st.QRUpdatedAt}
}

// pollStatus queries /status every 500ms until done(st) or max elapses. ok is
// false on timeout; err is a transport/daemon error.
func pollStatus(ctx context.Context, c *daemon.Client, max time.Duration, done func(daemon.StatusResponse) bool) (daemon.StatusResponse, bool, error) {
	deadline := time.Now().Add(max)
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		st, err := c.Status(pctx)
		cancel()
		if err != nil {
			return st, false, err
		}
		if done(st) {
			return st, true, nil
		}
		if !time.Now().Before(deadline) {
			return st, false, nil
		}
		select {
		case <-ctx.Done():
			return st, false, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
