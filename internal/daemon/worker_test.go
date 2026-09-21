package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrewmautone/claudewhats/internal/gemini"
	"github.com/andrewmautone/claudewhats/internal/store"
)

type fakeAI struct {
	out   string
	err   error
	calls int
}

func (f *fakeAI) Transcribe(ctx context.Context, a []byte, mime string) (string, error) {
	f.calls++
	return f.out, f.err
}
func (f *fakeAI) Describe(ctx context.Context, a []byte, mime, cap string) (string, error) {
	f.calls++
	return "img:" + f.out, f.err
}

func seedJob(t *testing.T, s *store.Store, dir, typ string) {
	p := filepath.Join(dir, "x.bin")
	os.WriteFile(p, []byte("data"), 0o600)
	s.UpsertChat("1@s.whatsapp.net", "dm", "")
	s.InsertMessage(store.Message{ChatJID: "1@s.whatsapp.net", ID: "M", SenderJID: "1@s.whatsapp.net", TS: 1, Type: typ, MediaPath: p, MediaMime: "audio/ogg", TranscriptStatus: "pending"})
	s.EnqueueJob("1@s.whatsapp.net", "M")
}

func TestWorkerTranscribes(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	seedJob(t, s, t.TempDir(), "audio")
	ai := &fakeAI{out: "texto"}
	w := &Worker{Store: s, AI: ai}
	worked, err := w.RunOnce(context.Background())
	if err != nil || !worked {
		t.Fatal(worked, err)
	}
	ms, _ := s.ReadMessages("1@s.whatsapp.net", 0, 1)
	if ms[0].Transcript != "texto" || ms[0].TranscriptStatus != "done" {
		t.Fatalf("%+v", ms[0])
	}
	if worked, _ := w.RunOnce(context.Background()); worked {
		t.Fatal("queue should be empty")
	}
}

func TestWorkerImageUsesDescribeAndFailsAfterMax(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	seedJob(t, s, t.TempDir(), "image")
	ai := &fakeAI{err: errors.New("quota")}
	w := &Worker{Store: s, AI: ai, MaxAttempts: 1}
	w.RunOnce(context.Background())
	ms, _ := s.ReadMessages("1@s.whatsapp.net", 0, 1)
	if ms[0].TranscriptStatus != "failed" {
		t.Fatalf("%+v", ms[0])
	}
	ai.err = nil
	ai.out = "gato"
	seedJob(t, s, t.TempDir(), "image")
	// message M already exists (dedup) so re-mark pending manually
	s.SetTranscript("1@s.whatsapp.net", "M", "", "pending")
	w.RunOnce(context.Background())
	ms, _ = s.ReadMessages("1@s.whatsapp.net", 0, 1)
	if ms[0].Transcript != "img:gato" {
		t.Fatalf("%+v", ms[0])
	}
}

func TestWorkerMissingFileGivesUp(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	s.UpsertChat("1@s.whatsapp.net", "dm", "")
	s.InsertMessage(store.Message{ChatJID: "1@s.whatsapp.net", ID: "Z", SenderJID: "1@s.whatsapp.net", TS: 1, Type: "audio", MediaPath: "/nope", TranscriptStatus: "pending"})
	s.EnqueueJob("1@s.whatsapp.net", "Z")
	w := &Worker{Store: s, AI: &fakeAI{}}
	w.RunOnce(context.Background())
	ms, _ := s.ReadMessages("1@s.whatsapp.net", 0, 1)
	if ms[0].TranscriptStatus != "failed" {
		t.Fatal("missing file should fail immediately")
	}
	_ = time.Second
}

func TestWorkerMissingKeyLeavesJobPending(t *testing.T) {
	s, _ := store.OpenMemory()
	defer s.Close()
	seedJob(t, s, t.TempDir(), "audio")
	w := &Worker{Store: s, AI: &fakeAI{err: gemini.ErrNoKey}, MaxAttempts: 1}
	worked, err := w.RunOnce(context.Background())
	if worked || !errors.Is(err, gemini.ErrNoKey) {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	ms, _ := s.ReadMessages("1@s.whatsapp.net", 0, 1)
	if ms[0].TranscriptStatus != "pending" {
		t.Fatalf("missing key must not fail the message: %+v", ms[0])
	}
	j, ok, _ := s.NextJob(time.Now().Unix())
	if !ok || j.Attempts != 0 {
		t.Fatalf("job must stay pending with no attempt burned: %+v %v", j, ok)
	}
	// key configured later: the same job now succeeds
	w.AI = &fakeAI{out: "agora sim"}
	if worked, err := w.RunOnce(context.Background()); !worked || err != nil {
		t.Fatal(worked, err)
	}
	ms, _ = s.ReadMessages("1@s.whatsapp.net", 0, 1)
	if ms[0].Transcript != "agora sim" || ms[0].TranscriptStatus != "done" {
		t.Fatalf("%+v", ms[0])
	}
}
