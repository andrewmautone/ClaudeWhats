package cli

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	var wait bool
	var openFlag bool
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
			// Reading the terminal QR (or the raw PNG) rarely scans reliably; the
			// HTML page reloads qr.png every 2s, so opening it once is enough for
			// the whole pairing session.
			opened := false
			if openFlag {
				if err := openBrowserFn(st.QRHTML); err != nil {
					warnf("abrir navegador: %v", err)
				} else {
					opened = true
				}
			}
			showQR := func() {
				if opened {
					printf("QR aberto no navegador: %s\n", st.QRHTML)
				} else {
					printf("QR em: %s\n", st.QRHTML)
				}
				printf("Escaneie no WhatsApp > Aparelhos conectados > Conectar aparelho.\n")
			}
			if !wait {
				return emit(pairJSON(st), func() {
					showQR()
					printf("Depois rode: claudewhats pair --wait\n")
				})
			}
			// --wait: report every fresh QR (the previous one expired) so the caller
			// can show the new file, then the final verdict. JSON mode is one object
			// per line so a reader can act on each event as it arrives.
			announce := func(st daemon.StatusResponse, first bool) {
				if jsonOut {
					printJSONLine(map[string]any{"event": "qr", "qr_png": st.QRPNG, "qr_txt": st.QRTxt, "qr_html": st.QRHTML, "qr_updated_at": st.QRUpdatedAt})
				} else if first {
					showQR()
				} else {
					printf("QR novo: %s\n", st.QRPNG)
				}
			}
			announce(st, true)
			last := st.QRUpdatedAt
			_, ok, err = pollStatus(ctx, c, timeout, func(st daemon.StatusResponse) bool {
				if st.Paired {
					return true
				}
				if st.QRUpdatedAt != last {
					last = st.QRUpdatedAt
					announce(st, false)
				}
				return false
			})
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("tempo esgotado; rode pair de novo")
			}
			if jsonOut {
				printJSONLine(map[string]any{"paired": true})
				return nil
			}
			printf("pareado\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "espera o WhatsApp confirmar o pareamento")
	cmd.Flags().BoolVar(&openFlag, "open", true, "abre a página do QR no navegador")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "tempo máximo do --wait")
	root.AddCommand(cmd)
}

func printJSONLine(v any) {
	b, _ := json.Marshal(v)
	printf("%s\n", b)
}

func pairJSON(st daemon.StatusResponse) map[string]any {
	return map[string]any{"paired": st.Paired, "pairing": st.Pairing, "qr_png": st.QRPNG, "qr_txt": st.QRTxt, "qr_html": st.QRHTML, "qr_updated_at": st.QRUpdatedAt}
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
