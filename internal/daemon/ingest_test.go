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

type fakeDL struct {
	data []byte
	err  error
}

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

func TestIngestMediaPathTraversalIsSanitized(t *testing.T) {
	in, s := newIngester(t, fakeDL{data: []byte("PDF")})
	outside := filepath.Join(filepath.Dir(in.MediaDir), "evil")
	doc := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: proto.String("application/pdf"), FileName: proto.String(`a.\..\evil`)}}
	in.OnMessage(evt("5511888@s.whatsapp.net", "5511888@s.whatsapp.net", "", `..\..\x`, false, doc))
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 10)
	if len(ms) != 1 || ms[0].MediaPath == "" {
		t.Fatalf("%+v", ms)
	}
	rel, err := filepath.Rel(in.MediaDir, ms[0].MediaPath)
	if err != nil || rel != filepath.Join("5511888", "_.._x._evil") {
		t.Fatalf("media escaped MediaDir: %s (rel %s)", ms[0].MediaPath, rel)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("file written outside MediaDir")
	}
	if b, _ := os.ReadFile(ms[0].MediaPath); string(b) != "PDF" {
		t.Fatalf("media not written at %s", ms[0].MediaPath)
	}
	if got := safeName(`..\..\x`); got != "_.._x" {
		t.Fatalf("safeName id: %q", got)
	}
	if got := safeName("B1.ogg"); got != "B1.ogg" {
		t.Fatalf("safeName plain: %q", got)
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

func TestIngestLIDDMLandsInPNChat(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	// incoming: chat == sender == LID, SenderAlt carries the phone number
	in.OnMessage(evt("999@lid", "999@lid", "5511888@s.whatsapp.net", "L1", false, &waE2E.Message{Conversation: proto.String("oi")}))
	// from-me: chat is the recipient LID, RecipientAlt carries the phone number
	e := evt("999@lid", "5511000@s.whatsapp.net", "", "L2", false, &waE2E.Message{Conversation: proto.String("olá")})
	e.Info.IsFromMe = true
	e.Info.RecipientAlt, _ = types.ParseJID("5511888@s.whatsapp.net")
	e.Info.Timestamp = time.Unix(1001, 0)
	in.OnMessage(e)
	// LID with no alt at all: falls back to the PN already bound to the contact
	e = evt("999@lid", "999@lid", "", "L3", false, &waE2E.Message{Conversation: proto.String("de novo")})
	e.Info.Timestamp = time.Unix(1002, 0)
	in.OnMessage(e)
	chats, _ := s.ListChats(0, "")
	if len(chats) != 1 || chats[0].JID != "5511888@s.whatsapp.net" {
		t.Fatalf("expected a single PN chat: %+v", chats)
	}
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 10)
	if len(ms) != 3 || ms[0].ID != "L1" || ms[1].ID != "L2" || !ms[1].FromMe || ms[2].ID != "L3" {
		t.Fatalf("%+v", ms)
	}
	if c, err := s.ResolveChat("fulano"); err != nil || c.JID != "5511888@s.whatsapp.net" {
		t.Fatalf("resolve by push name: %+v %v", c, err)
	}
}

func TestIngestSkipsStatusAndNewsletter(t *testing.T) {
	in, s := newIngester(t, fakeDL{data: []byte("IMG")})
	in.OnMessage(evt("status@broadcast", "5511888@s.whatsapp.net", "", "S1", false, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}}))
	in.OnMessage(evt("120363@newsletter", "120363@newsletter", "", "N1", false, &waE2E.Message{Conversation: proto.String("canal")}))
	if chats, _ := s.ListChats(0, ""); len(chats) != 0 {
		t.Fatalf("status/newsletter must not create chats: %+v", chats)
	}
	if n, _, _ := s.Stats(); n != 0 {
		t.Fatalf("stored %d messages", n)
	}
	if _, ok, _ := s.NextJob(1 << 40); ok {
		t.Fatal("no gemini job for status media")
	}
	if entries, _ := os.ReadDir(in.MediaDir); len(entries) != 0 {
		t.Fatal("no media should be downloaded")
	}
}

func TestOnContactNamesOnlyAutoContacts(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	p, _ := types.ParseJID("5511888@s.whatsapp.net")
	in.OnPushName(p, "maria zap")
	in.OnContact(p, "Maria Silva")
	if s.ContactName("5511888@s.whatsapp.net") != "Maria Silva" {
		t.Fatal("address-book name should replace push name on auto contacts")
	}
	s.RenameContact("5511888@s.whatsapp.net", "Mãe")
	in.OnContact(p, "Maria Silva")
	if s.ContactName("5511888@s.whatsapp.net") != "Mãe" {
		t.Fatal("manual name must survive contact sync")
	}
}
