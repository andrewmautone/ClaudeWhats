package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewmautone/claudewhats/internal/store"
	"go.mau.fi/whatsmeow/types"
)

type fakeWA struct {
	sentTo   string
	sentText string
	synced   string
	count    int
}

func (f *fakeWA) SendText(ctx context.Context, to types.JID, text string) (string, error) {
	f.sentTo, f.sentText = to.String(), text
	return "ID1", nil
}
func (f *fakeWA) RequestHistory(ctx context.Context, chat types.JID, oldest *types.MessageInfo, n int) error {
	f.synced, f.count = chat.String(), n
	return nil
}
func (f *fakeWA) IsConnected() bool { return true }

func TestServerEndpoints(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	s.UpsertChat("1@s.whatsapp.net", "dm", "")
	s.InsertMessage(store.Message{ChatJID: "1@s.whatsapp.net", ID: "A", SenderJID: "1@s.whatsapp.net", TS: 5, Type: "text"})
	wa := &fakeWA{}
	stopped := false
	srv := &Server{Store: s, WA: wa, Shutdown: func() { stopped = true }}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var st StatusResponse
	r, _ := http.Get(ts.URL + "/status")
	json.NewDecoder(r.Body).Decode(&st)
	if !st.OK || !st.Connected || st.Messages != 1 {
		t.Fatalf("%+v", st)
	}

	r, _ = http.Post(ts.URL+"/send", "application/json", strings.NewReader(`{"chat":"1@s.whatsapp.net","text":"oi"}`))
	var sr map[string]string
	json.NewDecoder(r.Body).Decode(&sr)
	if r.StatusCode != 200 || sr["id"] != "ID1" || wa.sentText != "oi" {
		t.Fatalf("%d %v %+v", r.StatusCode, sr, wa)
	}

	r, _ = http.Post(ts.URL+"/sync", "application/json", strings.NewReader(`{"chat":"1@s.whatsapp.net","count":25}`))
	if r.StatusCode != 200 || wa.synced != "1@s.whatsapp.net" || wa.count != 25 {
		t.Fatalf("%d %+v", r.StatusCode, wa)
	}

	r, _ = http.Post(ts.URL+"/send", "application/json", strings.NewReader(`{"chat":"","text":""}`))
	if r.StatusCode != 400 {
		t.Fatal("validation")
	}

	http.Post(ts.URL+"/shutdown", "application/json", nil)
	if !stopped {
		t.Fatal("shutdown not called")
	}
}

func TestIdleLoop(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	done := make(chan struct{})
	srv := &Server{Store: s, WA: &fakeWA{}, Idle: 50 * time.Millisecond, Shutdown: func() { close(done) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.IdleLoop(ctx)
	time.Sleep(30 * time.Millisecond)
	srv.Touch()
	select {
	case <-done:
		t.Fatal("touch should have postponed shutdown")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("idle shutdown never fired")
	}
}
