package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	if i := strings.IndexByte(chat, '@'); i >= 0 {
		user = chat[:i]
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
			if _, err := in.Store.ResolveIdentity(jidStr(m), "", ""); err != nil {
				in.logf("resolve member %s: %v", jidStr(m), err)
			}
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
