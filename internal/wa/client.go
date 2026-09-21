package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

var ErrNotPaired = errors.New("não pareado: rode `claudewhats pair` (ou `claudewhats serve`) para escanear o QR")

type Handler interface {
	OnMessage(evt *events.Message)
	OnGroup(jid types.JID, name string, members []types.JID)
	OnPushName(jid types.JID, name string)
	OnContact(jid types.JID, fullName string)
	// OnChatName carries the name history sync attaches to a conversation
	// (group subject or address-book name of a DM).
	OnChatName(jid types.JID, name string)
	// OnConnected fires after each connection's group/contact sync has run.
	OnConnected()
	OnLoggedOut()
}

type Client struct {
	WA *whatsmeow.Client

	log         waLog.Logger
	handlerOnce sync.Once
}

func Open(ctx context.Context, dbPath string, logger waLog.Logger) (*Client, error) {
	// _txlock=immediate: the store shares this file; taking the write lock at BEGIN
	// lets whatsmeow txs wait on busy_timeout instead of failing with SQLITE_BUSY.
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate", dbPath))
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
	wa := whatsmeow.NewClient(dev, logger)
	// the initial full app-state sync is the only time the address book arrives in
	// bulk; without this whatsmeow swallows it and *events.Contact never fires
	wa.EmitAppStateEventsOnFullSync = true
	return &Client{WA: wa, log: logger}, nil
}

func (c *Client) IsPaired() bool { return c.WA.Store.ID != nil }

// Connect registers the event handler (once per Client, even across repeated calls) and
// connects to WhatsApp. ctx is captured by the handler closure for the lifetime of the
// client: it is reused on every subsequent reconnect to send the post-connect presence
// update and to drive the joined-groups sync, so callers must keep it alive (and only
// cancel it) for as long as the Client itself is in use, not just for this one call.
//
// When the store has no session, onQR is called with every fresh QR code and Connect
// blocks until pairing succeeds (nil) or the QR channel ends otherwise ("timeout"
// after whatsmeow runs out of codes, or another terminal event) — callers may then
// simply call Connect again to start a new pairing round. A nil onQR means the
// caller cannot pair: ErrNotPaired.
func (c *Client) Connect(ctx context.Context, h Handler, onQR func(code string)) error {
	c.handlerOnce.Do(func() {
		c.WA.AddEventHandler(func(raw any) {
			switch evt := raw.(type) {
			case *events.Message:
				h.OnMessage(evt)
			case *events.HistorySync:
				for _, conv := range evt.Data.GetConversations() {
					// prefer the phone-number jid so LID-addressed DMs share one chat key
					id := conv.GetPnJID()
					if id == "" {
						id = conv.GetID()
					}
					chat, err := types.ParseJID(id)
					if err != nil {
						continue
					}
					if name := conversationName(conv); name != "" {
						h.OnChatName(chat, name)
					}
					for _, hm := range conv.GetMessages() {
						m, err := c.WA.ParseWebMessage(chat, hm.GetMessage())
						// stubs (revokes, calls, system events) carry no Message: nothing to store
						if err == nil && m.Message != nil {
							h.OnMessage(m)
						}
					}
				}
			case *events.GroupInfo:
				if evt.Name != nil {
					h.OnGroup(evt.JID, evt.Name.Name, nil)
				}
			case *events.JoinedGroup:
				// JoinedGroup embeds types.GroupInfo, which itself embeds GroupName; both
				// Name and Participants are promoted straight onto JoinedGroup.
				h.OnGroup(evt.JID, evt.Name, participantJIDs(evt.Participants))
			case *events.PushName:
				h.OnPushName(evt.JID, evt.NewPushName)
			case *events.Contact:
				if name := contactName(evt.Action); name != "" {
					h.OnContact(evt.JID, name)
				}
			case *events.Connected:
				c.goUnavailable(ctx, "connected")
				go func() {
					c.syncGroups(ctx, h)
					c.syncContacts(ctx)
					h.OnConnected()
				}()
			case *events.AppStateSyncComplete:
				// right after pairing the push name is empty and SendPresence fails;
				// retry once the critical block (which carries it) has synced
				if evt.Name == appstate.WAPatchCriticalBlock {
					c.goUnavailable(ctx, "app state")
				}
			case *events.PushNameSetting:
				c.goUnavailable(ctx, "push name")
			case *events.LoggedOut:
				h.OnLoggedOut()
			}
		})
	})
	if !c.IsPaired() {
		if onQR == nil {
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
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				onQR(item.Code)
			case whatsmeow.QRChannelSuccess.Event:
				return nil
			case whatsmeow.QRChannelEventError:
				return fmt.Errorf("pareamento: %s: %w", item.Event, item.Error)
			default:
				return fmt.Errorf("pareamento: %s", item.Event)
			}
		}
		// channel closed without a terminal event: only happens when ctx ends
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("pareamento: canal de QR fechado")
	}
	return c.WA.Connect()
}

// goUnavailable tells WhatsApp this device is not "online", so the phone keeps
// delivering notifications. Errors are logged, not dropped.
func (c *Client) goUnavailable(ctx context.Context, why string) {
	if err := c.WA.SendPresence(ctx, types.PresenceUnavailable); err != nil {
		c.log.Warnf("presence unavailable (%s): %v", why, err)
	}
}

func participantJIDs(ps []types.GroupParticipant) []types.JID {
	out := make([]types.JID, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.JID)
	}
	return out
}

// contactName is the address-book name from a contact action: full name, else
// first name (some phones only fill one of the two).
func contactName(a *waSyncAction.ContactAction) string {
	if a == nil {
		return ""
	}
	if n := a.GetFullName(); n != "" {
		return n
	}
	return a.GetFirstName()
}

// conversationName is the best name a history-sync conversation carries.
func conversationName(conv *waHistorySync.Conversation) string {
	if n := conv.GetDisplayName(); n != "" {
		return n
	}
	return conv.GetName()
}

// syncContacts asks for the app-state patch that carries the address book. On a
// fresh pairing the full sync already delivers it (see EmitAppStateEventsOnFullSync);
// this covers sessions paired before that flag was set, whose contacts never arrived.
func (c *Client) syncContacts(ctx context.Context) {
	if err := c.WA.FetchAppState(ctx, appstate.WAPatchCriticalUnblockLow, true, false); err != nil {
		c.log.Warnf("contacts app state: %v", err)
	}
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
	c.goUnavailable(ctx, "after send")
	return resp.ID, nil
}

// RequestHistory asks the phone for `count` messages older than `oldest` in chat.
// The phone answers with a HistorySync event, which flows through Handler.OnMessage.
func (c *Client) RequestHistory(ctx context.Context, chat types.JID, oldest *types.MessageInfo, count int) error {
	if c.WA.Store.ID == nil {
		return ErrNotPaired
	}
	if oldest == nil {
		oldest = &types.MessageInfo{MessageSource: types.MessageSource{Chat: chat}}
	}
	msg := c.WA.BuildHistorySyncRequest(oldest, count)
	_, err := c.WA.SendPeerMessage(ctx, msg)
	return err
}

func (c *Client) Download(ctx context.Context, m whatsmeow.DownloadableMessage) ([]byte, error) {
	return c.WA.Download(ctx, m)
}

func (c *Client) Disconnect() { c.WA.Disconnect() }
