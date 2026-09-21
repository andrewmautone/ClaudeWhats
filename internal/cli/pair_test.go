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

// stubOpenBrowser replaces openBrowserFn for the test's duration and records
// every path it was asked to open.
func stubOpenBrowser(t *testing.T) *[]string {
	t.Helper()
	var opened []string
	orig := openBrowserFn
	openBrowserFn = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	t.Cleanup(func() { openBrowserFn = orig })
	return &opened
}

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
	fakeDaemon(t, daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png", QRTxt: "/h/qr.txt", QRHTML: "/h/qr.html"})
	opened := stubOpenBrowser(t)
	out := run(t, s, "pair")
	if !strings.Contains(out, "QR aberto no navegador: /h/qr.html") || !strings.Contains(out, "Aparelhos conectados") || !strings.Contains(out, "pair --wait") {
		t.Fatal(out)
	}
	if len(*opened) != 1 || (*opened)[0] != "/h/qr.html" {
		t.Fatalf("openBrowserFn calls: %v", *opened)
	}

	fakeDaemon(t, daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png", QRTxt: "/h/qr.txt", QRHTML: "/h/qr.html"})
	opened = stubOpenBrowser(t)
	out = run(t, s, "pair", "--json")
	var res struct {
		Paired      bool   `json:"paired"`
		Pairing     bool   `json:"pairing"`
		QRPNG       string `json:"qr_png"`
		QRTxt       string `json:"qr_txt"`
		QRHTML      string `json:"qr_html"`
		QRUpdatedAt int64  `json:"qr_updated_at"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Paired || !res.Pairing || res.QRPNG != "/h/qr.png" || res.QRTxt != "/h/qr.txt" || res.QRHTML != "/h/qr.html" || res.QRUpdatedAt != 1700000000 {
		t.Fatalf("%v %s", err, out)
	}
	if len(*opened) != 1 || (*opened)[0] != "/h/qr.html" {
		t.Fatalf("openBrowserFn calls: %v", *opened)
	}

	fakeDaemon(t, daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png", QRTxt: "/h/qr.txt", QRHTML: "/h/qr.html"})
	opened = stubOpenBrowser(t)
	out = run(t, s, "pair", "--open=false")
	if !strings.Contains(out, "QR em: /h/qr.html") {
		t.Fatal(out)
	}
	if len(*opened) != 0 {
		t.Fatalf("openBrowserFn should not be called with --open=false: %v", *opened)
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
	pairing := daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr-1700000000.png", QRTxt: "/h/qr.txt", QRHTML: "/h/qr.html"}
	refreshed := daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000020, QRPNG: "/h/qr-1700000020.png", QRTxt: "/h/qr.txt", QRHTML: "/h/qr.html"}
	paired := daemon.StatusResponse{OK: true, Paired: true}
	stubOpenBrowser(t)
	calls := fakeDaemon(t, pairing, refreshed, refreshed, paired)
	out := run(t, s, "pair", "--wait")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(out, "QR aberto no navegador: /h/qr.html") || lines[len(lines)-1] != "pareado" || atomic.LoadInt32(calls) < 4 {
		t.Fatalf("%d %s", *calls, out)
	}
	// one "QR novo" per change of qr_updated_at, not per poll
	if strings.Count(out, "QR novo: /h/qr-1700000020.png") != 1 {
		t.Fatal(out)
	}

	fakeDaemon(t, pairing, refreshed, refreshed, paired)
	stubOpenBrowser(t)
	out = run(t, s, "pair", "--wait", "--json")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 2 qr events + final, got %d: %s", len(lines), out)
	}
	for i, want := range []string{"/h/qr-1700000000.png", "/h/qr-1700000020.png"} {
		var ev struct {
			Event string `json:"event"`
			QRPNG string `json:"qr_png"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil || ev.Event != "qr" || ev.QRPNG != want {
			t.Fatalf("line %d: %v %s", i, err, lines[i])
		}
	}
	if lines[2] != `{"paired":true}` {
		t.Fatal(lines[2])
	}
}

func TestPairWaitTimeout(t *testing.T) {
	s := testStore(t)
	fakeDaemon(t, daemon.StatusResponse{OK: true, Pairing: true, QRUpdatedAt: 1700000000, QRPNG: "/h/qr.png", QRHTML: "/h/qr.html"})
	stubOpenBrowser(t)
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
