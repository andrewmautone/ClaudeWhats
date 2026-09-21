# ClaudeWhats Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Binário Go `claudewhats` que grava toda mensagem de WhatsApp num SQLite local (com tipo + transcrição Gemini de áudio/imagem), resolve identidades LID↔número, e expõe CLI que o Claude usa via skill para ler/buscar/resumir/sincronizar/enviar.

**Architecture:** Um binário com dois papéis: daemon (`serve`) que conecta via whatsmeow, ingere eventos no store e roda a fila Gemini + HTTP loopback; e subcomandos de CLI que leem só do SQLite e, quando precisam do WhatsApp (`send`, `sync`), falam com o daemon por HTTP em `127.0.0.1`, subindo-o em background (sem janela, com idle timeout) se necessário.

**Tech Stack:** Go 1.23, `go.mau.fi/whatsmeow`, `modernc.org/sqlite` (sem cgo, FTS5), `github.com/spf13/cobra`, `gopkg.in/yaml.v3`, Gemini REST (`generativelanguage.googleapis.com`), `github.com/mdp/qrterminal/v3`.

**Spec:** `docs/superpowers/specs/2026-09-21-claudewhats-design.md`

## Global Constraints

- Módulo: `github.com/andrewmautone/claudewhats`. Go 1.23. Sem cgo (driver `modernc.org/sqlite`, driver name `"sqlite"`).
- Dados em `~/.claudewhats/`: `config.yaml`, `data.db`, `media/<chat>/<msgid>.<ext>`, `daemon.log`, `daemon.pid`. Override com env `CLAUDEWHATS_HOME` (testes usam isso).
- Comandos de leitura (`chats`, `read`, `search`, `summary`, `contact *`, `status`) nunca abrem conexão WhatsApp.
- `messages.type` sempre gravado: `text|audio|image|video|document|sticker|reaction|location|contact|other`.
- Presença: `SendPresence(PresenceUnavailable)` após conectar e após cada envio. Nunca chamar `MarkRead`.
- Nunca re-parear sozinho: `LoggedOut` → loga e encerra.
- Idle timeout do daemon em background: default `30m`; `0` = nunca.
- HTTP do daemon: `127.0.0.1:7411` (config `port`).
- Gemini: modelo default `gemini-2.5-flash`, chave em `GEMINI_API_KEY` ou `config.yaml: gemini_api_key`.
- Todos os subcomandos aceitam `--json`.
- Timestamps em `messages.ts` são Unix seconds (UTC).
- Commits: mensagem em inglês, sufixo `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

## File Structure

```
go.mod
cmd/claudewhats/main.go            # chama cli.Execute()
internal/config/config.go          # Config, Load(), Home(), paths
internal/config/config_test.go
internal/store/store.go            # Open(path), migrations, WAL
internal/store/schema.sql          # embed
internal/store/contacts.go         # contacts + identities + Resolve/Link
internal/store/contacts_test.go
internal/store/messages.go         # chats, messages, FTS, ResolveChat
internal/store/messages_test.go
internal/store/jobs.go             # gemini_jobs
internal/store/jobs_test.go
internal/gemini/client.go          # Transcribe, Describe, Summarize
internal/gemini/client_test.go
internal/wa/classify.go            # Classify(*waE2E.Message) -> Classified
internal/wa/classify_test.go
internal/wa/client.go              # wrapper whatsmeow: Connect, QR, handlers, Send, RequestHistory
internal/daemon/ingest.go          # Ingester: evento -> store + mídia + job
internal/daemon/ingest_test.go
internal/daemon/worker.go          # loop gemini_jobs
internal/daemon/worker_test.go
internal/daemon/server.go          # HTTP loopback + idle timer + Run()
internal/daemon/server_test.go
internal/daemon/client.go          # Client: Status/Send/Sync/Shutdown + EnsureRunning
internal/daemon/client_test.go
internal/daemon/spawn_windows.go   # detached sem janela
internal/daemon/spawn_unix.go
internal/cli/root.go               # cobra root, --json, output helpers
internal/cli/chats.go  read.go  search.go  contact.go  summary.go  sync.go  send.go  status.go  serve.go  stop.go
internal/cli/cli_test.go
skill/SKILL.md
README.md
```

---

### Task 1: Scaffold + config

**Files:**
- Create: `go.mod`, `cmd/claudewhats/main.go`, `internal/cli/root.go`, `internal/config/config.go`, `internal/config/config_test.go`, `.gitignore`

**Interfaces:**
- Produces: `config.Home() string`, `config.Load() (*Config, error)`, `Config{GeminiAPIKey, GeminiModel string; Port int; IdleTimeout time.Duration; MediaDir, DBPath, LogPath, PidPath string}`, `cli.Execute()`.

- [ ] **Step 1: go.mod e deps**

```bash
cd C:/Users/Andrew/Documents/GitHub/ClaudeWhats
go mod init github.com/andrewmautone/claudewhats
go get go.mau.fi/whatsmeow@latest modernc.org/sqlite@latest github.com/spf13/cobra@latest gopkg.in/yaml.v3@latest github.com/mdp/qrterminal/v3@latest google.golang.org/protobuf@latest
printf 'data.db*\n*.exe\n' > .gitignore
```

- [ ] **Step 2: Teste do config**

`internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsAndYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDEWHATS_HOME", home)
	t.Setenv("GEMINI_API_KEY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7411 || cfg.IdleTimeout != 30*time.Minute || cfg.GeminiModel != "gemini-2.5-flash" {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.DBPath != filepath.Join(home, "data.db") {
		t.Fatalf("dbpath %s", cfg.DBPath)
	}
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("gemini_api_key: abc\nport: 8000\nidle_timeout: 5m\n"), 0o600)
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GeminiAPIKey != "abc" || cfg.Port != 8000 || cfg.IdleTimeout != 5*time.Minute {
		t.Fatalf("yaml not applied: %+v", cfg)
	}
	t.Setenv("GEMINI_API_KEY", "env")
	cfg, _ = Load()
	if cfg.GeminiAPIKey != "env" {
		t.Fatal("env should override yaml")
	}
}
```

- [ ] **Step 3: Rodar, ver falhar**

Run: `go test ./internal/config/` → FAIL (package não compila).

- [ ] **Step 4: Implementar config**

`internal/config/config.go`:
```go
package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GeminiAPIKey string        `yaml:"gemini_api_key"`
	GeminiModel  string        `yaml:"gemini_model"`
	Port         int           `yaml:"port"`
	IdleTimeout  time.Duration `yaml:"idle_timeout"`

	Home     string `yaml:"-"`
	DBPath   string `yaml:"-"`
	MediaDir string `yaml:"-"`
	LogPath  string `yaml:"-"`
	PidPath  string `yaml:"-"`
}

// Home returns the data directory (CLAUDEWHATS_HOME or ~/.claudewhats).
func Home() string {
	if h := os.Getenv("CLAUDEWHATS_HOME"); h != "" {
		return h
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return ".claudewhats"
	}
	return filepath.Join(u, ".claudewhats")
}

func Load() (*Config, error) {
	home := Home()
	if err := os.MkdirAll(filepath.Join(home, "media"), 0o700); err != nil {
		return nil, err
	}
	cfg := &Config{GeminiModel: "gemini-2.5-flash", Port: 7411, IdleTimeout: 30 * time.Minute}
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err == nil {
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		cfg.GeminiAPIKey = k
	}
	cfg.Home = home
	cfg.DBPath = filepath.Join(home, "data.db")
	cfg.MediaDir = filepath.Join(home, "media")
	cfg.LogPath = filepath.Join(home, "daemon.log")
	cfg.PidPath = filepath.Join(home, "daemon.pid")
	return cfg, nil
}
```

`internal/cli/root.go`:
```go
package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var jsonOut bool

var root = &cobra.Command{
	Use:           "claudewhats",
	Short:         "WhatsApp local archive + Gemini for Claude",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "output JSON")
}

// emit prints v as JSON when --json, otherwise calls text().
func emit(v any, text func()) error {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	text()
	return nil
}

func Execute() {
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}
```

`cmd/claudewhats/main.go`:
```go
package main

import "github.com/andrewmautone/claudewhats/internal/cli"

func main() { cli.Execute() }
```

- [ ] **Step 5: Rodar testes e build**

Run: `go test ./internal/config/ && go build ./...` → PASS, build ok.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat: scaffold module, config and cobra root"
```

---

### Task 2: Store — abertura e schema

**Files:**
- Create: `internal/store/store.go`, `internal/store/schema.sql`, `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Open(path string) (*Store, error)`, `store.OpenMemory() (*Store, error)` (testes), `(*Store).Close()`, `(*Store).DB() *sql.DB`.

- [ ] **Step 1: Teste**

`internal/store/store_test.go`:
```go
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
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/store/` → FAIL.

- [ ] **Step 3: Schema**

`internal/store/schema.sql`:
```sql
CREATE TABLE IF NOT EXISTS contacts (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  notes TEXT NOT NULL DEFAULT '',
  auto INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS identities (
  jid TEXT PRIMARY KEY,
  contact_id INTEGER NOT NULL REFERENCES contacts(id),
  kind TEXT NOT NULL CHECK (kind IN ('pn','lid')),
  push_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS identities_contact ON identities(contact_id);
CREATE TABLE IF NOT EXISTS chats (
  jid TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('dm','group')),
  name TEXT NOT NULL DEFAULT '',
  last_msg_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS group_members (
  chat_jid TEXT NOT NULL,
  jid TEXT NOT NULL,
  PRIMARY KEY (chat_jid, jid)
);
CREATE TABLE IF NOT EXISTS messages (
  chat_jid TEXT NOT NULL,
  id TEXT NOT NULL,
  sender_jid TEXT NOT NULL,
  ts INTEGER NOT NULL,
  from_me INTEGER NOT NULL DEFAULT 0,
  type TEXT NOT NULL,
  text TEXT NOT NULL DEFAULT '',
  media_path TEXT NOT NULL DEFAULT '',
  media_mime TEXT NOT NULL DEFAULT '',
  transcript TEXT NOT NULL DEFAULT '',
  transcript_status TEXT NOT NULL DEFAULT 'skipped',
  quoted_id TEXT NOT NULL DEFAULT '',
  raw_json TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (chat_jid, id)
);
CREATE INDEX IF NOT EXISTS messages_ts ON messages(ts);
CREATE INDEX IF NOT EXISTS messages_chat_ts ON messages(chat_jid, ts);
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(text, transcript, content='messages', content_rowid='rowid');
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, text, transcript) VALUES (new.rowid, new.text, new.transcript);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text, transcript) VALUES ('delete', old.rowid, old.text, old.transcript);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text, transcript) VALUES ('delete', old.rowid, old.text, old.transcript);
  INSERT INTO messages_fts(rowid, text, transcript) VALUES (new.rowid, new.text, new.transcript);
END;
CREATE TABLE IF NOT EXISTS gemini_jobs (
  id INTEGER PRIMARY KEY,
  chat_jid TEXT NOT NULL,
  msg_id TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  UNIQUE (chat_jid, msg_id)
);
```

`internal/store/store.go`:
```go
package store

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	return open(dsn)
}

func OpenMemory() (*Store, error) {
	return open("file::memory:?_pragma=foreign_keys(1)")
}

func open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) DB() *sql.DB { return s.db }
func (s *Store) Close() error { return s.db.Close() }
```

- [ ] **Step 4: Rodar teste**

Run: `go test ./internal/store/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): sqlite schema with fts5 and jobs"
```

---

### Task 3: Store — contatos e identidades

**Files:**
- Create: `internal/store/contacts.go`, `internal/store/contacts_test.go`

**Interfaces:**
- Produces:
  - `type Contact struct{ ID int64; Name, Notes string; Auto bool; JIDs []string }`
  - `(*Store).ResolveIdentity(jid, altJID, pushName string) (contactID int64, err error)` — cria contato auto se não existe; se `altJID != ""` garante que ambos apontam pro mesmo contato (funde se preciso).
  - `(*Store).AddContact(name, number string) (int64, error)` — `number` só dígitos → jid `<digits>@s.whatsapp.net`, `auto=0`.
  - `(*Store).LinkContacts(a, b string) (int64, error)` — `a`,`b` = jid ou nome; funde no contato manual (auto=0) se houver, senão no menor id.
  - `(*Store).RenameContact(ref, name string) error`
  - `(*Store).ListContacts(q string) ([]Contact, error)`
  - `(*Store).ContactName(jid string) string` — nome do contato, ou push_name, ou o user do jid.
  - `KindOf(jid string) string` → `"lid"` se termina em `@lid`, senão `"pn"`.

- [ ] **Step 1: Testes**

`internal/store/contacts_test.go`:
```go
package store

import "testing"

func mustMem(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestResolveIdentityAutoCreatesAndLinksAlt(t *testing.T) {
	s := mustMem(t)
	lid := "111@lid"
	pn := "5511999@s.whatsapp.net"
	c1, err := s.ResolveIdentity(lid, "", "Fulano")
	if err != nil || c1 == 0 {
		t.Fatal(err)
	}
	c2, _ := s.ResolveIdentity(pn, lid, "Fulano")
	if c1 != c2 {
		t.Fatalf("alt should share contact: %d %d", c1, c2)
	}
	if s.ContactName(lid) != "Fulano" {
		t.Fatal("push name should be used as display")
	}
}

func TestResolveIdentityMergesTwoExistingContacts(t *testing.T) {
	s := mustMem(t)
	manual, _ := s.AddContact("Maria", "5511888")
	autoID, _ := s.ResolveIdentity("222@lid", "", "M.")
	if manual == autoID {
		t.Fatal("precondition")
	}
	got, err := s.ResolveIdentity("222@lid", "5511888@s.whatsapp.net", "M.")
	if err != nil {
		t.Fatal(err)
	}
	if got != manual {
		t.Fatalf("manual contact must win, got %d want %d", got, manual)
	}
	cs, _ := s.ListContacts("")
	if len(cs) != 1 || len(cs[0].JIDs) != 2 {
		t.Fatalf("expected 1 merged contact with 2 jids: %+v", cs)
	}
}

func TestLinkAndRenameByName(t *testing.T) {
	s := mustMem(t)
	s.AddContact("Joao", "5511777")
	s.ResolveIdentity("333@lid", "", "jj")
	id, err := s.LinkContacts("Joao", "333@lid")
	if err != nil {
		t.Fatal(err)
	}
	if s.ContactName("333@lid") != "Joao" {
		t.Fatal("lid should now resolve to Joao")
	}
	if err := s.RenameContact("333@lid", "João Silva"); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.ListContacts("silva")
	if len(cs) != 1 || cs[0].ID != id || cs[0].Name != "João Silva" {
		t.Fatalf("%+v", cs)
	}
}

func TestContactNameFallbacks(t *testing.T) {
	s := mustMem(t)
	if s.ContactName("5511666@s.whatsapp.net") != "5511666" {
		t.Fatal("unknown jid should fall back to user part")
	}
	if KindOf("1@lid") != "lid" || KindOf("1@s.whatsapp.net") != "pn" {
		t.Fatal("KindOf")
	}
}
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/store/ -run 'Resolve|Link|ContactName'` → FAIL.

- [ ] **Step 3: Implementar**

`internal/store/contacts.go`:
```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Contact struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Notes string   `json:"notes,omitempty"`
	Auto  bool     `json:"auto"`
	JIDs  []string `json:"jids"`
}

func KindOf(jid string) string {
	if strings.HasSuffix(jid, "@lid") {
		return "lid"
	}
	return "pn"
}

func userPart(jid string) string {
	if i := strings.IndexByte(jid, '@'); i > 0 {
		return jid[:i]
	}
	return jid
}

func (s *Store) contactOf(jid string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT contact_id FROM identities WHERE jid=?`, jid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) newContact(name string, auto bool) (int64, error) {
	a := 0
	if auto {
		a = 1
	}
	res, err := s.db.Exec(`INSERT INTO contacts(name, auto, created_at) VALUES (?,?,?)`, name, a, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) putIdentity(jid string, contactID int64, pushName string) error {
	_, err := s.db.Exec(`INSERT INTO identities(jid, contact_id, kind, push_name) VALUES (?,?,?,?)
		ON CONFLICT(jid) DO UPDATE SET contact_id=excluded.contact_id,
		push_name=CASE WHEN excluded.push_name<>'' THEN excluded.push_name ELSE identities.push_name END`,
		jid, contactID, KindOf(jid), pushName)
	return err
}

// merge moves every identity of `from` into `into` and deletes `from`.
func (s *Store) merge(into, from int64) error {
	if into == from {
		return nil
	}
	if _, err := s.db.Exec(`UPDATE identities SET contact_id=? WHERE contact_id=?`, into, from); err != nil {
		return err
	}
	// keep a name if the winner has none
	_, err := s.db.Exec(`UPDATE contacts SET name=(SELECT name FROM contacts WHERE id=?) WHERE id=? AND name=''`, from, into)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM contacts WHERE id=?`, from)
	return err
}

// winner picks which of two contacts survives a merge: manual (auto=0) wins, else lowest id.
func (s *Store) winner(a, b int64) (into, from int64, err error) {
	var autoA, autoB int
	if err = s.db.QueryRow(`SELECT auto FROM contacts WHERE id=?`, a).Scan(&autoA); err != nil {
		return
	}
	if err = s.db.QueryRow(`SELECT auto FROM contacts WHERE id=?`, b).Scan(&autoB); err != nil {
		return
	}
	switch {
	case autoA == 0 && autoB != 0:
		return a, b, nil
	case autoB == 0 && autoA != 0:
		return b, a, nil
	case a < b:
		return a, b, nil
	default:
		return b, a, nil
	}
}

// ResolveIdentity returns the contact for jid, creating an auto contact when unknown.
// altJID (the LID<->PN alternative) is bound to the same contact.
func (s *Store) ResolveIdentity(jid, altJID, pushName string) (int64, error) {
	if jid == "" {
		return 0, errors.New("empty jid")
	}
	id, err := s.contactOf(jid)
	if err != nil {
		return 0, err
	}
	var altID int64
	if altJID != "" && altJID != jid {
		if altID, err = s.contactOf(altJID); err != nil {
			return 0, err
		}
	}
	switch {
	case id == 0 && altID == 0:
		if id, err = s.newContact("", true); err != nil {
			return 0, err
		}
	case id == 0:
		id = altID
	case altID != 0 && altID != id:
		into, from, err := s.winner(id, altID)
		if err != nil {
			return 0, err
		}
		if err := s.merge(into, from); err != nil {
			return 0, err
		}
		id = into
	}
	if err := s.putIdentity(jid, id, pushName); err != nil {
		return 0, err
	}
	if altJID != "" && altJID != jid {
		if err := s.putIdentity(altJID, id, pushName); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) AddContact(name, number string) (int64, error) {
	d := digits(number)
	if d == "" {
		return 0, fmt.Errorf("número inválido: %q", number)
	}
	jid := d + "@s.whatsapp.net"
	existing, err := s.contactOf(jid)
	if err != nil {
		return 0, err
	}
	if existing != 0 {
		_, err = s.db.Exec(`UPDATE contacts SET name=?, auto=0 WHERE id=?`, name, existing)
		return existing, err
	}
	id, err := s.newContact(name, false)
	if err != nil {
		return 0, err
	}
	return id, s.putIdentity(jid, id, "")
}

// findContact resolves a jid, phone number or (case-insensitive substring) name to a contact id.
func (s *Store) findContact(ref string) (int64, error) {
	if strings.Contains(ref, "@") {
		id, err := s.contactOf(ref)
		if err != nil {
			return 0, err
		}
		if id == 0 {
			return 0, fmt.Errorf("jid desconhecido: %s", ref)
		}
		return id, nil
	}
	if d := digits(ref); d != "" && d == strings.TrimPrefix(ref, "+") {
		return s.findContact(d + "@s.whatsapp.net")
	}
	rows, err := s.db.Query(`SELECT id, name FROM contacts WHERE lower(name) LIKE '%'||lower(?)||'%' ORDER BY (lower(name)=lower(?)) DESC, id LIMIT 5`, ref, ref)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []int64
	var names []string
	for rows.Next() {
		var id int64
		var n string
		rows.Scan(&id, &n)
		ids = append(ids, id)
		names = append(names, n)
	}
	switch {
	case len(ids) == 0:
		return 0, fmt.Errorf("contato não encontrado: %s", ref)
	case len(ids) > 1 && !strings.EqualFold(names[0], ref):
		return 0, fmt.Errorf("ambíguo %q: %s", ref, strings.Join(names, ", "))
	}
	return ids[0], nil
}

func (s *Store) LinkContacts(a, b string) (int64, error) {
	ia, err := s.findContact(a)
	if err != nil {
		return 0, err
	}
	ib, err := s.findContact(b)
	if err != nil {
		return 0, err
	}
	into, from, err := s.winner(ia, ib)
	if err != nil {
		return 0, err
	}
	return into, s.merge(into, from)
}

func (s *Store) RenameContact(ref, name string) error {
	id, err := s.findContact(ref)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE contacts SET name=?, auto=0 WHERE id=?`, name, id)
	return err
}

func (s *Store) ListContacts(q string) ([]Contact, error) {
	rows, err := s.db.Query(`SELECT c.id, c.name, c.notes, c.auto, group_concat(i.jid, ' ')
		FROM contacts c LEFT JOIN identities i ON i.contact_id=c.id
		WHERE ?='' OR lower(c.name) LIKE '%'||lower(?)||'%' OR i.jid LIKE '%'||?||'%'
		GROUP BY c.id ORDER BY c.name, c.id`, q, q, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		var auto int
		var jids sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.Notes, &auto, &jids); err != nil {
			return nil, err
		}
		c.Auto = auto == 1
		if jids.Valid {
			c.JIDs = strings.Fields(jids.String)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ContactName is the display name for a jid: contact name, else push name, else jid user.
func (s *Store) ContactName(jid string) string {
	var name, push string
	err := s.db.QueryRow(`SELECT c.name, i.push_name FROM identities i JOIN contacts c ON c.id=i.contact_id WHERE i.jid=?`, jid).Scan(&name, &push)
	if err == nil {
		if name != "" {
			return name
		}
		if push != "" {
			return push
		}
	}
	return userPart(jid)
}
```

- [ ] **Step 4: Rodar testes**

Run: `go test ./internal/store/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): contacts, identities and LID/PN auto-merge"
```

---

### Task 4: Store — chats, mensagens, busca

**Files:**
- Create: `internal/store/messages.go`, `internal/store/messages_test.go`

**Interfaces:**
- Produces:
  - `type Message struct{ ChatJID, ID, SenderJID string; TS int64; FromMe bool; Type, Text, MediaPath, MediaMime, Transcript, TranscriptStatus, QuotedID, RawJSON string; Sender string /*display, filled by queries*/ }`
  - `type Chat struct{ JID, Kind, Name string; LastMsgAt int64; Count int64 }`
  - `(*Store).UpsertChat(jid, kind, name string) error` (name vazio não sobrescreve)
  - `(*Store).SetGroupMembers(chatJID string, jids []string) error`
  - `(*Store).InsertMessage(m Message) (inserted bool, err error)` — ignora duplicata (PK); atualiza `chats.last_msg_at`.
  - `(*Store).SetTranscript(chatJID, id, transcript, status string) error`
  - `(*Store).ListChats(since int64, kind string) ([]Chat, error)` — `Count` = msgs com `ts>=since`; ordena por last_msg_at desc; `since=0` = todas com count total.
  - `(*Store).ReadMessages(chatJID string, since int64, limit int) ([]Message, error)` — cronológico asc (últimos `limit`).
  - `(*Store).Search(q, chatJID string, since int64, limit int) ([]Message, error)` — FTS5.
  - `(*Store).ResolveChat(ref string) (Chat, error)` — jid exato; número → dm jid; nome de chat (grupo) ou nome de contato (dm) substring case-insensitive; ambíguo → erro listando.
  - `(*Store).GetMessage(chatJID, id string) (Message, bool, error)`
  - `(*Store).OldestMessage(chatJID string) (Message, bool, error)`
  - `(*Store).Stats() (msgs, pendingJobs int64, err error)`

- [ ] **Step 1: Testes**

`internal/store/messages_test.go`:
```go
package store

import (
	"strings"
	"testing"
)

func seed(t *testing.T, s *Store) {
	t.Helper()
	s.AddContact("Maria", "5511888")
	s.UpsertChat("5511888@s.whatsapp.net", "dm", "")
	s.UpsertChat("123@g.us", "group", "Família")
	s.InsertMessage(Message{ChatJID: "5511888@s.whatsapp.net", ID: "m1", SenderJID: "5511888@s.whatsapp.net", TS: 100, Type: "text", Text: "bom dia, reunião amanhã"})
	s.InsertMessage(Message{ChatJID: "5511888@s.whatsapp.net", ID: "m2", SenderJID: "me@s.whatsapp.net", TS: 200, FromMe: true, Type: "audio", TranscriptStatus: "pending"})
	s.InsertMessage(Message{ChatJID: "123@g.us", ID: "g1", SenderJID: "5511888@s.whatsapp.net", TS: 300, Type: "image", Text: "olha", TranscriptStatus: "pending"})
}

func TestInsertDedupAndLastMsg(t *testing.T) {
	s := mustMem(t)
	seed(t, s)
	ins, err := s.InsertMessage(Message{ChatJID: "123@g.us", ID: "g1", SenderJID: "x", TS: 1, Type: "text"})
	if err != nil || ins {
		t.Fatalf("dup should not insert: %v %v", ins, err)
	}
	chats, _ := s.ListChats(0, "")
	if len(chats) != 2 || chats[0].JID != "123@g.us" || chats[0].LastMsgAt != 300 || chats[1].Count != 2 {
		t.Fatalf("%+v", chats)
	}
	chats, _ = s.ListChats(150, "dm")
	if len(chats) != 1 || chats[0].Count != 1 {
		t.Fatalf("since filter: %+v", chats)
	}
}

func TestReadAndTranscriptAndSearch(t *testing.T) {
	s := mustMem(t)
	seed(t, s)
	if err := s.SetTranscript("5511888@s.whatsapp.net", "m2", "vou levar o bolo", "done"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 10)
	if len(msgs) != 2 || msgs[0].ID != "m1" || msgs[1].Transcript != "vou levar o bolo" || msgs[0].Sender != "Maria" {
		t.Fatalf("%+v", msgs)
	}
	msgs, _ = s.ReadMessages("5511888@s.whatsapp.net", 0, 1)
	if len(msgs) != 1 || msgs[0].ID != "m2" {
		t.Fatalf("limit should keep the latest: %+v", msgs)
	}
	hits, _ := s.Search("bolo", "", 0, 10)
	if len(hits) != 1 || hits[0].ID != "m2" {
		t.Fatalf("fts transcript: %+v", hits)
	}
	hits, _ = s.Search("reunião", "123@g.us", 0, 10)
	if len(hits) != 0 {
		t.Fatal("chat filter")
	}
}

func TestResolveChat(t *testing.T) {
	s := mustMem(t)
	seed(t, s)
	for _, ref := range []string{"5511888@s.whatsapp.net", "5511888", "maria"} {
		c, err := s.ResolveChat(ref)
		if err != nil || c.JID != "5511888@s.whatsapp.net" {
			t.Fatalf("%s: %+v %v", ref, c, err)
		}
	}
	c, err := s.ResolveChat("fam")
	if err != nil || c.JID != "123@g.us" {
		t.Fatalf("%+v %v", c, err)
	}
	s.UpsertChat("456@g.us", "group", "Família 2")
	if _, err := s.ResolveChat("fam"); err == nil || !strings.Contains(err.Error(), "Família 2") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	if _, err := s.ResolveChat("zzz"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestOldestAndStats(t *testing.T) {
	s := mustMem(t)
	seed(t, s)
	m, ok, _ := s.OldestMessage("5511888@s.whatsapp.net")
	if !ok || m.ID != "m1" {
		t.Fatalf("%+v", m)
	}
	g, ok, _ := s.GetMessage("123@g.us", "g1")
	if !ok || g.Type != "image" {
		t.Fatalf("%+v", g)
	}
	if _, ok, _ := s.GetMessage("123@g.us", "nope"); ok {
		t.Fatal("missing message")
	}
	n, _, _ := s.Stats()
	if n != 3 {
		t.Fatal(n)
	}
}
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/store/` → FAIL.

- [ ] **Step 3: Implementar**

`internal/store/messages.go`:
```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Message struct {
	ChatJID          string `json:"chat_jid"`
	ID               string `json:"id"`
	SenderJID        string `json:"sender_jid"`
	Sender           string `json:"sender"`
	TS               int64  `json:"ts"`
	FromMe           bool   `json:"from_me"`
	Type             string `json:"type"`
	Text             string `json:"text,omitempty"`
	MediaPath        string `json:"media_path,omitempty"`
	MediaMime        string `json:"media_mime,omitempty"`
	Transcript       string `json:"transcript,omitempty"`
	TranscriptStatus string `json:"transcript_status"`
	QuotedID         string `json:"quoted_id,omitempty"`
	RawJSON          string `json:"-"`
}

type Chat struct {
	JID       string `json:"jid"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	LastMsgAt int64  `json:"last_msg_at"`
	Count     int64  `json:"count"`
}

func (s *Store) UpsertChat(jid, kind, name string) error {
	_, err := s.db.Exec(`INSERT INTO chats(jid, kind, name) VALUES (?,?,?)
		ON CONFLICT(jid) DO UPDATE SET name=CASE WHEN excluded.name<>'' THEN excluded.name ELSE chats.name END`, jid, kind, name)
	return err
}

func (s *Store) SetGroupMembers(chatJID string, jids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM group_members WHERE chat_jid=?`, chatJID); err != nil {
		return err
	}
	for _, j := range jids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO group_members(chat_jid, jid) VALUES (?,?)`, chatJID, j); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) InsertMessage(m Message) (bool, error) {
	if m.TranscriptStatus == "" {
		m.TranscriptStatus = "skipped"
	}
	fm := 0
	if m.FromMe {
		fm = 1
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO messages(chat_jid,id,sender_jid,ts,from_me,type,text,media_path,media_mime,transcript,transcript_status,quoted_id,raw_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ChatJID, m.ID, m.SenderJID, m.TS, fm, m.Type, m.Text, m.MediaPath, m.MediaMime, m.Transcript, m.TranscriptStatus, m.QuotedID, m.RawJSON)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	_, err = s.db.Exec(`UPDATE chats SET last_msg_at=max(last_msg_at, ?) WHERE jid=?`, m.TS, m.ChatJID)
	return true, err
}

func (s *Store) SetTranscript(chatJID, id, transcript, status string) error {
	_, err := s.db.Exec(`UPDATE messages SET transcript=?, transcript_status=? WHERE chat_jid=? AND id=?`, transcript, status, chatJID, id)
	return err
}

func (s *Store) ListChats(since int64, kind string) ([]Chat, error) {
	rows, err := s.db.Query(`SELECT c.jid, c.kind, c.name, c.last_msg_at,
		(SELECT count(*) FROM messages m WHERE m.chat_jid=c.jid AND m.ts>=?)
		FROM chats c WHERE (?='' OR c.kind=?) AND c.last_msg_at>=? ORDER BY c.last_msg_at DESC`, since, kind, kind, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.JID, &c.Kind, &c.Name, &c.LastMsgAt, &c.Count); err != nil {
			return nil, err
		}
		if c.Name == "" {
			c.Name = s.ContactName(c.JID)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const msgCols = `chat_jid,id,sender_jid,ts,from_me,type,text,media_path,media_mime,transcript,transcript_status,quoted_id`

func (s *Store) scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var fm int
		if err := rows.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.TS, &fm, &m.Type, &m.Text, &m.MediaPath, &m.MediaMime, &m.Transcript, &m.TranscriptStatus, &m.QuotedID); err != nil {
			return nil, err
		}
		m.FromMe = fm == 1
		if m.FromMe {
			m.Sender = "eu"
		} else {
			m.Sender = s.ContactName(m.SenderJID)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ReadMessages(chatJID string, since int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM (SELECT * FROM messages WHERE chat_jid=? AND ts>=? ORDER BY ts DESC LIMIT ?) ORDER BY ts ASC`, chatJID, since, limit)
	if err != nil {
		return nil, err
	}
	return s.scanMessages(rows)
}

func (s *Store) Search(q, chatJID string, since int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages WHERE rowid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)
		AND (?='' OR chat_jid=?) AND ts>=? ORDER BY ts DESC LIMIT ?`, ftsQuery(q), chatJID, chatJID, since, limit)
	if err != nil {
		return nil, err
	}
	return s.scanMessages(rows)
}

// ftsQuery quotes each word so punctuation never breaks FTS5 syntax.
func ftsQuery(q string) string {
	ws := strings.Fields(q)
	for i, w := range ws {
		ws[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return strings.Join(ws, " ")
}

func (s *Store) chatByJID(jid string) (Chat, error) {
	var c Chat
	err := s.db.QueryRow(`SELECT jid, kind, name, last_msg_at FROM chats WHERE jid=?`, jid).Scan(&c.JID, &c.Kind, &c.Name, &c.LastMsgAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("chat não encontrado: %s", jid)
	}
	if c.Name == "" {
		c.Name = s.ContactName(c.JID)
	}
	return c, err
}

func (s *Store) ResolveChat(ref string) (Chat, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, "@") {
		return s.chatByJID(ref)
	}
	if d := digits(ref); d != "" && d == strings.TrimPrefix(ref, "+") {
		return s.chatByJID(d + "@s.whatsapp.net")
	}
	// group name or contact name, case-insensitive substring
	rows, err := s.db.Query(`SELECT jid, name FROM (
		SELECT jid, name FROM chats WHERE kind='group' AND lower(name) LIKE '%'||lower(?)||'%'
		UNION
		SELECT ch.jid, c.name FROM chats ch JOIN identities i ON i.jid=ch.jid JOIN contacts c ON c.id=i.contact_id
		WHERE ch.kind='dm' AND (lower(c.name) LIKE '%'||lower(?)||'%' OR lower(i.push_name) LIKE '%'||lower(?)||'%')
	) ORDER BY (lower(name)=lower(?)) DESC LIMIT 6`, ref, ref, ref, ref)
	if err != nil {
		return Chat{}, err
	}
	defer rows.Close()
	var jids, names []string
	for rows.Next() {
		var j, n string
		rows.Scan(&j, &n)
		jids = append(jids, j)
		names = append(names, n)
	}
	switch {
	case len(jids) == 0:
		return Chat{}, fmt.Errorf("chat não encontrado: %q", ref)
	case len(jids) > 1 && !strings.EqualFold(names[0], ref):
		return Chat{}, fmt.Errorf("ambíguo %q: %s", ref, strings.Join(names, ", "))
	}
	return s.chatByJID(jids[0])
}

func (s *Store) GetMessage(chatJID, id string) (Message, bool, error) {
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages WHERE chat_jid=? AND id=?`, chatJID, id)
	if err != nil {
		return Message{}, false, err
	}
	ms, err := s.scanMessages(rows)
	if err != nil || len(ms) == 0 {
		return Message{}, false, err
	}
	return ms[0], true, nil
}

func (s *Store) OldestMessage(chatJID string) (Message, bool, error) {
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages WHERE chat_jid=? ORDER BY ts ASC LIMIT 1`, chatJID)
	if err != nil {
		return Message{}, false, err
	}
	ms, err := s.scanMessages(rows)
	if err != nil || len(ms) == 0 {
		return Message{}, false, err
	}
	return ms[0], true, nil
}

func (s *Store) Stats() (msgs, pendingJobs int64, err error) {
	if err = s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&msgs); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT count(*) FROM gemini_jobs`).Scan(&pendingJobs)
	return
}
```

- [ ] **Step 4: Rodar testes**

Run: `go test ./internal/store/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): chats, messages, fts search, chat resolution"
```

---

### Task 5: Store — fila Gemini

**Files:**
- Create: `internal/store/jobs.go`, `internal/store/jobs_test.go`

**Interfaces:**
- Produces: `type Job struct{ ID int64; ChatJID, MsgID string; Attempts int }`, `(*Store).EnqueueJob(chatJID, msgID string) error`, `(*Store).NextJob(now int64) (Job, bool, error)`, `(*Store).CompleteJob(id int64) error`, `(*Store).FailJob(id int64, now int64, errMsg string, maxAttempts int) (gaveUp bool, err error)` — backoff `30s * 2^attempts`; ao desistir apaga o job e marca `transcript_status='failed'`.

- [ ] **Step 1: Teste**

`internal/store/jobs_test.go`:
```go
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
	ms, _ := s.ReadMessages("123@g.us", 0, 10)
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
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/store/ -run Job` → FAIL.

- [ ] **Step 3: Implementar**

`internal/store/jobs.go`:
```go
package store

import (
	"database/sql"
	"errors"
)

type Job struct {
	ID       int64
	ChatJID  string
	MsgID    string
	Attempts int
}

func (s *Store) EnqueueJob(chatJID, msgID string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO gemini_jobs(chat_jid, msg_id) VALUES (?,?)`, chatJID, msgID)
	return err
}

func (s *Store) NextJob(now int64) (Job, bool, error) {
	var j Job
	err := s.db.QueryRow(`SELECT id, chat_jid, msg_id, attempts FROM gemini_jobs WHERE next_at<=? ORDER BY next_at, id LIMIT 1`, now).
		Scan(&j.ID, &j.ChatJID, &j.MsgID, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return j, false, nil
	}
	return j, err == nil, err
}

func (s *Store) CompleteJob(id int64) error {
	_, err := s.db.Exec(`DELETE FROM gemini_jobs WHERE id=?`, id)
	return err
}

func (s *Store) FailJob(id, now int64, errMsg string, maxAttempts int) (bool, error) {
	var j Job
	if err := s.db.QueryRow(`SELECT id, chat_jid, msg_id, attempts FROM gemini_jobs WHERE id=?`, id).Scan(&j.ID, &j.ChatJID, &j.MsgID, &j.Attempts); err != nil {
		return false, err
	}
	j.Attempts++
	if j.Attempts >= maxAttempts {
		if err := s.SetTranscript(j.ChatJID, j.MsgID, "", "failed"); err != nil {
			return false, err
		}
		return true, s.CompleteJob(id)
	}
	backoff := int64(30) << uint(j.Attempts-1)
	_, err := s.db.Exec(`UPDATE gemini_jobs SET attempts=?, next_at=?, last_error=? WHERE id=?`, j.Attempts, now+backoff, errMsg, id)
	return false, err
}
```

- [ ] **Step 4: Rodar**

Run: `go test ./internal/store/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(store): gemini job queue with backoff"
```

---

### Task 6: Cliente Gemini

**Files:**
- Create: `internal/gemini/client.go`, `internal/gemini/client_test.go`

**Interfaces:**
- Produces: `gemini.New(apiKey, model string) *Client` (campo exportado `BaseURL` para testes), `(*Client).Transcribe(ctx, audio []byte, mime string) (string, error)`, `(*Client).Describe(ctx, image []byte, mime, caption string) (string, error)`, `(*Client).Summarize(ctx, instructions, text string) (string, error)`, `var ErrNoKey`.

- [ ] **Step 1: Teste**

`internal/gemini/client_test.go`:
```go
package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeServer(t *testing.T, reply string, capture *map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "models/test-model:generateContent") || r.URL.Query().Get("key") != "k" {
			t.Errorf("bad request %s %s", r.URL.Path, r.URL.RawQuery)
		}
		json.NewDecoder(r.Body).Decode(capture)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"` + reply + `"}]}}]}`))
	}))
}

func TestTranscribeSendsInlineAudio(t *testing.T) {
	var got map[string]any
	srv := fakeServer(t, "olá mundo", &got)
	defer srv.Close()
	c := New("k", "test-model")
	c.BaseURL = srv.URL
	out, err := c.Transcribe(context.Background(), []byte("abc"), "audio/ogg")
	if err != nil || out != "olá mundo" {
		t.Fatal(out, err)
	}
	parts := got["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected text+inline parts: %v", parts)
	}
	inline := parts[1].(map[string]any)["inline_data"].(map[string]any)
	if inline["mime_type"] != "audio/ogg" || inline["data"] != "YWJj" {
		t.Fatalf("%v", inline)
	}
}

func TestSummarizeAndErrors(t *testing.T) {
	var got map[string]any
	srv := fakeServer(t, "resumo", &got)
	defer srv.Close()
	c := New("k", "test-model")
	c.BaseURL = srv.URL
	out, err := c.Summarize(context.Background(), "resuma", "texto longo")
	if err != nil || out != "resumo" {
		t.Fatal(out, err)
	}
	if _, err := New("", "m").Summarize(context.Background(), "a", "b"); err != ErrNoKey {
		t.Fatal("missing key must be ErrNoKey")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer bad.Close()
	c.BaseURL = bad.URL
	if _, err := c.Describe(context.Background(), []byte("x"), "image/jpeg", ""); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected http error, got %v", err)
	}
}
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/gemini/` → FAIL.

- [ ] **Step 3: Implementar**

`internal/gemini/client.go`:
```go
package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrNoKey = errors.New("GEMINI_API_KEY não configurada")

type Client struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

func New(apiKey, model string) *Client {
	return &Client{APIKey: apiKey, Model: model, BaseURL: "https://generativelanguage.googleapis.com", HTTP: &http.Client{Timeout: 120 * time.Second}}
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
}
type inlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}
type request struct {
	Contents []struct {
		Parts []part `json:"parts"`
	} `json:"contents"`
}

func (c *Client) generate(ctx context.Context, parts []part) (string, error) {
	if c.APIKey == "" {
		return "", ErrNoKey
	}
	var req request
	req.Contents = append(req.Contents, struct {
		Parts []part `json:"parts"`
	}{parts})
	body, _ := json.Marshal(req)
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", c.BaseURL, c.Model, c.APIKey)
	hr, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("gemini http %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", err
	}
	if len(out.Candidates) == 0 {
		return "", errors.New("gemini: resposta sem candidatos")
	}
	var sb strings.Builder
	for _, p := range out.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	return strings.TrimSpace(sb.String()), nil
}

func (c *Client) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	return c.generate(ctx, []part{
		{Text: "Transcreva este áudio fielmente, em português do Brasil quando for o idioma falado. Responda só com a transcrição, sem comentários."},
		{InlineData: &inlineData{MimeType: mime, Data: base64.StdEncoding.EncodeToString(audio)}},
	})
}

func (c *Client) Describe(ctx context.Context, image []byte, mime, caption string) (string, error) {
	prompt := "Descreva esta imagem em uma ou duas frases em português e transcreva qualquer texto visível. Responda só com a descrição."
	if caption != "" {
		prompt += " Legenda enviada junto: " + caption
	}
	return c.generate(ctx, []part{
		{Text: prompt},
		{InlineData: &inlineData{MimeType: mime, Data: base64.StdEncoding.EncodeToString(image)}},
	})
}

func (c *Client) Summarize(ctx context.Context, instructions, text string) (string, error) {
	return c.generate(ctx, []part{{Text: instructions + "\n\n---\n\n" + text}})
}
```

- [ ] **Step 4: Rodar**

Run: `go test ./internal/gemini/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(gemini): rest client for transcribe, describe, summarize"
```

---

### Task 7: wa — classificação de mensagem

**Files:**
- Create: `internal/wa/classify.go`, `internal/wa/classify_test.go`

**Interfaces:**
- Produces: `type Classified struct{ Type, Text, Mime, Ext, QuotedID string; Media whatsmeow.DownloadableMessage /*nil se não há mídia*/; Transcribe bool }`, `wa.Classify(msg *waE2E.Message) Classified`. `Transcribe` = true só para `audio` e `image`.

- [ ] **Step 1: Teste**

`internal/wa/classify_test.go`:
```go
package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		typ  string
		text string
		mime string
		tr   bool
	}{
		{"conversation", &waE2E.Message{Conversation: proto.String("oi")}, "text", "oi", "", false},
		{"extended", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link"), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q1")}}}, "text", "link", "", false},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true)}}, "audio", "", "audio/ogg", true},
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg"), Caption: proto.String("olha")}}, "image", "olha", "image/jpeg", true},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: proto.String("video/mp4")}}, "video", "", "video/mp4", false},
		{"doc", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String("a.pdf")}}, "document", "a.pdf", "application/pdf", false},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}}, "sticker", "", "image/webp", false},
		{"reaction", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍"), Key: &waCommon.MessageKey{ID: proto.String("R1")}}}, "reaction", "👍", "", false},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(-23.5), DegreesLongitude: proto.Float64(-46.6)}}, "location", "-23.500000,-46.600000", "", false},
		{"contact", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Zé")}}, "contact", "Zé", "", false},
		{"other", &waE2E.Message{}, "other", "", "", false},
	}
	for _, c := range cases {
		got := Classify(c.msg)
		if got.Type != c.typ || got.Text != c.text || got.Mime != c.mime || got.Transcribe != c.tr {
			t.Errorf("%s: got %+v", c.name, got)
		}
		if (got.Media != nil) != (c.typ == "audio" || c.typ == "image" || c.typ == "video" || c.typ == "document" || c.typ == "sticker") {
			t.Errorf("%s: media presence wrong", c.name)
		}
	}
	if Classify(cases[1].msg).QuotedID != "Q1" {
		t.Error("quoted id")
	}
	if Classify(cases[7].msg).QuotedID != "R1" {
		t.Error("reaction target id")
	}
	if Classify(cases[2].msg).Ext != ".ogg" {
		t.Error("audio ext")
	}
}
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/wa/` → FAIL.

- [ ] **Step 3: Implementar**

`internal/wa/classify.go`:
```go
package wa

import (
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

type Classified struct {
	Type       string
	Text       string
	Mime       string
	Ext        string
	QuotedID   string
	Media      whatsmeow.DownloadableMessage
	Transcribe bool
}

var extByMime = map[string]string{
	"audio/ogg": ".ogg", "audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/aac": ".aac", "audio/wav": ".wav",
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
	"video/mp4": ".mp4", "application/pdf": ".pdf",
}

func baseMime(m string) string {
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = m[:i]
	}
	return strings.TrimSpace(m)
}

func extFor(mime, fileName string) string {
	if i := strings.LastIndexByte(fileName, '.'); i >= 0 && i < len(fileName)-1 {
		return fileName[i:]
	}
	if e, ok := extByMime[mime]; ok {
		return e
	}
	return ".bin"
}

// Classify maps a raw WhatsApp message to our storage type, text and media handle.
func Classify(msg *waE2E.Message) Classified {
	if msg == nil {
		return Classified{Type: "other"}
	}
	switch {
	case msg.GetConversation() != "":
		return Classified{Type: "text", Text: msg.GetConversation()}
	case msg.GetExtendedTextMessage() != nil:
		m := msg.GetExtendedTextMessage()
		return Classified{Type: "text", Text: m.GetText(), QuotedID: m.GetContextInfo().GetStanzaID()}
	case msg.GetAudioMessage() != nil:
		m := msg.GetAudioMessage()
		mime := baseMime(m.GetMimetype())
		return Classified{Type: "audio", Mime: mime, Ext: extFor(mime, ""), Media: m, Transcribe: true, QuotedID: m.GetContextInfo().GetStanzaID()}
	case msg.GetImageMessage() != nil:
		m := msg.GetImageMessage()
		mime := baseMime(m.GetMimetype())
		return Classified{Type: "image", Text: m.GetCaption(), Mime: mime, Ext: extFor(mime, ""), Media: m, Transcribe: true, QuotedID: m.GetContextInfo().GetStanzaID()}
	case msg.GetVideoMessage() != nil:
		m := msg.GetVideoMessage()
		mime := baseMime(m.GetMimetype())
		return Classified{Type: "video", Text: m.GetCaption(), Mime: mime, Ext: extFor(mime, ""), Media: m, QuotedID: m.GetContextInfo().GetStanzaID()}
	case msg.GetDocumentMessage() != nil:
		m := msg.GetDocumentMessage()
		mime := baseMime(m.GetMimetype())
		text := m.GetFileName()
		if c := m.GetCaption(); c != "" {
			text = text + " — " + c
		}
		return Classified{Type: "document", Text: text, Mime: mime, Ext: extFor(mime, m.GetFileName()), Media: m, QuotedID: m.GetContextInfo().GetStanzaID()}
	case msg.GetStickerMessage() != nil:
		m := msg.GetStickerMessage()
		mime := baseMime(m.GetMimetype())
		return Classified{Type: "sticker", Mime: mime, Ext: extFor(mime, ""), Media: m}
	case msg.GetReactionMessage() != nil:
		m := msg.GetReactionMessage()
		return Classified{Type: "reaction", Text: m.GetText(), QuotedID: m.GetKey().GetID()}
	case msg.GetLocationMessage() != nil:
		m := msg.GetLocationMessage()
		return Classified{Type: "location", Text: fmt.Sprintf("%f,%f", m.GetDegreesLatitude(), m.GetDegreesLongitude())}
	case msg.GetContactMessage() != nil:
		return Classified{Type: "contact", Text: msg.GetContactMessage().GetDisplayName()}
	}
	return Classified{Type: "other"}
}
```

- [ ] **Step 4: Rodar**

Run: `go test ./internal/wa/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(wa): classify raw messages into storage types"
```

---

### Task 8: wa — wrapper whatsmeow

**Files:**
- Create: `internal/wa/client.go`

**Interfaces:**
- Consumes: nada do projeto além de `Classify`.
- Produces:
  ```go
  type Handler interface {
      OnMessage(evt *events.Message)              // novas + history sync
      OnGroup(jid types.JID, name string, members []types.JID)
      OnPushName(jid types.JID, name string)
      OnLoggedOut()
  }
  type Client struct{ WA *whatsmeow.Client }
  func Open(ctx context.Context, dbPath string, logger waLog.Logger) (*Client, error)   // sqlstore no mesmo data.db (tabelas whatsmeow_*)
  func (c *Client) IsPaired() bool
  func (c *Client) Connect(ctx context.Context, h Handler, showQR func(code string)) error // se !IsPaired: mostra QR e espera pareamento; se showQR==nil retorna ErrNotPaired
  func (c *Client) SendText(ctx context.Context, to types.JID, text string) (id string, err error) // + SendPresence(unavailable) depois
  func (c *Client) RequestHistory(ctx context.Context, chat types.JID, oldest *types.MessageInfo, count int) error
  func (c *Client) Download(ctx context.Context, m whatsmeow.DownloadableMessage) ([]byte, error)
  func (c *Client) Disconnect()
  var ErrNotPaired = errors.New("não pareado")
  ```

- [ ] **Step 1: Implementar**

`internal/wa/client.go`:
```go
package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

var ErrNotPaired = errors.New("não pareado: rode `claudewhats serve` para escanear o QR")

type Handler interface {
	OnMessage(evt *events.Message)
	OnGroup(jid types.JID, name string, members []types.JID)
	OnPushName(jid types.JID, name string)
	OnLoggedOut()
}

type Client struct {
	WA *whatsmeow.Client
}

func Open(ctx context.Context, dbPath string, logger waLog.Logger) (*Client, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", dbPath))
	if err != nil {
		return nil, err
	}
	container := sqlstore.NewWithDB(db, "sqlite3", logger)
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("whatsmeow store: %w", err)
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{WA: whatsmeow.NewClient(dev, logger)}, nil
}

func (c *Client) IsPaired() bool { return c.WA.Store.ID != nil }

func (c *Client) Connect(ctx context.Context, h Handler, showQR func(code string)) error {
	c.WA.AddEventHandler(func(raw any) {
		switch evt := raw.(type) {
		case *events.Message:
			h.OnMessage(evt)
		case *events.HistorySync:
			for _, conv := range evt.Data.GetConversations() {
				chat, err := types.ParseJID(conv.GetID())
				if err != nil {
					continue
				}
				for _, hm := range conv.GetMessages() {
					m, err := c.WA.ParseWebMessage(chat, hm.GetMessage())
					if err == nil {
						h.OnMessage(m)
					}
				}
			}
		case *events.GroupInfo:
			if evt.Name != nil {
				h.OnGroup(evt.JID, evt.Name.Name, nil)
			}
		case *events.JoinedGroup:
			h.OnGroup(evt.JID, evt.GroupName.Name, participantJIDs(evt.Participants))
		case *events.PushName:
			h.OnPushName(evt.JID, evt.NewPushName)
		case *events.Connected:
			_ = c.WA.SendPresence(ctx, types.PresenceUnavailable)
			go c.syncGroups(ctx, h)
		case *events.LoggedOut:
			h.OnLoggedOut()
		}
	})
	if !c.IsPaired() {
		if showQR == nil {
			return ErrNotPaired
		}
		qr, err := c.WA.GetQRChannel(ctx)
		if err != nil {
			return err
		}
		if err := c.WA.Connect(); err != nil {
			return err
		}
		for item := range qr {
			if item.Event == "code" {
				showQR(item.Code)
			} else if item.Event != "success" {
				return fmt.Errorf("pareamento: %s", item.Event)
			}
		}
		return nil
	}
	return c.WA.Connect()
}

func participantJIDs(ps []types.GroupParticipant) []types.JID {
	out := make([]types.JID, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.JID)
	}
	return out
}

func (c *Client) syncGroups(ctx context.Context, h Handler) {
	groups, err := c.WA.GetJoinedGroups(ctx)
	if err != nil {
		return
	}
	for _, g := range groups {
		h.OnGroup(g.JID, g.Name, participantJIDs(g.Participants))
	}
}

func (c *Client) SendText(ctx context.Context, to types.JID, text string) (string, error) {
	resp, err := c.WA.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return "", err
	}
	_ = c.WA.SendPresence(ctx, types.PresenceUnavailable)
	return resp.ID, nil
}

// RequestHistory asks the phone for `count` messages older than `oldest` in chat.
// The phone answers with a HistorySync event, which flows through Handler.OnMessage.
func (c *Client) RequestHistory(ctx context.Context, chat types.JID, oldest *types.MessageInfo, count int) error {
	if oldest == nil {
		oldest = &types.MessageInfo{MessageSource: types.MessageSource{Chat: chat}}
	}
	msg := c.WA.BuildHistorySyncRequest(oldest, count)
	_, err := c.WA.SendMessage(ctx, c.WA.Store.ID.ToNonAD(), msg, whatsmeow.SendRequestExtra{Peer: true})
	return err
}

func (c *Client) Download(ctx context.Context, m whatsmeow.DownloadableMessage) ([]byte, error) {
	return c.WA.Download(ctx, m)
}

func (c *Client) Disconnect() { c.WA.Disconnect() }
```

Nota ao implementar: confirme com `go doc go.mau.fi/whatsmeow/types/events.GroupInfo`, `.JoinedGroup`, `.PushName` os nomes exatos dos campos (`Name *types.GroupName`, `GroupName`, `NewPushName`) e ajuste se a versão divergir; `go build ./...` é o teste desta task.

- [ ] **Step 2: Build**

Run: `go build ./... && go vet ./internal/wa/` → ok.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat(wa): whatsmeow client wrapper with QR, send, history request"
```

---

### Task 9: Daemon — ingestão

**Files:**
- Create: `internal/daemon/ingest.go`, `internal/daemon/ingest_test.go`

**Interfaces:**
- Consumes: `store.*`, `wa.Classify`, `wa.Handler`.
- Produces:
  ```go
  type Downloader interface { Download(ctx context.Context, m whatsmeow.DownloadableMessage) ([]byte, error) }
  type Ingester struct { Store *store.Store; MediaDir string; DL Downloader; Log *log.Logger; OnLoggedOutFn func() }
  func (in *Ingester) OnMessage(evt *events.Message)   // implementa wa.Handler
  func (in *Ingester) OnGroup(...) / OnPushName(...) / OnLoggedOut()
  ```
  Regras: chat kind = `group` se `Info.IsGroup`; sender resolvido com `ResolveIdentity(sender, senderAlt, pushName)`; dm chat também registra identidade do outro lado (`Info.Chat` + `RecipientAlt`); mídia salva em `MediaDir/<chat user>/<msgid><ext>`; se `Transcribe` → `transcript_status=pending` + `EnqueueJob`; download falho → `media_path=""`, status `failed` (sem job); `raw_json` = `protojson` do `evt.Message`.

- [ ] **Step 1: Teste**

`internal/daemon/ingest_test.go`:
```go
package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrewmautone/claudewhats/internal/store"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

type fakeDL struct{ data []byte; err error }

func (f fakeDL) Download(ctx context.Context, m whatsmeow.DownloadableMessage) ([]byte, error) {
	return f.data, f.err
}

func newIngester(t *testing.T, dl Downloader) (*Ingester, *store.Store) {
	s, _ := store.OpenMemory()
	t.Cleanup(func() { s.Close() })
	return &Ingester{Store: s, MediaDir: t.TempDir(), DL: dl}, s
}

func evt(chat, sender, alt, id string, group bool, msg *waE2E.Message) *events.Message {
	c, _ := types.ParseJID(chat)
	sn, _ := types.ParseJID(sender)
	e := &events.Message{Message: msg}
	e.Info.Chat = c
	e.Info.Sender = sn
	e.Info.IsGroup = group
	e.Info.ID = id
	e.Info.PushName = "Fulano"
	e.Info.Timestamp = time.Unix(1000, 0)
	if alt != "" {
		e.Info.SenderAlt, _ = types.ParseJID(alt)
	}
	return e
}

func TestIngestTextDM(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	in.OnMessage(evt("5511888@s.whatsapp.net", "5511888@s.whatsapp.net", "999@lid", "A1", false, &waE2E.Message{Conversation: proto.String("oi")}))
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 10)
	if len(ms) != 1 || ms[0].Type != "text" || ms[0].Text != "oi" || ms[0].Sender != "Fulano" || ms[0].TranscriptStatus != "skipped" {
		t.Fatalf("%+v", ms)
	}
	if s.ContactName("999@lid") != "Fulano" {
		t.Fatal("alt jid should be linked")
	}
	chats, _ := s.ListChats(0, "")
	if len(chats) != 1 || chats[0].Kind != "dm" {
		t.Fatalf("%+v", chats)
	}
}

func TestIngestAudioInGroupDownloadsAndEnqueues(t *testing.T) {
	in, s := newIngester(t, fakeDL{data: []byte("OGG")})
	in.OnMessage(evt("123@g.us", "777@lid", "", "B1", true, &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg; codecs=opus")}}))
	ms, _ := s.ReadMessages("123@g.us", 0, 10)
	if len(ms) != 1 || ms[0].Type != "audio" || ms[0].TranscriptStatus != "pending" || ms[0].MediaMime != "audio/ogg" {
		t.Fatalf("%+v", ms)
	}
	want := filepath.Join(in.MediaDir, "123", "B1.ogg")
	if ms[0].MediaPath != want {
		t.Fatalf("path %s want %s", ms[0].MediaPath, want)
	}
	if b, _ := os.ReadFile(want); string(b) != "OGG" {
		t.Fatal("media not written")
	}
	j, ok, _ := s.NextJob(time.Now().Unix())
	if !ok || j.MsgID != "B1" {
		t.Fatal("job not enqueued")
	}
	chats, _ := s.ListChats(0, "group")
	if len(chats) != 1 {
		t.Fatal("group chat missing")
	}
}

func TestIngestDownloadFailure(t *testing.T) {
	in, s := newIngester(t, fakeDL{err: errors.New("net")})
	in.OnMessage(evt("5511888@s.whatsapp.net", "5511888@s.whatsapp.net", "", "C1", false, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}))
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 10)
	if ms[0].TranscriptStatus != "failed" || ms[0].MediaPath != "" {
		t.Fatalf("%+v", ms[0])
	}
	if _, ok, _ := s.NextJob(1 << 40); ok {
		t.Fatal("no job on download failure")
	}
}

func TestOnGroupAndPushName(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	g, _ := types.ParseJID("123@g.us")
	p, _ := types.ParseJID("5511888@s.whatsapp.net")
	in.OnGroup(g, "Família", []types.JID{p})
	in.OnPushName(p, "Maria")
	c, err := s.ResolveChat("família")
	if err != nil || c.JID != "123@g.us" {
		t.Fatal(err)
	}
	if s.ContactName("5511888@s.whatsapp.net") != "Maria" {
		t.Fatal("push name")
	}
}
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/daemon/` → FAIL.

- [ ] **Step 3: Implementar**

`internal/daemon/ingest.go`:
```go
package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/andrewmautone/claudewhats/internal/wa"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/encoding/protojson"
)

type Downloader interface {
	Download(ctx context.Context, m whatsmeow.DownloadableMessage) ([]byte, error)
}

type Ingester struct {
	Store         *store.Store
	MediaDir      string
	DL            Downloader
	Log           *log.Logger
	OnLoggedOutFn func()
}

func (in *Ingester) logf(format string, a ...any) {
	if in.Log != nil {
		in.Log.Printf(format, a...)
	}
}

func jidStr(j types.JID) string {
	if j.IsEmpty() {
		return ""
	}
	return j.ToNonAD().String()
}

func (in *Ingester) OnMessage(evt *events.Message) {
	info := evt.Info
	chat := jidStr(info.Chat)
	sender := jidStr(info.Sender)
	if chat == "" || sender == "" {
		return
	}
	kind := "dm"
	if info.IsGroup {
		kind = "group"
	}
	if err := in.Store.UpsertChat(chat, kind, ""); err != nil {
		in.logf("upsert chat: %v", err)
		return
	}
	pushName := info.PushName
	if info.IsFromMe {
		pushName = ""
	}
	if _, err := in.Store.ResolveIdentity(sender, jidStr(info.SenderAlt), pushName); err != nil {
		in.logf("resolve sender: %v", err)
	}
	if kind == "dm" && chat != sender {
		if _, err := in.Store.ResolveIdentity(chat, jidStr(info.RecipientAlt), ""); err != nil {
			in.logf("resolve chat identity: %v", err)
		}
	}
	cl := wa.Classify(evt.Message)
	m := store.Message{
		ChatJID: chat, ID: info.ID, SenderJID: sender, TS: info.Timestamp.Unix(), FromMe: info.IsFromMe,
		Type: cl.Type, Text: cl.Text, MediaMime: cl.Mime, QuotedID: cl.QuotedID, TranscriptStatus: "skipped",
	}
	if raw, err := protojson.Marshal(evt.Message); err == nil {
		m.RawJSON = string(raw)
	}
	if cl.Media != nil {
		path, err := in.saveMedia(chat, info.ID, cl)
		if err != nil {
			in.logf("download %s: %v", info.ID, err)
			m.TranscriptStatus = "failed"
		} else {
			m.MediaPath = path
			if cl.Transcribe {
				m.TranscriptStatus = "pending"
			}
		}
	}
	inserted, err := in.Store.InsertMessage(m)
	if err != nil {
		in.logf("insert %s: %v", info.ID, err)
		return
	}
	if inserted && m.TranscriptStatus == "pending" {
		if err := in.Store.EnqueueJob(chat, info.ID); err != nil {
			in.logf("enqueue: %v", err)
		}
	}
}

func (in *Ingester) saveMedia(chat, id string, cl wa.Classified) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	data, err := in.DL.Download(ctx, cl.Media)
	if err != nil {
		return "", err
	}
	user := chat
	if i := len(chat); i > 0 {
		for j := 0; j < len(chat); j++ {
			if chat[j] == '@' {
				user = chat[:j]
				break
			}
		}
	}
	dir := filepath.Join(in.MediaDir, user)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+cl.Ext)
	return path, os.WriteFile(path, data, 0o600)
}

func (in *Ingester) OnGroup(jid types.JID, name string, members []types.JID) {
	chat := jidStr(jid)
	if err := in.Store.UpsertChat(chat, "group", name); err != nil {
		in.logf("group %s: %v", chat, err)
	}
	if len(members) > 0 {
		js := make([]string, 0, len(members))
		for _, m := range members {
			js = append(js, jidStr(m))
			in.Store.ResolveIdentity(jidStr(m), "", "")
		}
		if err := in.Store.SetGroupMembers(chat, js); err != nil {
			in.logf("members %s: %v", chat, err)
		}
	}
}

func (in *Ingester) OnPushName(jid types.JID, name string) {
	if _, err := in.Store.ResolveIdentity(jidStr(jid), "", name); err != nil {
		in.logf("pushname: %v", err)
	}
}

func (in *Ingester) OnLoggedOut() {
	in.logf("LOGGED OUT: sessão encerrada pelo WhatsApp; rode `claudewhats serve` para parear de novo")
	if in.OnLoggedOutFn != nil {
		in.OnLoggedOutFn()
	}
}
```

- [ ] **Step 4: Rodar**

Run: `go test ./internal/daemon/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(daemon): ingest events into store with media and jobs"
```

---

### Task 10: Daemon — worker Gemini

**Files:**
- Create: `internal/daemon/worker.go`, `internal/daemon/worker_test.go`

**Interfaces:**
- Produces:
  ```go
  type Transcriber interface {
      Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
      Describe(ctx context.Context, image []byte, mime, caption string) (string, error)
  }
  type Worker struct{ Store *store.Store; AI Transcriber; Log *log.Logger; MaxAttempts int /*default 5*/; Poll time.Duration /*default 3s*/ }
  func (w *Worker) RunOnce(ctx context.Context) (worked bool, err error)  // processa 1 job
  func (w *Worker) Run(ctx context.Context)                                 // loop até ctx cancelar
  ```

- [ ] **Step 1: Teste**

`internal/daemon/worker_test.go`:
```go
package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrewmautone/claudewhats/internal/store"
)

type fakeAI struct {
	out string
	err error
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
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/daemon/ -run Worker` → FAIL.

- [ ] **Step 3: Implementar**

`internal/daemon/worker.go`:
```go
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
	msg, found, err := w.Store.GetMessage(job.ChatJID, job.MsgID)
	if err != nil {
		return true, err
	}
	m := &msg
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

func (w *Worker) Run(ctx context.Context) {
	poll := w.Poll
	if poll <= 0 {
		poll = 3 * time.Second
	}
	for {
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
```

- [ ] **Step 4: Rodar**

Run: `go test ./internal/daemon/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(daemon): gemini transcription worker"
```

---

### Task 11: Daemon — servidor HTTP, idle timer, Run

**Files:**
- Create: `internal/daemon/server.go`, `internal/daemon/server_test.go`

**Interfaces:**
- Produces:
  ```go
  type Sender interface {
      SendText(ctx context.Context, to types.JID, text string) (string, error)
      RequestHistory(ctx context.Context, chat types.JID, oldest *types.MessageInfo, count int) error
      IsConnected() bool
  }
  type Server struct{ Store *store.Store; WA Sender; Idle time.Duration; Log *log.Logger; Shutdown func() }
  func (s *Server) Handler() http.Handler
  // GET  /status            -> {"ok":true,"connected":bool,"messages":n,"pending_jobs":n,"pid":n}
  // POST /send   {"chat":jid,"text":..}      -> {"id":..}
  // POST /sync   {"chat":jid,"count":n}      -> {"requested":n}
  // POST /shutdown                          -> {"ok":true}
  func (s *Server) Touch()                          // renova idle
  func (s *Server) IdleLoop(ctx context.Context)    // chama Shutdown quando Idle passa sem Touch (Idle<=0: nunca)
  type StatusResponse struct{ OK, Connected bool; Messages, PendingJobs int64; PID int }
  func Run(ctx context.Context, cfg *config.Config, background bool, showQR func(string)) error // monta tudo; showQR nil em background
  ```

- [ ] **Step 1: Teste**

`internal/daemon/server_test.go`:
```go
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
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/daemon/ -run 'Server|Idle'` → FAIL.

- [ ] **Step 3: Implementar**

`internal/daemon/server.go`:
```go
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/gemini"
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/andrewmautone/claudewhats/internal/wa"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type Sender interface {
	SendText(ctx context.Context, to types.JID, text string) (string, error)
	RequestHistory(ctx context.Context, chat types.JID, oldest *types.MessageInfo, count int) error
	IsConnected() bool
}

type StatusResponse struct {
	OK          bool  `json:"ok"`
	Connected   bool  `json:"connected"`
	Messages    int64 `json:"messages"`
	PendingJobs int64 `json:"pending_jobs"`
	PID         int   `json:"pid"`
}

type Server struct {
	Store    *store.Store
	WA       Sender
	Idle     time.Duration
	Log      *log.Logger
	Shutdown func()

	mu       sync.Mutex
	lastSeen time.Time
}

func (s *Server) Touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

func (s *Server) IdleLoop(ctx context.Context) {
	if s.Idle <= 0 {
		<-ctx.Done()
		return
	}
	s.Touch()
	tick := time.NewTicker(s.Idle / 4)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.mu.Lock()
			idle := time.Since(s.lastSeen)
			s.mu.Unlock()
			if idle >= s.Idle {
				if s.Log != nil {
					s.Log.Printf("idle %s, encerrando", idle.Round(time.Second))
				}
				s.Shutdown()
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		s.Touch()
		msgs, jobs, _ := s.Store.Stats()
		writeJSON(w, 200, StatusResponse{OK: true, Connected: s.WA != nil && s.WA.IsConnected(), Messages: msgs, PendingJobs: jobs, PID: os.Getpid()})
	})
	mux.HandleFunc("POST /send", func(w http.ResponseWriter, r *http.Request) {
		s.Touch()
		var req struct{ Chat, Text string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Chat == "" || req.Text == "" {
			writeJSON(w, 400, map[string]string{"error": "chat e text são obrigatórios"})
			return
		}
		jid, err := types.ParseJID(req.Chat)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		id, err := s.WA.SendText(r.Context(), jid, req.Text)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"id": id})
	})
	mux.HandleFunc("POST /sync", func(w http.ResponseWriter, r *http.Request) {
		s.Touch()
		var req struct {
			Chat  string
			Count int
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Chat == "" {
			writeJSON(w, 400, map[string]string{"error": "chat é obrigatório"})
			return
		}
		if req.Count <= 0 {
			req.Count = 50
		}
		jid, err := types.ParseJID(req.Chat)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		var oldest *types.MessageInfo
		if m, ok, _ := s.Store.OldestMessage(req.Chat); ok {
			sender, _ := types.ParseJID(m.SenderJID)
			oldest = &types.MessageInfo{ID: m.ID, Timestamp: time.Unix(m.TS, 0), MessageSource: types.MessageSource{Chat: jid, Sender: sender, IsFromMe: m.FromMe, IsGroup: jid.Server == types.GroupServer}}
		}
		if err := s.WA.RequestHistory(r.Context(), jid, oldest, req.Count); err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]int{"requested": req.Count})
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
		go s.Shutdown()
	})
	return mux
}

type waConn struct{ *wa.Client }

func (c waConn) IsConnected() bool { return c.WA.IsConnected() }

// Run wires store, whatsmeow, ingester, worker and HTTP; blocks until ctx is done or shutdown.
func Run(ctx context.Context, cfg *config.Config, background bool, showQR func(string)) error {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if background {
		f, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		logger = log.New(f, "", log.LstdFlags)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("porta %d ocupada (outro daemon rodando?): %w", cfg.Port, err)
	}
	defer ln.Close()
	os.WriteFile(cfg.PidPath, []byte(strconv.Itoa(os.Getpid())), 0o600)
	defer os.Remove(cfg.PidPath)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	client, err := wa.Open(ctx, cfg.DBPath, waLog.Stdout("WA", "WARN", false))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopOnce := sync.Once{}
	shutdown := func() { stopOnce.Do(cancel) }

	ing := &Ingester{Store: st, MediaDir: cfg.MediaDir, DL: client, Log: logger, OnLoggedOutFn: shutdown}
	if err := client.Connect(ctx, ing, showQR); err != nil {
		if errors.Is(err, wa.ErrNotPaired) {
			logger.Print(err)
		}
		return err
	}
	defer client.Disconnect()
	logger.Printf("conectado, pid %d, porta %d", os.Getpid(), cfg.Port)

	worker := &Worker{Store: st, AI: gemini.New(cfg.GeminiAPIKey, cfg.GeminiModel), Log: logger}
	go worker.Run(ctx)

	srv := &Server{Store: st, WA: waConn{client}, Log: logger, Shutdown: shutdown}
	if background {
		srv.Idle = cfg.IdleTimeout
	}
	go srv.IdleLoop(ctx)
	hs := &http.Server{Handler: srv.Handler()}
	go hs.Serve(ln)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
	case <-ctx.Done():
	}
	logger.Print("encerrando")
	hs.Close()
	return nil
}
```

- [ ] **Step 4: Rodar + build**

Run: `go test ./internal/daemon/ && go build ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(daemon): loopback http api, idle shutdown and Run wiring"
```

---

### Task 12: Daemon — cliente + auto-spawn

**Files:**
- Create: `internal/daemon/client.go`, `internal/daemon/spawn_windows.go`, `internal/daemon/spawn_unix.go`, `internal/daemon/client_test.go`

**Interfaces:**
- Produces:
  ```go
  type Client struct{ BaseURL string; HTTP *http.Client }
  func NewClient(port int) *Client
  func (c *Client) Status(ctx) (StatusResponse, error)
  func (c *Client) Send(ctx, chatJID, text string) (id string, err error)
  func (c *Client) Sync(ctx, chatJID string, count int) error
  func (c *Client) Shutdown(ctx) error
  func Spawn(cfg *config.Config) error                 // exec.Command(self, "serve", "--background") detached; sem janela
  func EnsureRunning(ctx, cfg, wait time.Duration) (*Client, error)  // Status ok? senão Spawn e poll até wait; erro inclui últimas 5 linhas do daemon.log
  func Kick(cfg *config.Config)                        // fire-and-forget: se Status falha, Spawn; ignora erros
  ```

- [ ] **Step 1: Teste**

`internal/daemon/client_test.go`:
```go
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
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/daemon/ -run Client|Ensure` → FAIL.

- [ ] **Step 3: Implementar**

`internal/daemon/client.go`:
```go
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(port int) *Client {
	return &Client{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		json.Unmarshal(rb, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(rb))
		}
		return fmt.Errorf("daemon %d: %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		return json.Unmarshal(rb, out)
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	var st StatusResponse
	err := c.do(ctx, "GET", "/status", nil, &st)
	return st, err
}

func (c *Client) Send(ctx context.Context, chatJID, text string) (string, error) {
	var out struct{ ID string }
	err := c.do(ctx, "POST", "/send", map[string]string{"chat": chatJID, "text": text}, &out)
	return out.ID, err
}

func (c *Client) Sync(ctx context.Context, chatJID string, count int) error {
	return c.do(ctx, "POST", "/sync", map[string]any{"chat": chatJID, "count": count}, nil)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, "POST", "/shutdown", nil, nil)
}

var spawnFn = Spawn

// Spawn starts `claudewhats serve --background` detached from this process.
func Spawn(cfg *config.Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "serve", "--background")
	cmd.Env = append(os.Environ(), "CLAUDEWHATS_HOME="+cfg.Home)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func logTail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// EnsureRunning returns a client to a live daemon, spawning one when needed.
func EnsureRunning(ctx context.Context, cfg *config.Config, wait time.Duration) (*Client, error) {
	c := NewClient(cfg.Port)
	probe := func() bool {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, err := c.Status(pctx)
		return err == nil
	}
	if probe() {
		return c, nil
	}
	if err := spawnFn(cfg); err != nil {
		return nil, fmt.Errorf("não consegui iniciar o daemon: %w", err)
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if probe() {
			return c, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	msg := "daemon não subiu a tempo"
	if tail := logTail(cfg.LogPath, 5); tail != "" {
		msg += "; fim do daemon.log:\n" + tail
	}
	return nil, errors.New(msg)
}

// Kick nudges the daemon to life without waiting; errors are ignored.
func Kick(cfg *config.Config) {
	c := NewClient(cfg.Port)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.Status(ctx); err == nil {
		return
	}
	_ = spawnFn(cfg)
}
```

`internal/daemon/spawn_windows.go`:
```go
//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

const (
	createNoWindow  = 0x08000000
	detachedProcess = 0x00000008
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}
```

`internal/daemon/spawn_unix.go`:
```go
//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
```

- [ ] **Step 4: Rodar**

Run: `go test ./internal/daemon/ && GOOS=linux go vet ./internal/daemon/` → PASS (o vet cross-OS garante que o arquivo unix compila).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(daemon): http client, detached spawn and EnsureRunning"
```

---

### Task 13: CLI — leitura (`chats`, `read`, `search`, `contact`, `status`)

**Files:**
- Create: `internal/cli/common.go`, `internal/cli/chats.go`, `internal/cli/read.go`, `internal/cli/search.go`, `internal/cli/contact.go`, `internal/cli/status.go`, `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: `config.Load`, `store.*`, `daemon.NewClient/Kick`.
- Produces (em `common.go`): `openStore() (*config.Config, *store.Store, error)`, `parseSince(s string) (int64, error)` (`24h`, `7d`, `30m`, `0`/`""` = 0, ou `2026-09-21`), `fmtTS(int64) string` (`2006-01-02 15:04` local), `noSpawn bool` flag persistente, `kick(cfg)` = `if !noSpawn { daemon.Kick(cfg) }`. Testes chamam `runWith(s *store.Store, args ...string) (string, error)` que executa o root com um store injetado (`storeOverride`).

- [ ] **Step 1: Teste**

`internal/cli/cli_test.go`:
```go
package cli

import (
	"bytes"
	"encoding/json"
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
	if !strings.Contains(out, "(image) [transcrição pendente]: olha") {
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
	if !strings.Contains(out, "m2") && !strings.Contains(out, "bolo") {
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
```

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/cli/` → FAIL.

- [ ] **Step 3: Implementar**

`internal/cli/common.go`:
```go
package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/andrewmautone/claudewhats/internal/store"
)

var (
	noSpawn       bool
	storeOverride *store.Store
	out           io.Writer = os.Stdout
)

func init() {
	root.PersistentFlags().BoolVar(&noSpawn, "no-spawn", false, "não acorda o daemon em background")
}

func openStore() (*config.Config, *store.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if storeOverride != nil {
		return cfg, storeOverride, nil
	}
	s, err := store.Open(cfg.DBPath)
	return cfg, s, err
}

func closeStore(s *store.Store) {
	if s != storeOverride {
		s.Close()
	}
}

func kick(cfg *config.Config) {
	if !noSpawn && storeOverride == nil {
		daemon.Kick(cfg)
	}
}

// parseSince accepts 24h / 7d / 30m / YYYY-MM-DD / "" (=0).
func parseSince(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.Unix(), nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("since inválido: %q", s)
		}
		return time.Now().Add(-time.Duration(n) * 24 * time.Hour).Unix(), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("since inválido: %q (use 24h, 7d, 30m ou 2026-09-21)", s)
	}
	return time.Now().Add(-d).Unix(), nil
}

func fmtTS(ts int64) string { return time.Unix(ts, 0).Local().Format("2006-01-02 15:04") }

func printf(format string, a ...any) { fmt.Fprintf(out, format, a...) }

// runWith executes the CLI against an injected store, capturing stdout (tests).
func runWith(s *store.Store, args ...string) (string, error) {
	var buf bytes.Buffer
	prev, prevStore := out, storeOverride
	out, storeOverride = &buf, s
	defer func() { out, storeOverride = prev, prevStore }()
	jsonOut, noSpawn = false, false
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}
```

Ajuste em `root.go`: `emit` deve escrever em `out` (não `os.Stdout`): `enc := json.NewEncoder(out)`.

`internal/cli/chats.go`:
```go
package cli

import (
	"github.com/spf13/cobra"
)

func init() {
	var since, kind string
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "Lista conversas (do banco local)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			sv, err := parseSince(since)
			if err != nil {
				return err
			}
			chats, err := s.ListChats(sv, kind)
			if err != nil {
				return err
			}
			kick(cfg)
			return emit(chats, func() {
				for _, c := range chats {
					printf("%-6s %-40s %5d msgs  último: %s  %s\n", c.Kind, c.Name, c.Count, fmtTS(c.LastMsgAt), c.JID)
				}
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "janela: 24h, 7d, 2026-09-21")
	cmd.Flags().StringVar(&kind, "kind", "", "dm ou group")
	root.AddCommand(cmd)
}
```

`internal/cli/read.go`:
```go
package cli

import (
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/spf13/cobra"
)

func printMessages(msgs []store.Message) {
	for _, m := range msgs {
		body := m.Text
		switch {
		case m.Transcript != "":
			body = m.Transcript
			if m.Text != "" {
				body += " [legenda: " + m.Text + "]"
			}
		case m.TranscriptStatus == "pending":
			body = "[transcrição pendente]: " + m.Text
		case m.TranscriptStatus == "failed":
			body = "[transcrição falhou]: " + m.Text
		}
		printf("%s %s (%s): %s\n", fmtTS(m.TS), m.Sender, m.Type, body)
	}
}

func init() {
	var since string
	var limit int
	cmd := &cobra.Command{
		Use:   "read <chat>",
		Short: "Mensagens de uma conversa (jid, número, nome de contato ou de grupo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			chat, err := s.ResolveChat(args[0])
			if err != nil {
				return err
			}
			sv, err := parseSince(since)
			if err != nil {
				return err
			}
			msgs, err := s.ReadMessages(chat.JID, sv, limit)
			if err != nil {
				return err
			}
			kick(cfg)
			return emit(msgs, func() {
				printf("# %s (%s)\n", chat.Name, chat.JID)
				printMessages(msgs)
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "janela: 24h, 7d, 2026-09-21")
	cmd.Flags().IntVar(&limit, "limit", 200, "máximo de mensagens (as mais recentes)")
	root.AddCommand(cmd)
}
```

`internal/cli/search.go`:
```go
package cli

import "github.com/spf13/cobra"

func init() {
	var since, chat string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <texto>",
		Short: "Busca full-text em texto e transcrições",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			chatJID := ""
			if chat != "" {
				c, err := s.ResolveChat(chat)
				if err != nil {
					return err
				}
				chatJID = c.JID
			}
			sv, err := parseSince(since)
			if err != nil {
				return err
			}
			q := ""
			for i, a := range args {
				if i > 0 {
					q += " "
				}
				q += a
			}
			msgs, err := s.Search(q, chatJID, sv, limit)
			if err != nil {
				return err
			}
			kick(cfg)
			return emit(msgs, func() {
				for _, m := range msgs {
					printf("[%s] ", s.ContactName(m.ChatJID))
					printMessages([]store.Message{m})
				}
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "janela")
	cmd.Flags().StringVar(&chat, "chat", "", "limitar a um chat")
	cmd.Flags().IntVar(&limit, "limit", 50, "máximo de resultados")
	root.AddCommand(cmd)
}
```
(importar `store` em search.go.)

`internal/cli/contact.go`:
```go
package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	contact := &cobra.Command{Use: "contact", Short: "Contatos e vínculo de identidades"}

	add := &cobra.Command{
		Use: "add <nome> <numero>", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			id, err := s.AddContact(args[0], args[1])
			if err != nil {
				return err
			}
			return emit(map[string]any{"id": id, "name": args[0]}, func() { printf("contato #%d %s\n", id, args[0]) })
		},
	}
	var q string
	list := &cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			cs, err := s.ListContacts(q)
			if err != nil {
				return err
			}
			return emit(cs, func() {
				for _, c := range cs {
					name := c.Name
					if name == "" {
						name = "(sem nome)"
					}
					printf("#%-4d %-30s %s\n", c.ID, name, strings.Join(c.JIDs, " "))
				}
			})
		},
	}
	list.Flags().StringVar(&q, "q", "", "filtro por nome ou jid")
	link := &cobra.Command{
		Use: "link <a> <b>", Short: "Junta duas identidades/contatos num só", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			id, err := s.LinkContacts(args[0], args[1])
			if err != nil {
				return err
			}
			return emit(map[string]any{"id": id}, func() { printf("vinculados no contato #%d\n", id) })
		},
	}
	rename := &cobra.Command{
		Use: "rename <ref> <nome>", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			if err := s.RenameContact(args[0], args[1]); err != nil {
				return err
			}
			return emit(map[string]any{"ok": true}, func() { printf("renomeado\n") })
		},
	}
	contact.AddCommand(add, list, link, rename)
	root.AddCommand(contact)
}
```

`internal/cli/status.go`:
```go
package cli

import (
	"context"
	"time"

	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Estado do banco e do daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			msgs, jobs, err := s.Stats()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			st, derr := daemon.NewClient(cfg.Port).Status(ctx)
			res := map[string]any{"messages": msgs, "pending_jobs": jobs, "daemon_running": derr == nil, "connected": st.Connected, "pid": st.PID, "home": cfg.Home}
			return emit(res, func() {
				printf("mensagens: %d  jobs pendentes: %d\n", msgs, jobs)
				if derr != nil {
					printf("daemon: parado\n")
				} else {
					printf("daemon: rodando (pid %d), whatsapp conectado: %v\n", st.PID, st.Connected)
				}
			})
		},
	})
}
```

- [ ] **Step 4: Rodar**

Run: `go test ./internal/cli/ && go build ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(cli): chats, read, search, contact and status commands"
```

---

### Task 14: CLI — `summary`, `send`, `sync`, `serve`, `stop`

**Files:**
- Create: `internal/cli/summary.go`, `internal/cli/send.go`, `internal/cli/sync.go`, `internal/cli/serve.go`, `internal/cli/stop.go`
- Modify: `internal/cli/cli_test.go` (adicionar teste do summary com Gemini fake)

**Interfaces:**
- Consumes: `gemini.New`, `daemon.EnsureRunning`, `daemon.Run`, `daemon.NewClient`.
- Produces: `summarizerOverride` (var de teste, tipo `interface{ Summarize(ctx, instr, text string) (string, error) }`), `buildTranscript(s, chat, since, limit) string` (texto plano `[ts] sender: body` por linha, usado pelo summary).

- [ ] **Step 1: Teste**

Adicionar em `internal/cli/cli_test.go`:
```go
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
	out := run(t, s, "summary", "--no-spawn")
	if !strings.Contains(out, "## Família") || !strings.Contains(out, "## Maria") || !strings.Contains(out, "## Geral") {
		t.Fatal(out)
	}
	if len(f.got) != 3 { // 2 chats + geral
		t.Fatalf("calls %d", len(f.got))
	}
	if !strings.Contains(f.got[0], "vou levar o bolo") && !strings.Contains(f.got[1], "vou levar o bolo") {
		t.Fatal("transcript must be in summary input")
	}
	out = run(t, s, "summary", "--chat", "maria", "--no-spawn", "--json")
	if !strings.Contains(out, `"summary"`) {
		t.Fatal(out)
	}
}
```
(adicionar imports `context`, `strconv`.)

- [ ] **Step 2: Rodar, ver falhar**

Run: `go test ./internal/cli/ -run Summary` → FAIL.

- [ ] **Step 3: Implementar**

`internal/cli/summary.go`:
```go
package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/andrewmautone/claudewhats/internal/gemini"
	"github.com/andrewmautone/claudewhats/internal/store"
	"github.com/spf13/cobra"
)

type summarizer interface {
	Summarize(ctx context.Context, instructions, text string) (string, error)
}

var summarizerOverride summarizer

const chatPrompt = "Resuma esta conversa de WhatsApp em português, em tópicos curtos: assuntos, decisões, pedidos e pendências. Cite quem falou quando importar. Se não houver nada relevante, diga 'nada relevante'."
const overallPrompt = "Estes são resumos de várias conversas de WhatsApp. Faça um resumo geral em português destacando o que exige ação ou resposta, em ordem de importância."

func buildTranscript(s *store.Store, msgs []store.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		body := m.Text
		if m.Transcript != "" {
			body = fmt.Sprintf("(%s) %s", m.Type, m.Transcript)
			if m.Text != "" {
				body += " [legenda: " + m.Text + "]"
			}
		} else if m.Type != "text" {
			body = fmt.Sprintf("(%s) %s", m.Type, m.Text)
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", fmtTS(m.TS), m.Sender, body)
	}
	return strings.TrimSpace(b.String())
}

type chatSummary struct {
	Chat    string `json:"chat"`
	JID     string `json:"jid"`
	Count   int    `json:"count"`
	Summary string `json:"summary"`
}

func init() {
	var since, chat string
	var limit int
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Resume conversas do período com Gemini",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			defer closeStore(s)
			sv, err := parseSince(since)
			if err != nil {
				return err
			}
			var ai summarizer = summarizerOverride
			if ai == nil {
				ai = gemini.New(cfg.GeminiAPIKey, cfg.GeminiModel)
			}
			var chats []store.Chat
			if chat != "" {
				c, err := s.ResolveChat(chat)
				if err != nil {
					return err
				}
				chats = []store.Chat{c}
			} else if chats, err = s.ListChats(sv, ""); err != nil {
				return err
			}
			ctx := context.Background()
			var results []chatSummary
			for _, c := range chats {
				msgs, err := s.ReadMessages(c.JID, sv, limit)
				if err != nil {
					return err
				}
				if len(msgs) == 0 {
					continue
				}
				sum, err := ai.Summarize(ctx, chatPrompt, buildTranscript(s, msgs))
				if err != nil {
					return fmt.Errorf("%s: %w", c.Name, err)
				}
				results = append(results, chatSummary{Chat: c.Name, JID: c.JID, Count: len(msgs), Summary: sum})
			}
			overall := ""
			if chat == "" && len(results) > 1 {
				var b strings.Builder
				for _, r := range results {
					fmt.Fprintf(&b, "## %s (%d msgs)\n%s\n\n", r.Chat, r.Count, r.Summary)
				}
				if overall, err = ai.Summarize(ctx, overallPrompt, b.String()); err != nil {
					return err
				}
			}
			kick(cfg)
			return emit(map[string]any{"chats": results, "overall": overall}, func() {
				for _, r := range results {
					printf("## %s (%d msgs)\n%s\n\n", r.Chat, r.Count, r.Summary)
				}
				if overall != "" {
					printf("## Geral\n%s\n", overall)
				}
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "24h", "janela: 24h, 7d, 2026-09-21")
	cmd.Flags().StringVar(&chat, "chat", "", "só este chat")
	cmd.Flags().IntVar(&limit, "limit", 500, "máximo de mensagens por chat")
	root.AddCommand(cmd)
}
```

`internal/cli/send.go`:
```go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	var yes bool
	cmd := &cobra.Command{
		Use:   "send <chat> <texto>",
		Short: "Envia texto (pede confirmação sem --yes)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			chat, err := s.ResolveChat(args[0])
			closeStore(s)
			if err != nil {
				return err
			}
			text := strings.Join(args[1:], " ")
			if !yes {
				fmt.Fprintf(os.Stderr, "Enviar para %s (%s):\n%s\nConfirma? [s/N] ", chat.Name, chat.JID, text)
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if l := strings.ToLower(strings.TrimSpace(line)); l != "s" && l != "sim" && l != "y" {
					return fmt.Errorf("cancelado")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cl, err := daemon.EnsureRunning(ctx, cfg, 20*time.Second)
			if err != nil {
				return err
			}
			id, err := cl.Send(ctx, chat.JID, text)
			if err != nil {
				return err
			}
			return emit(map[string]string{"id": id, "chat": chat.JID}, func() { printf("enviado (%s)\n", id) })
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "não pedir confirmação")
	root.AddCommand(cmd)
}
```

`internal/cli/sync.go`:
```go
package cli

import (
	"context"
	"time"

	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	var count int
	cmd := &cobra.Command{
		Use:   "sync <chat>",
		Short: "Pede ao WhatsApp mensagens antigas desta conversa",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, s, err := openStore()
			if err != nil {
				return err
			}
			chat, err := s.ResolveChat(args[0])
			closeStore(s)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cl, err := daemon.EnsureRunning(ctx, cfg, 20*time.Second)
			if err != nil {
				return err
			}
			if err := cl.Sync(ctx, chat.JID, count); err != nil {
				return err
			}
			return emit(map[string]any{"chat": chat.JID, "requested": count}, func() {
				printf("pedido enviado ao celular; as mensagens chegam no banco em alguns segundos (rode `read` de novo)\n")
			})
		},
	}
	cmd.Flags().IntVar(&count, "count", 50, "quantas mensagens antigas pedir")
	root.AddCommand(cmd)
}
```

`internal/cli/serve.go`:
```go
package cli

import (
	"context"
	"os"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
)

func init() {
	var background bool
	var idle time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Roda o daemon (foreground mostra QR na primeira vez)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("idle") {
				cfg.IdleTimeout = idle
			}
			var showQR func(string)
			if !background {
				showQR = func(code string) {
					printf("Escaneie no WhatsApp > Aparelhos conectados:\n")
					qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
				}
			}
			return daemon.Run(context.Background(), cfg, background, showQR)
		},
	}
	cmd.Flags().BoolVar(&background, "background", false, "modo detached: log em arquivo, idle timeout")
	cmd.Flags().DurationVar(&idle, "idle", 30*time.Minute, "encerra após este tempo sem comandos (0 = nunca; só com --background)")
	root.AddCommand(cmd)
}
```

`internal/cli/stop.go`:
```go
package cli

import (
	"context"
	"time"

	"github.com/andrewmautone/claudewhats/internal/config"
	"github.com/andrewmautone/claudewhats/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Encerra o daemon em background",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := daemon.NewClient(cfg.Port).Shutdown(ctx); err != nil {
				return emit(map[string]bool{"stopped": false}, func() { printf("daemon já estava parado\n") })
			}
			return emit(map[string]bool{"stopped": true}, func() { printf("daemon encerrado\n") })
		},
	})
}
```

- [ ] **Step 4: Rodar tudo + build**

Run: `go test ./... && go build -o claudewhats.exe ./cmd/claudewhats && ./claudewhats.exe --help` → PASS, lista todos os comandos.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(cli): summary, send, sync, serve and stop"
```

---

### Task 15: Skill + README + smoke manual

**Files:**
- Create: `skill/SKILL.md`, `README.md`

- [ ] **Step 1: SKILL.md**

`skill/SKILL.md`:
```markdown
---
name: claudewhats
description: Lê, busca, resume e envia mensagens de WhatsApp do usuário via o CLI `claudewhats` (banco local SQLite + transcrição Gemini). Use quando o usuário pedir para ver conversas, resumir o zap, achar uma mensagem, ver o que alguém mandou, puxar histórico de um chat, mandar mensagem no WhatsApp, ou cadastrar/vincular contatos.
---

# ClaudeWhats

Tudo é lido do banco local; nada aqui abre janela. Sempre use `--json` para parsear.

## Comandos

- `claudewhats status --json` — daemon rodando? conectado? quantas msgs.
- `claudewhats chats --since 7d --json` — conversas com atividade (kind dm|group, count, jid).
- `claudewhats read "<chat>" --since 24h --limit 200 --json` — `<chat>` = jid, número, nome do contato ou do grupo. Cada msg tem `type` (text|audio|image|...) e `transcript` (áudio transcrito / imagem descrita). `transcript_status` pending = Gemini ainda não processou.
- `claudewhats search "<texto>" [--chat X] [--since 7d] --json` — full-text em texto+transcrições.
- `claudewhats summary [--since 24h] [--chat X] --json` — Gemini resume por chat + geral. Para muitos chats prefira isto a ler tudo.
- `claudewhats sync "<chat>" --count 50` — pede histórico antigo ao celular; espere ~10s e rode `read` de novo.
- `claudewhats send "<chat>" "<texto>" --yes` — envia texto. **Só com pedido explícito do usuário, e confirme o chat e o texto com ele antes de usar `--yes`.**
- `claudewhats contact add "<nome>" "<numero>"` / `contact list --json` / `contact link <a> <b>` / `contact rename <ref> <nome>`.

## Regras

1. Leitura primeiro: `chats` → `read`/`search`/`summary`. Nunca peça ao usuário para abrir terminal do daemon; ele sobe sozinho em background.
2. Se um comando erra com "não pareado", diga ao usuário para rodar `claudewhats serve` uma vez e escanear o QR.
3. `contact link` só quando o usuário pedir explicitamente para juntar duas identidades (ex.: "o 55...@lid do grupo é a Maria").
4. Se `transcript_status` vier `pending` em muitas mensagens, avise que a transcrição ainda está rodando e ofereça tentar de novo.
5. Ambiguidade no `<chat>` volta erro listando candidatos: pergunte ao usuário qual.
```

- [ ] **Step 2: README.md**

```markdown
# ClaudeWhats

Arquivo local do seu WhatsApp (SQLite) com transcrição de áudio/imagem via Gemini e CLI feita para o Claude.

## Setup
1. `go build -o claudewhats.exe ./cmd/claudewhats` e coloque no PATH.
2. `~/.claudewhats/config.yaml`: `gemini_api_key: ...` (ou env `GEMINI_API_KEY`).
3. `claudewhats serve` → escaneie o QR (WhatsApp > Aparelhos conectados). Ctrl+C depois de "conectado".
4. Pronto: `claudewhats chats`, `read`, `search`, `summary`, `send`, `sync`, `contact`.

O daemon sobe sozinho em background quando um comando precisa e encerra após 30 min ocioso (`idle_timeout` no config). Log em `~/.claudewhats/daemon.log`; `claudewhats stop` encerra.

## Skill
Copie `skill/` para `~/.claude/skills/claudewhats/`.
```

- [ ] **Step 3: Smoke manual (Andrew)**

1. `go build -o claudewhats.exe ./cmd/claudewhats`
2. `./claudewhats.exe serve` → QR → parear com uma conta de teste → esperar "conectado" → mandar um texto e um áudio pra essa conta → Ctrl+C.
3. `./claudewhats.exe chats` e `read <numero>` → ver `type` audio e transcrição.
4. `./claudewhats.exe send <numero> "teste" --yes` sem daemon rodando → deve subir em background sem janela (checar `daemon.log`, `status`).
5. `./claudewhats.exe sync <numero> --count 20` → `read` após 10s mostra histórico.
6. Registrar no OptMem o resultado.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "docs: skill and readme"
```
