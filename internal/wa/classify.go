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
