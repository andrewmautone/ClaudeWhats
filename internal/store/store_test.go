package store

import "testing"

func TestOpenCreatesSchema(t *testing.T) {
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('contacts','identities','chats','group_members','messages','gemini_jobs')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("expected 6 tables, got %d", n)
	}
	if _, err := s.DB().Exec(`INSERT INTO messages_fts(rowid, text, transcript) VALUES (1,'oi','')`); err != nil {
		t.Fatalf("fts5 missing: %v", err)
	}
}
