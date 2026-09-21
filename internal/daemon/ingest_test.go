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
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 0, 10)
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
	ms, _ := s.ReadMessages("123@g.us", 0, 0, 10)
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
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 0, 10)
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
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 0, 10)
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
	ms, _ := s.ReadMessages("5511888@s.whatsapp.net", 0, 0, 10)
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

func TestOnChatNameGroupAndDM(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	g, _ := types.ParseJID("123@g.us")
	p, _ := types.ParseJID("5511888@s.whatsapp.net")
	in.OnChatName(g, "Trabalho")
	in.OnChatName(p, "Amor")
	c, err := s.ResolveChat("trabalho")
	if err != nil || c.JID != "123@g.us" || c.Kind != "group" {
		t.Fatalf("group chat name: %v %+v", err, c)
	}
	if s.ContactName("5511888@s.whatsapp.net") != "Amor" {
		t.Fatal("dm chat name should become the contact name")
	}
	// the DM's name doubles as push name only while none was seen
	in.OnPushName(p, "amor zap")
	s.RenameContact("5511888@s.whatsapp.net", "")
	in.OnChatName(p, "Amor")
	if s.ContactName("5511888@s.whatsapp.net") != "amor zap" {
		t.Fatal("chat name must not overwrite a real push name")
	}
	// a manual name wins
	s.RenameContact("5511888@s.whatsapp.net", "Mãe")
	in.OnChatName(p, "Amor")
	if s.ContactName("5511888@s.whatsapp.net") != "Mãe" {
		t.Fatal("manual name must survive history sync")
	}
	// with no push name yet, the chat name fills it in
	q, _ := types.ParseJID("5511777@s.whatsapp.net")
	in.OnChatName(q, "Zé")
	s.RenameContact("5511777@s.whatsapp.net", "")
	if s.ContactName("5511777@s.whatsapp.net") != "Zé" {
		t.Fatal("chat name should seed an empty push name")
	}
}

func TestOnContactNameResolvesDMChat(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	in.OnMessage(evt("5511888@s.whatsapp.net", "5511888@s.whatsapp.net", "", "m1", false,
		&waE2E.Message{Conversation: proto.String("oi")}))
	p, _ := types.ParseJID("5511888@s.whatsapp.net")
	in.OnContact(p, "Amor")
	c, err := s.ResolveChat("amor")
	if err != nil || c.JID != "5511888@s.whatsapp.net" {
		t.Fatalf("resolve by contact name: %v %+v", err, c)
	}
	if c.Name != "Amor" {
		t.Fatalf("resolved chat should carry the contact name, got %q", c.Name)
	}
}

type fakeLIDs map[string]string

func (f fakeLIDs) GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	pn, ok := f[lid.ToNonAD().String()]
	if !ok {
		return types.EmptyJID, nil
	}
	return types.ParseJID(pn)
}

func TestIngestLIDResolvedFromStore(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	in.LIDs = fakeLIDs{"555@lid": "5511777@s.whatsapp.net"}
	// group sender with no alt: whatsmeow's store supplies the PN
	in.OnMessage(evt("123@g.us", "555@lid", "", "G1", true, &waE2E.Message{Conversation: proto.String("oi")}))
	if s.ContactName("5511777@s.whatsapp.net") != "Fulano" || s.ContactName("555@lid") != "Fulano" {
		t.Fatal("lid and pn should share one contact")
	}
	// DM keyed by LID with no alt: lands in the PN chat
	in.OnMessage(evt("555@lid", "555@lid", "", "D1", false, &waE2E.Message{Conversation: proto.String("dm")}))
	ms, _ := s.ReadMessages("5511777@s.whatsapp.net", 0, 0, 10)
	if len(ms) != 1 || ms[0].ID != "D1" {
		t.Fatalf("dm should land in pn chat: %+v", ms)
	}
	if ms, _ := s.ReadMessages("555@lid", 0, 0, 10); len(ms) != 0 {
		t.Fatalf("no lid chat expected: %+v", ms)
	}
	// unknown LID stays unlinked, no error
	in.OnMessage(evt("123@g.us", "666@lid", "", "G2", true, &waE2E.Message{Conversation: proto.String("x")}))
	if _, ok := s.PNForLID("666@lid"); ok {
		t.Fatal("unmapped lid must not be linked")
	}
}

func TestBackfillLIDs(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	// pre-existing bare LIDs (no resolver at ingest time)
	in.OnGroup(mustJID("123@g.us"), "Crew", []types.JID{mustJID("555@lid"), mustJID("666@lid")})
	s.AddContact("Zé", "5511777")
	s.SetContactNameIfAuto("666@lid", "Pushy")
	in.LIDs = fakeLIDs{"555@lid": "5511777@s.whatsapp.net", "666@lid": "5511888@s.whatsapp.net"}
	linked, total := in.BackfillLIDs(context.Background())
	if linked != 2 || total != 2 {
		t.Fatalf("linked=%d total=%d", linked, total)
	}
	if s.ContactName("555@lid") != "Zé" {
		t.Fatalf("lid should take the manual contact's name, got %q", s.ContactName("555@lid"))
	}
	if s.ContactName("5511888@s.whatsapp.net") != "Pushy" {
		t.Fatalf("pn should inherit the lid contact's name, got %q", s.ContactName("5511888@s.whatsapp.net"))
	}
	if lids, _ := s.UnlinkedLIDs(); len(lids) != 0 {
		t.Fatalf("still unlinked: %v", lids)
	}
	// second run: nothing left to do
	if linked, total := in.BackfillLIDs(context.Background()); linked != 0 || total != 0 {
		t.Fatalf("second run linked=%d total=%d", linked, total)
	}
}

func TestOnContactLIDAppliesToPN(t *testing.T) {
	in, s := newIngester(t, fakeDL{})
	in.LIDs = fakeLIDs{"555@lid": "5511777@s.whatsapp.net"}
	in.OnContact(mustJID("555@lid"), "Dona Maria")
	if s.ContactName("5511777@s.whatsapp.net") != "Dona Maria" {
		t.Fatalf("got %q", s.ContactName("5511777@s.whatsapp.net"))
	}
	in.OnChatName(mustJID("777@lid"), "Seu João")
	if s.ContactName("777@lid") != "Seu João" {
		t.Fatalf("unmapped lid keeps its own name, got %q", s.ContactName("777@lid"))
	}
}

func mustJID(s string) types.JID {
	j, err := types.ParseJID(s)
	if err != nil {
		panic(err)
	}
	return j
}
