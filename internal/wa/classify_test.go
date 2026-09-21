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
