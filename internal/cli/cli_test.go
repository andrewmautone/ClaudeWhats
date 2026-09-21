package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/andrewmautone/claudewhats/internal/memory"
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
	t.Setenv("CLAUDEWHATS_MEMORY_DIR", t.TempDir())
	s := testStore(t)
	out := run(t, s, "read", "5511888", "--json", "--no-spawn", "--limit", "1")
	var payload struct {
		Chat     store.Chat      `json:"chat"`
		Memory   []string        `json:"memory"`
		Messages []store.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload.Messages) != 1 || payload.Messages[0].ID != "m2" {
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

func TestVersion(t *testing.T) {
	s := testStore(t)
	out := run(t, s, "version")
	if !strings.Contains(out, "claudewhats dev") {
		t.Fatal(out)
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

func TestMemoryCommandsAndBlock(t *testing.T) {
	t.Setenv("CLAUDEWHATS_MEMORY_DIR", t.TempDir())
	s := testStore(t)

	out := run(t, s, "memory", "note", "Maria prefere entrega às 18h")
	if !strings.Contains(out, "Saved as #0.") {
		t.Fatal(out)
	}

	out = run(t, s, "read", "maria", "--no-spawn")
	if !strings.Contains(out, "## memória") || !strings.Contains(out, "Maria prefere") {
		t.Fatal(out)
	}

	out = run(t, s, "read", "maria", "--no-spawn", "--no-memory")
	if strings.Contains(out, "## memória") {
		t.Fatal(out)
	}

	out = run(t, s, "read", "maria", "--no-spawn", "--json")
	var payload struct {
		Chat     store.Chat      `json:"chat"`
		Memory   []string        `json:"memory"`
		Messages []store.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload.Memory) != 1 || len(payload.Messages) != 2 {
		t.Fatalf("%v %s", err, out)
	}

	out = run(t, s, "memory", "recall", "prefere", "--json")
	var recalled struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(out), &recalled); err != nil || len(recalled.Lines) != 1 {
		t.Fatalf("%v %s", err, out)
	}

	out = run(t, s, "memory", "wake")
	if !strings.Contains(out, "You are awake.") {
		t.Fatal(out)
	}

	t.Setenv("CLAUDEWHATS_MEMORY_DIR", t.TempDir()) // fresh, empty tree
	out, err := runWith(s, "memory", "zoom", "0-1")
	if err == nil {
		t.Fatalf("expected zoom error, got success: %s", out)
	}
	if !strings.Contains(out, "is beyond the memory") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestChatsJSONNumber(t *testing.T) {
	s := testStore(t)
	s.ResolveIdentity("777@lid", "", "")
	s.UpsertChat("777@lid", "dm", "")
	s.InsertMessage(store.Message{ChatJID: "777@lid", ID: "l1", SenderJID: "777@lid", TS: 1700000300, Type: "text", Text: "?"})
	out := run(t, s, "chats", "--json", "--no-spawn")
	var chats []store.Chat
	if err := json.Unmarshal([]byte(out), &chats); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	nums := map[string]string{}
	for _, c := range chats {
		nums[c.JID] = c.Number
	}
	if nums["5511888@s.whatsapp.net"] != "5511888" || nums["777@lid"] != "" {
		t.Fatalf("%v", nums)
	}
	if !strings.Contains(out, `"number"`) || strings.Contains(out, `"name": "777"`) {
		t.Fatal(out)
	}
	if !strings.Contains(out, "desconhecido") {
		t.Fatal(out)
	}
}

func TestMemoryBlockSkipsLID(t *testing.T) {
	m, _, err := memory.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.Note("777 gosta de bolo")
	m.Note("5511777 é o Zé")
	// LID digits never match memory; a bare LID chat has nothing to search by
	if lines := memoryBlock(m, store.Chat{JID: "777@lid", Name: "desconhecido"}); lines != nil {
		t.Fatal(lines)
	}
	// the number behind the LID does
	lines := memoryBlock(m, store.Chat{JID: "777@lid", Number: "5511777"})
	if len(lines) != 1 || !strings.Contains(lines[0], "Zé") {
		t.Fatal(lines)
	}
}
