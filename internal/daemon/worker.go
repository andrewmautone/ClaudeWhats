package daemon

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/andrewmautone/claudewhats/internal/store"
)

type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
	Describe(ctx context.Context, image []byte, mime, caption string) (string, error)
}

type Worker struct {
	Store       *store.Store
	AI          Transcriber
	Log         *log.Logger
	MaxAttempts int
	Poll        time.Duration
}

func (w *Worker) logf(format string, a ...any) {
	if w.Log != nil {
		w.Log.Printf(format, a...)
	}
}

// RunOnce processes a single pending job, if any is due.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	max := w.MaxAttempts
	if max <= 0 {
		max = 5
	}
	now := time.Now().Unix()
	job, ok, err := w.Store.NextJob(now)
	if err != nil || !ok {
		return false, err
	}
	m, found, err := w.Store.GetMessage(job.ChatJID, job.MsgID)
	if err != nil {
		return true, err
	}
	if !found || m.MediaPath == "" {
		w.Store.SetTranscript(job.ChatJID, job.MsgID, "", "failed")
		return true, w.Store.CompleteJob(job.ID)
	}
	data, err := os.ReadFile(m.MediaPath)
	if err != nil {
		w.logf("job %d: media missing: %v", job.ID, err)
		w.Store.SetTranscript(job.ChatJID, job.MsgID, "", "failed")
		return true, w.Store.CompleteJob(job.ID)
	}
	var out string
	switch m.Type {
	case "audio":
		out, err = w.AI.Transcribe(ctx, data, m.MediaMime)
	case "image":
		out, err = w.AI.Describe(ctx, data, m.MediaMime, m.Text)
	default:
		w.Store.SetTranscript(job.ChatJID, job.MsgID, "", "skipped")
		return true, w.Store.CompleteJob(job.ID)
	}
	if err != nil {
		w.logf("job %d (%s) attempt %d: %v", job.ID, m.Type, job.Attempts+1, err)
		_, ferr := w.Store.FailJob(job.ID, now, err.Error(), max)
		return true, ferr
	}
	if err := w.Store.SetTranscript(job.ChatJID, job.MsgID, out, "done"); err != nil {
		return true, err
	}
	return true, w.Store.CompleteJob(job.ID)
}

// Run polls for jobs until ctx is canceled.
func (w *Worker) Run(ctx context.Context) {
	poll := w.Poll
	if poll <= 0 {
		poll = 3 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return
		}
		worked, err := w.RunOnce(ctx)
		if err != nil {
			w.logf("worker: %v", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}
	}
}
