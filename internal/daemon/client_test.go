package daemon

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/store"
)

func TestClientAgainstServer(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	srv := &Server{Store: s, WA: &fakeWA{}, Shutdown: func() {}}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := &Client{BaseURL: ts.URL, HTTP: ts.Client()}
	st, err := c.Status(context.Background())
	if err != nil || !st.OK {
		t.Fatal(err)
	}
	id, err := c.Send(context.Background(), "1@s.whatsapp.net", "x")
	if err != nil || id != "ID1" {
		t.Fatal(id, err)
	}
	if err := c.Sync(context.Background(), "1@s.whatsapp.net", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(context.Background(), "", ""); err == nil || !strings.Contains(err.Error(), "obrigat") {
		t.Fatalf("server error must surface: %v", err)
	}
}

func TestEnsureRunningReportsLogOnFailure(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Port: 1, LogPath: filepath.Join(home, "daemon.log"), PidPath: filepath.Join(home, "daemon.pid"), Home: home}
	os.WriteFile(cfg.LogPath, []byte("linha1\nnão pareado: rode serve\n"), 0o600)
	spawnFn = func(cfg *config.Config) error { return nil } // don't actually spawn in tests
	defer func() { spawnFn = Spawn }()
	_, err := EnsureRunning(context.Background(), cfg, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "não pareado") {
		t.Fatalf("expected log tail in error, got %v", err)
	}
}
