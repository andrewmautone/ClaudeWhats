package daemon

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewmautone/claudewhats/internal/store"
)

func TestStatusWithoutWA(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	srv := &Server{Store: s, Shutdown: func() {}}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var st StatusResponse
	r, _ := http.Get(ts.URL + "/status")
	json.NewDecoder(r.Body).Decode(&st)
	if !st.OK || st.Connected || st.Paired || st.Pairing || st.QRUpdatedAt != 0 {
		t.Fatalf("%+v", st)
	}

	r, _ = http.Post(ts.URL+"/send", "application/json", strings.NewReader(`{"chat":"1@s.whatsapp.net","text":"oi"}`))
	var e map[string]string
	json.NewDecoder(r.Body).Decode(&e)
	if r.StatusCode != 503 || e["error"] != "whatsapp não conectado" {
		t.Fatalf("%d %v", r.StatusCode, e)
	}
	r, _ = http.Post(ts.URL+"/sync", "application/json", strings.NewReader(`{"chat":"1@s.whatsapp.net"}`))
	if r.StatusCode != 503 {
		t.Fatalf("sync %d", r.StatusCode)
	}
}

func TestStatusWhilePairing(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	pair := &pairState{pngPath: "/x/qr.png", txtPath: "/x/qr.txt"}
	pair.setPairing(true)
	pair.touch(time.Unix(1700000000, 0), "/x/qr-1700000000.png")
	srv := &Server{Store: s, Pair: pair, Shutdown: func() {}}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var st StatusResponse
	r, _ := http.Get(ts.URL + "/status")
	json.NewDecoder(r.Body).Decode(&st)
	if !st.Pairing || st.Paired || st.QRUpdatedAt != 1700000000 || st.QRPNG != "/x/qr-1700000000.png" || st.QRTxt != "/x/qr.txt" {
		t.Fatalf("%+v", st)
	}
	pair.setPaired()
	r, _ = http.Get(ts.URL + "/status")
	json.NewDecoder(r.Body).Decode(&st)
	if !st.Paired || st.Pairing {
		t.Fatalf("%+v", st)
	}
}

func TestQRSinkWritesFilesAtomically(t *testing.T) {
	dir := t.TempDir()
	pair := &pairState{pngPath: filepath.Join(dir, "qr.png"), txtPath: filepath.Join(dir, "qr.txt"), htmlPath: filepath.Join(dir, "qr.html")}
	shown := ""
	sink := &qrSink{pair: pair, show: func(code string) { shown = code }}

	before := time.Now().Add(-time.Second)
	sink.onQR("2@abc,def,ghi")
	if shown != "2@abc,def,ghi" {
		t.Fatalf("show not called: %q", shown)
	}
	b, err := os.ReadFile(pair.pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Fatalf("png: %v", err)
	}
	txt, err := os.ReadFile(pair.txtPath)
	if err != nil || len(txt) == 0 {
		t.Fatalf("txt: %v %d", err, len(txt))
	}
	// a stale timestamped file from an earlier code must be pruned
	old := filepath.Join(dir, "qr-1.png")
	os.WriteFile(old, []byte("x"), 0o600)
	sink.onQR("2@second")
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old qr-*.png not removed")
	}
	pairing, updated := pair.snapshot()
	if !pairing || updated.Before(before) {
		t.Fatalf("state %v %v", pairing, updated)
	}
	unique := pair.png()
	if unique == pair.pngPath || filepath.Dir(unique) != dir || !strings.HasPrefix(filepath.Base(unique), "qr-") {
		t.Fatalf("unique png %q", unique)
	}
	ub, err := os.ReadFile(unique)
	if err != nil {
		t.Fatal(err)
	}
	if sb, _ := os.ReadFile(pair.pngPath); !bytes.Equal(ub, sb) {
		t.Fatal("unique png differs from qr.png")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 {
		t.Fatalf("expected qr.png, qr.txt, qr.html and one qr-*.png: %v", entries)
	}
	if b, err := os.ReadFile(pair.htmlPath); err != nil || !bytes.Equal(b, qrHTML) {
		t.Fatalf("qr.html not written: %v", err)
	}

	sink.clear()
	if _, err := os.Stat(pair.pngPath); !os.IsNotExist(err) {
		t.Fatal("png not removed")
	}
	if _, err := os.Stat(pair.txtPath); !os.IsNotExist(err) {
		t.Fatal("txt not removed")
	}
	if _, err := os.Stat(pair.htmlPath); !os.IsNotExist(err) {
		t.Fatal("html not removed")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("clear left files: %v", entries)
	}
	pairing, updated = pair.snapshot()
	if pairing || !updated.IsZero() || pair.png() != pair.pngPath {
		t.Fatalf("state after clear %v %v %s", pairing, updated, pair.png())
	}
}
