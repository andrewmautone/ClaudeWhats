package store

import "testing"

func TestJobLifecycle(t *testing.T) {
	s := mustMem(t)
	seed(t, s)
	if err := s.EnqueueJob("123@g.us", "g1"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueJob("123@g.us", "g1"); err != nil {
		t.Fatal("enqueue must be idempotent")
	}
	j, ok, _ := s.NextJob(1000)
	if !ok || j.MsgID != "g1" {
		t.Fatalf("%+v", j)
	}
	gave, _ := s.FailJob(j.ID, 1000, "boom", 5)
	if gave {
		t.Fatal("should retry")
	}
	if _, ok, _ := s.NextJob(1000); ok {
		t.Fatal("backoff should hide job")
	}
	j, ok, _ = s.NextJob(1000 + 31)
	if !ok || j.Attempts != 1 {
		t.Fatalf("after backoff: %+v %v", j, ok)
	}
	gave, _ = s.FailJob(j.ID, 2000, "boom", 2)
	if !gave {
		t.Fatal("should give up at maxAttempts")
	}
	ms, _ := s.ReadMessages("123@g.us", 0, 0, 10)
	if ms[0].TranscriptStatus != "failed" {
		t.Fatal(ms[0].TranscriptStatus)
	}
	_, ok, _ = s.NextJob(1 << 40)
	if ok {
		t.Fatal("job should be gone")
	}
	s.EnqueueJob("5511888@s.whatsapp.net", "m2")
	j, _, _ = s.NextJob(1 << 40)
	if err := s.CompleteJob(j.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NextJob(1 << 40); ok {
		t.Fatal("completed job should be gone")
	}
}
