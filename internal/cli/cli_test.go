package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestReadUntilLimitOut(t *testing.T) {
	t.Setenv("CLAUDEWHATS_MEMORY_DIR", t.TempDir())
	s := testStore(t)
	day := time.Unix(1700000000, 0).Local().Format("2006-01-02")

	out := run(t, s, "read", "maria", "--since", day, "--until", day, "--no-spawn")
	if !strings.Contains(out, "bom dia") || !strings.Contains(out, "vou levar o bolo") {
		t.Fatalf("--since/--until should include both messages: %s", out)
	}

	// A day strictly before the seeded messages returns none with --until.
	before := time.Unix(1700000000, 0).Local().AddDate(0, 0, -1).Format("2006-01-02")
	out = run(t, s, "read", "maria", "--until", before, "--no-spawn")
	if strings.Contains(out, "bom dia") || strings.Contains(out, "vou levar o bolo") {
		t.Fatalf("--until before messages should return none: %s", out)
	}

	out = run(t, s, "read", "maria", "--limit", "0", "--no-spawn", "--json")
	var payload struct {
		Messages []store.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload.Messages) != 2 {
		t.Fatalf("--limit 0 should return all: %v %s", err, out)
	}

	outFile := filepath.Join(t.TempDir(), "sub", "x.txt")
	out = run(t, s, "read", "maria", "--out="+outFile, "--no-spawn")
	if !strings.Contains(out, "salvo: "+outFile) || !strings.Contains(out, "(2 mensagens)") {
		t.Fatalf("expected salvo message: %s", out)
	}
	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "bom dia") || !strings.Contains(string(content), "vou levar o bolo") || strings.Contains(string(content), "## memória") {
		t.Fatalf("unexpected file contents: %s", content)
	}

	outFileJSON := filepath.Join(t.TempDir(), "x.json")
	out = run(t, s, "read", "maria", "--out="+outFileJSON, "--json", "--no-spawn")
	var savedPayload struct {
		Saved string `json:"saved"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &savedPayload); err != nil || savedPayload.Saved != outFileJSON || savedPayload.Count != 2 {
		t.Fatalf("%v %s", err, out)
	}
	jsonContent, err := os.ReadFile(outFileJSON)
	if err != nil {
		t.Fatal(err)
	}
	var filePayload struct {
		Chat     store.Chat      `json:"chat"`
		Messages []store.Message `json:"messages"`
	}
	if err := json.Unmarshal(jsonContent, &filePayload); err != nil || len(filePayload.Messages) != 2 {
		t.Fatalf("%v %s", err, jsonContent)
	}
}

func TestReadOutAutoPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDEWHATS_HOME", home)
	t.Setenv("CLAUDEWHATS_MEMORY_DIR", t.TempDir())
	s := testStore(t)

	// --out with no value: saved under <home>/exports/, text by default.
	out := run(t, s, "read", "maria", "--out", "--no-spawn")
	var saved struct {
		Saved string `json:"saved"`
		Count int    `json:"count"`
	}
	// text mode: parse the "salvo: <path> (<n> mensagens)" line.
	if !strings.Contains(out, "salvo: ") || !strings.Contains(out, "(2 mensagens)") {
		t.Fatalf("expected salvo message: %s", out)
	}
	exportDir := filepath.Join(home, "exports")
	entries, err := os.ReadDir(exportDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one file in %s: %v %v", exportDir, entries, err)
	}
	if !strings.HasPrefix(entries[0].Name(), "Maria") && !strings.HasPrefix(entries[0].Name(), "maria") {
		t.Fatalf("unexpected filename: %s", entries[0].Name())
	}
	if !strings.HasSuffix(entries[0].Name(), ".txt") {
		t.Fatalf("expected .txt by default: %s", entries[0].Name())
	}
	content, err := os.ReadFile(filepath.Join(exportDir, entries[0].Name()))
	if err != nil || !strings.Contains(string(content), "bom dia") {
		t.Fatalf("%v %s", err, content)
	}

	// --out auto explicitly, with --json: same behavior, .json extension.
	out = run(t, s, "read", "maria", "--out=auto", "--json", "--no-spawn")
	if err := json.Unmarshal([]byte(out), &saved); err != nil || saved.Count != 2 || saved.Saved == "" {
		t.Fatalf("%v %s", err, out)
	}
	if !strings.HasPrefix(saved.Saved, exportDir) || !strings.HasSuffix(saved.Saved, ".json") {
		t.Fatalf("expected json export under %s: %s", exportDir, saved.Saved)
	}
	if _, err := os.Stat(saved.Saved); err != nil {
		t.Fatal(err)
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

func TestConfigSetAndShow(t *testing.T) {
	t.Setenv("CLAUDEWHATS_HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "")
	out, err := runWith(nil, "config", "set", "gemini_api_key", "AIzaSyABCDEFGHIJKL1234")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "ok: gemini_api_key definido") {
		t.Fatal(out)
	}
	out, err = runWith(nil, "config", "show")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "AIzaSyABCDEFGHIJKL1234") {
		t.Fatalf("key leaked unmasked: %s", out)
	}
	if !strings.Contains(out, "AIza…1234") {
		t.Fatalf("expected masked key, got: %s", out)
	}
	if !strings.Contains(out, "config.yaml") {
		t.Fatalf("expected source config.yaml: %s", out)
	}
}

func TestConfigSetInvalidPort(t *testing.T) {
	t.Setenv("CLAUDEWHATS_HOME", t.TempDir())
	_, err := runWith(nil, "config", "set", "port", "not-a-number")
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
	_, err = runWith(nil, "config", "set", "port", "70000")
	if err == nil {
		t.Fatal("expected error for out-of-range port")
	}
}

func TestConfigSetPreservesUnknownKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDEWHATS_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("future_flag: keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runWith(nil, "config", "set", "gemini_model", "gemini-2.5-pro"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "future_flag: keep-me") {
		t.Fatalf("unknown key was dropped: %s", b)
	}
	if !strings.Contains(string(b), "gemini_model: gemini-2.5-pro") {
		t.Fatalf("new key missing: %s", b)
	}
}

func TestConfigSetFileModeNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on windows")
	}
	home := t.TempDir()
	t.Setenv("CLAUDEWHATS_HOME", home)
	if _, err := runWith(nil, "config", "set", "gemini_api_key", "secret"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config.yaml is not private: %v", fi.Mode())
	}
}

func TestConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDEWHATS_HOME", home)
	out, err := runWith(nil, "config", "path")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, filepath.Join(home, "config.yaml")) {
		t.Fatalf("unexpected path output: %s", out)
	}
}

func TestConfigShowEnvOverridesKeySource(t *testing.T) {
	t.Setenv("CLAUDEWHATS_HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "AIzaEnvKeyValue1234")
	out, err := runWith(nil, "config", "show", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	keyInfo := payload["gemini_api_key"].(map[string]any)
	if keyInfo["source"] != "env" {
		t.Fatalf("expected env source, got %v", payload)
	}
	if strings.Contains(out, "AIzaEnvKeyValue1234") {
		t.Fatalf("key leaked unmasked: %s", out)
	}
}
