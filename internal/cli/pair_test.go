package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/andrewmautone/claudewhats/internal/daemon"
)

// fakeDaemon serves /status from a sequence of responses (the last one repeats)
// and points daemonClientOverride at it for the test's duration.
func fakeDaemon(t *testing.T, seq ...daemon.StatusResponse) *int32 {
	t.Helper()
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1)) - 1
		if n >= len(seq) {
			n = len(seq) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(seq[n])
	}))
	daemonClientOverride = &daemon.Client{BaseURL: ts.URL, HTTP: ts.Client()}
	t.Cleanup(func() { ts.Close(); daemonClientOverride = nil })
	return &calls
}

func TestPairShowsQRPath(t *testing.T) {
	s := testStore(t)
	fakeDaemon(t, daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png", QRTxt: "/h/qr.txt"})
	out := run(t, s, "pair")
	if !strings.Contains(out, "QR em: /h/qr.png") || !strings.Contains(out, "Aparelhos conectados") || !strings.Contains(out, "pair --wait") {
		t.Fatal(out)
	}
	out = run(t, s, "pair", "--json")
	var res struct {
		Paired      bool   `json:"paired"`
		Pairing     bool   `json:"pairing"`
		QRPNG       string `json:"qr_png"`
		QRTxt       string `json:"qr_txt"`
		QRUpdatedAt int64  `json:"qr_updated_at"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Paired || !res.Pairing || res.QRPNG != "/h/qr.png" || res.QRTxt != "/h/qr.txt" || res.QRUpdatedAt != 1700000000 {
		t.Fatalf("%v %s", err, out)
	}
}

func TestPairAlreadyPaired(t *testing.T) {
	s := testStore(t)
	fakeDaemon(t, daemon.StatusResponse{OK: true, Paired: true, Connected: true})
	out := run(t, s, "pair")
	if !strings.Contains(out, "já pareado") {
		t.Fatal(out)
	}
	out = run(t, s, "pair", "--json")
	if !strings.Contains(out, `"paired": true`) {
		t.Fatal(out)
	}
}

func TestPairWaitUntilPaired(t *testing.T) {
	s := testStore(t)
	pairing := daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png", QRTxt: "/h/qr.txt"}
	calls := fakeDaemon(t, pairing, daemon.StatusResponse{OK: true, Paired: true})
	out := run(t, s, "pair", "--wait")
	if !strings.Contains(out, "QR em: /h/qr.png") || !strings.HasSuffix(strings.TrimSpace(out), "pareado") || atomic.LoadInt32(calls) < 2 {
		t.Fatalf("%d %s", *calls, out)
	}
	fakeDaemon(t, pairing, daemon.StatusResponse{OK: true, Paired: true})
	out = run(t, s, "pair", "--wait", "--json")
	if !strings.Contains(out, `"paired": true`) {
		t.Fatal(out)
	}
}

func TestPairWaitTimeout(t *testing.T) {
	s := testStore(t)
	fakeDaemon(t, daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png"})
	_, err := runWith(s, "pair", "--wait", "--timeout", "600ms")
	if err == nil || !strings.Contains(err.Error(), "tempo esgotado") {
		t.Fatalf("%v", err)
	}
}

func TestStatusShowsPairing(t *testing.T) {
	s := testStore(t)
	fakeDaemon(t, daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png", PID: 7})
	out := run(t, s, "status")
	if !strings.Contains(out, "pareado: não") || !strings.Contains(out, "pareando (QR em /h/qr.png)") {
		t.Fatal(out)
	}
	out = run(t, s, "status", "--json")
	var res map[string]any
	json.Unmarshal([]byte(out), &res)
	if res["paired"] != false || res["pairing"] != true || res["qr_png"] != "/h/qr.png" {
		t.Fatal(out)
	}
}
