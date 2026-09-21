package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/andrewmautone/claudewhats/internal/store"
)

func testStore(t *testing.T) *store.Store {
	s, _ := store.OpenMemory()
	t.Cleanup(func() { s.Close() })
	s.AddContact("Maria", "5511888")
	s.UpsertChat("5511888@s.whatsapp.net", "dm", "")
	s.UpsertChat("123@g.us", "group", "Família")
	s.InsertMessage(store.Message{ChatJID: "5511888@s.whatsapp.net", ID: "m1", SenderJID: "5511888@s.whatsapp.net", TS: 1700000000, Type: "text", Text: "bom dia"})
	s.InsertMessage(store.Message{ChatJID: "5511888@s.whatsapp.net", ID: "m2", SenderJID: "me", TS: 1700000100, FromMe: true, Type: "audio", Transcript: "vou levar o bolo", TranscriptStatus: "done"})
	s.InsertMessage(store.Message{ChatJID: "123@g.us", ID: "g1", SenderJID: "5511888@s.whatsapp.net", TS: 1700000200, Type: "image", Text: "olha", TranscriptStatus: "pending"})
	return s
}

func run(t *testing.T, s *store.Store, args ...string) string {
	t.Helper()
	out, err := runWith(s, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return out
}

func TestChatsAndReadText(t *testing.T) {
	s := testStore(t)
	out := run(t, s, "chats", "--no-spawn")
	if !strings.Contains(out, "Família") || !strings.Contains(out, "Maria") {
		t.Fatal(out)
	}
	out = run(t, s, "read", "maria", "--no-spawn")
	if !strings.Contains(out, "Maria (text): bom dia") || !strings.Contains(out, "eu (audio): vou levar o bolo") {
		t.Fatal(out)
	}
	out = run(t, s, "read", "123@g.us", "--no-spawn")
	if !strings.Contains(out, "(image): [transcrição pendente]: olha") {
		t.Fatal(out)
	}
}

func TestReadJSONAndSearch(t *testing.T) {
	s := testStore(t)
	out := run(t, s, "read", "5511888", "--json", "--no-spawn", "--limit", "1")
	var msgs []store.Message
	if err := json.Unmarshal([]byte(out), &msgs); err != nil || len(msgs) != 1 || msgs[0].ID != "m2" {
		t.Fatalf("%v %s", err, out)
	}
	out = run(t, s, "search", "bolo", "--no-spawn")
	if !strings.Contains(out, "[Maria]") || !strings.Contains(out, "bolo") {
		t.Fatal(out)
	}
	// group hits are labelled with the group name, not the jid digits
	out = run(t, s, "search", "olha", "--no-spawn")
	if !strings.Contains(out, "[Família]") || strings.Contains(out, "[123]") {
		t.Fatal(out)
	}
}

func TestContactCommands(t *testing.T) {
	s := testStore(t)
	run(t, s, "contact", "add", "Zé", "+55 11 7777")
	s.ResolveIdentity("42@lid", "", "zezinho")
	run(t, s, "contact", "link", "Zé", "42@lid")
	out := run(t, s, "contact", "list", "--json")
	var cs []store.Contact
	json.Unmarshal([]byte(out), &cs)
	var ze *store.Contact
	for i := range cs {
		if cs[i].Name == "Zé" {
			ze = &cs[i]
		}
	}
	if ze == nil || len(ze.JIDs) != 2 {
		t.Fatalf("%s", out)
	}
	run(t, s, "contact", "rename", "42@lid", "José")
	if s.ContactName("42@lid") != "José" {
		t.Fatal("rename")
	}
}

func TestParseSince(t *testing.T) {
	for _, in := range []string{"24h", "7d", "30m", "2026-09-21"} {
		if _, err := parseSince(in); err != nil {
			t.Fatal(in, err)
		}
	}
	if v, _ := parseSince(""); v != 0 {
		t.Fatal("empty = 0")
	}
	if _, err := parseSince("abc"); err == nil {
		t.Fatal("invalid")
	}
	var _ bytes.Buffer
}

type fakeSum struct{ got []string }

func (f *fakeSum) Summarize(ctx context.Context, instr, text string) (string, error) {
	f.got = append(f.got, text)
	return "RESUMO(" + strconv.Itoa(strings.Count(text, "\n")+1) + " linhas)", nil
}

func TestSummaryPerChatAndOverall(t *testing.T) {
	s := testStore(t)
	f := &fakeSum{}
	summarizerOverride = f
	defer func() { summarizerOverride = nil }()
	out := run(t, s, "summary", "--no-spawn", "--since", "0")
	if !strings.Contains(out, "## Família") || !strings.Contains(out, "## Maria") || !strings.Contains(out, "## Geral") {
		t.Fatal(out)
	}
	if len(f.got) != 3 { // 2 chats + geral
		t.Fatalf("calls %d", len(f.got))
	}
	if !strings.Contains(f.got[0], "vou levar o bolo") && !strings.Contains(f.got[1], "vou levar o bolo") {
		t.Fatal("transcript must be in summary input")
	}
	out = run(t, s, "summary", "--chat", "maria", "--no-spawn", "--since", "0", "--json")
	if !strings.Contains(out, `"summary"`) {
		t.Fatal(out)
	}
}
