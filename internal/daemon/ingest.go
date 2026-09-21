package daemon

import (
	"context"
	"fmt"
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

// LIDResolver is the slice of whatsmeow's store.LIDStore the ingester needs:
// the LID -> phone-number mapping WhatsApp keeps on the server side.
type LIDResolver interface {
	GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error)
}

type Ingester struct {
	Store         *store.Store
	MediaDir      string
	DL            Downloader
	Log           *log.Logger
	OnLoggedOutFn func()
	// LIDs resolves LID jids to phone numbers when the event carries no alt
	// (history sync, group members, app-state contacts). nil disables it.
	LIDs LIDResolver
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

// pnFor returns the phone-number jid for a LID via whatsmeow's mapping store;
// empty for non-LIDs, when no resolver is set, or when the mapping is unknown.
func (in *Ingester) pnFor(j types.JID) types.JID {
	if in.LIDs == nil || j.Server != types.HiddenUserServer {
		return types.EmptyJID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pn, err := in.LIDs.GetPNForLID(ctx, j)
	if err != nil {
		in.logf("lid %s: %v", j, err)
		return types.EmptyJID
	}
	if pn.IsEmpty() || pn.Server != types.DefaultUserServer {
		return types.EmptyJID
	}
	return pn.ToNonAD()
}

// altOr is alt when the event carries one, else the mapped PN of j.
func (in *Ingester) altOr(j, alt types.JID) types.JID {
	if !alt.IsEmpty() {
		return alt
	}
	return in.pnFor(j)
}

func (in *Ingester) OnMessage(evt *events.Message) {
	info := evt.Info
	// status stories and channels are not conversations: skip before any I/O
	if info.Chat.Server == types.BroadcastServer || info.Chat.Server == types.NewsletterServer {
		return
	}
	chat := jidStr(info.Chat)
	sender := jidStr(info.Sender)
	if chat == "" || sender == "" {
		return
	}
	kind := "dm"
	if info.IsGroup {
		kind = "group"
	}
	pushName := info.PushName
	if info.IsFromMe {
		pushName = ""
	}
	if _, err := in.Store.ResolveIdentity(sender, jidStr(in.altOr(info.Sender, info.SenderAlt)), pushName); err != nil {
		in.logf("resolve sender: %v", err)
	}
	if kind == "dm" && chat != sender {
		if _, err := in.Store.ResolveIdentity(chat, jidStr(in.altOr(info.Chat, info.RecipientAlt)), ""); err != nil {
			in.logf("resolve chat identity: %v", err)
		}
	}
	if kind == "dm" && info.Chat.Server == types.HiddenUserServer {
		chat = in.canonicalDM(info)
	}
	if err := in.Store.UpsertChat(chat, kind, ""); err != nil {
		in.logf("upsert chat: %v", err)
		return
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

// canonicalDM keys a LID-addressed DM by the contact's phone-number jid, so the
// same conversation never splits across an @lid and an @s.whatsapp.net chat.
// whatsmeow carries the PN in RecipientAlt (from-me) / SenderAlt (incoming);
// failing that, fall back to a PN already bound to the LID's contact (which
// the ResolveIdentity calls above just fed from whatsmeow's LID store).
func (in *Ingester) canonicalDM(info types.MessageInfo) string {
	alt := info.SenderAlt
	if info.IsFromMe {
		alt = info.RecipientAlt
	}
	if !alt.IsEmpty() && alt.Server == types.DefaultUserServer {
		return jidStr(alt)
	}
	if pn, ok := in.Store.PNForLID(jidStr(info.Chat)); ok {
		return pn
	}
	return jidStr(info.Chat)
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
	// id and ext come from the wire (stanza id / document fileName): sanitize
	// them and double-check the final path stays under MediaDir.
	dir := filepath.Join(in.MediaDir, safeName(user))
	path := filepath.Join(dir, safeName(id+cl.Ext))
	if rel, err := filepath.Rel(in.MediaDir, path); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("bad media path %q", path)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o600)
}

// safeName keeps only [A-Za-z0-9._-], replaces everything else with '_' and
// strips leading dots, so the result can never escape its directory.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if out == "" {
		return "_"
	}
	return out
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
			if _, err := in.Store.ResolveIdentity(jidStr(m), jidStr(in.pnFor(m)), ""); err != nil {
				in.logf("resolve member %s: %v", jidStr(m), err)
			}
		}
		if err := in.Store.SetGroupMembers(chat, js); err != nil {
			in.logf("members %s: %v", chat, err)
		}
	}
}

func (in *Ingester) OnPushName(jid types.JID, name string) {
	if _, err := in.Store.ResolveIdentity(jidStr(jid), jidStr(in.pnFor(jid)), name); err != nil {
		in.logf("pushname: %v", err)
	}
}

// linkLID binds a LID jid to its phone number (when whatsmeow knows it) so a
// name applied to either side lands on the one shared contact.
func (in *Ingester) linkLID(jid types.JID) {
	pn := in.pnFor(jid)
	if pn.IsEmpty() {
		return
	}
	if _, err := in.Store.ResolveIdentity(jidStr(jid), jidStr(pn), ""); err != nil {
		in.logf("link lid %s: %v", jidStr(jid), err)
	}
}

// OnContact applies an address-book name; a name the user set by hand wins.
func (in *Ingester) OnContact(jid types.JID, fullName string) {
	in.linkLID(jid)
	if err := in.Store.SetContactNameIfAuto(jidStr(jid), fullName); err != nil {
		in.logf("contact %s: %v", jidStr(jid), err)
	}
}

// OnChatName applies the name history sync attaches to a conversation: the
// subject for groups; for DMs an address-book name (manual names still win)
// that also stands in as push name until the peer sends one.
func (in *Ingester) OnChatName(jid types.JID, name string) {
	chat := jidStr(jid)
	if jid.Server == types.GroupServer {
		if err := in.Store.UpsertChat(chat, "group", name); err != nil {
			in.logf("chat name %s: %v", chat, err)
		}
		return
	}
	in.linkLID(jid)
	if err := in.Store.SetContactNameIfAuto(chat, name); err != nil {
		in.logf("chat name %s: %v", chat, err)
	}
	if err := in.Store.SetPushNameIfEmpty(chat, name); err != nil {
		in.logf("chat name %s: %v", chat, err)
	}
}

// OnConnected runs once the post-connect group/contact sync is done: identities
// that arrived as bare LIDs get their phone number from whatsmeow's store.
func (in *Ingester) OnConnected() {
	in.BackfillLIDs(context.Background())
}

// BackfillLIDs links every LID identity that has no phone-number sibling to the
// PN whatsmeow knows for it, merging contacts as needed. Returns linked/total.
func (in *Ingester) BackfillLIDs(ctx context.Context) (linked, total int) {
	if in.LIDs == nil {
		return 0, 0
	}
	lids, err := in.Store.UnlinkedLIDs()
	if err != nil {
		in.logf("lid backfill: %v", err)
		return 0, 0
	}
	total = len(lids)
	for _, l := range lids {
		if ctx.Err() != nil {
			break
		}
		j, err := types.ParseJID(l)
		if err != nil {
			continue
		}
		pn := in.pnFor(j)
		if pn.IsEmpty() {
			continue
		}
		if _, err := in.Store.ResolveIdentity(l, jidStr(pn), ""); err != nil {
			in.logf("lid backfill %s: %v", l, err)
			continue
		}
		linked++
	}
	in.logf("lid backfill: %d/%d vinculados", linked, total)
	return linked, total
}

func (in *Ingester) OnLoggedOut() {
	in.logf("LOGGED OUT: sessão encerrada pelo WhatsApp; rode `claudewhats serve` para parear de novo")
	if in.OnLoggedOutFn != nil {
		in.OnLoggedOutFn()
	}
}
