package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

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

	handlerOnce sync.Once
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

// Connect registers the event handler (once per Client, even across repeated calls) and
// connects to WhatsApp. ctx is captured by the handler closure for the lifetime of the
// client: it is reused on every subsequent reconnect to send the post-connect presence
// update and to drive the joined-groups sync, so callers must keep it alive (and only
// cancel it) for as long as the Client itself is in use, not just for this one call.
func (c *Client) Connect(ctx context.Context, h Handler, showQR func(code string)) error {
	c.handlerOnce.Do(func() {
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
				// JoinedGroup embeds types.GroupInfo, which itself embeds GroupName; both
				// Name and Participants are promoted straight onto JoinedGroup.
				h.OnGroup(evt.JID, evt.Name, participantJIDs(evt.Participants))
			case *events.PushName:
				h.OnPushName(evt.JID, evt.NewPushName)
			case *events.Connected:
				_ = c.WA.SendPresence(ctx, types.PresenceUnavailable)
				go c.syncGroups(ctx, h)
			case *events.LoggedOut:
				h.OnLoggedOut()
			}
		})
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
			if item.Event == whatsmeow.QRChannelEventCode {
				showQR(item.Code)
			} else if item.Event != whatsmeow.QRChannelSuccess.Event {
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
