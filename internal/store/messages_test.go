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

func TestBackfillDoesNotRegressLastMsgAt(t *testing.T) {
	s := mustMem(t)
	// Insert newer message first
	s.UpsertChat("test@g.us", "group", "Test")
	s.InsertMessage(Message{ChatJID: "test@g.us", ID: "msg1", SenderJID: "user@s.whatsapp.net", TS: 500, Type: "text", Text: "recent"})

	// Verify last_msg_at is 500
	chats, _ := s.ListChats(0, "")
	if len(chats) != 1 || chats[0].LastMsgAt != 500 {
		t.Fatalf("initial last_msg_at should be 500, got %d", chats[0].LastMsgAt)
	}

	// Insert older message (backfill scenario)
	s.InsertMessage(Message{ChatJID: "test@g.us", ID: "msg2", SenderJID: "user@s.whatsapp.net", TS: 100, Type: "text", Text: "older"})

	// Verify last_msg_at stays 500 (should use max)
	chats, _ = s.ListChats(0, "")
	if len(chats) != 1 || chats[0].LastMsgAt != 500 {
		t.Fatalf("backfill should not regress last_msg_at, expected 500 got %d", chats[0].LastMsgAt)
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
	found, _ := s.Search("reunião", "", 0, 10)
	if len(found) != 1 || found[0].ID != "m1" {
		t.Fatalf("fts: %+v", found)
	}
	found, _ = s.Search("bolo", "5511888@s.whatsapp.net", 0, 10)
	if len(found) != 1 || found[0].ID != "m2" || found[0].Sender != "eu" {
		t.Fatalf("filtered search: %+v", found)
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
		t.Fatalf("group by substring: %+v %v", c, err)
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
	msgs, pendjobs, _ := s.Stats()
	if msgs != 3 || pendjobs != 0 {
		t.Fatalf("msgs=%d pendjobs=%d", msgs, pendjobs)
	}
}

func TestSetGroupMembers(t *testing.T) {
	s := mustMem(t)
	s.UpsertChat("g1@g.us", "group", "Crew")
	if err := s.SetGroupMembers("g1@g.us", []string{"a@s.whatsapp.net", "b@s.whatsapp.net", "c@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMembers("g1@g.us", []string{"a@s.whatsapp.net", "c@s.whatsapp.net"}); err != nil {
		t.Fatal(err)
	}
	// Verify the members were updated
	var count int
	s.DB().QueryRow(`SELECT count(*) FROM group_members WHERE chat_jid=?`, "g1@g.us").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 members, got %d", count)
	}
}

func TestResolveChatByPushName(t *testing.T) {
	s := mustMem(t)
	// contact first seen as a group member (no name), later a push name arrives
	s.ResolveIdentity("5511222@s.whatsapp.net", "", "")
	s.UpsertChat("5511222@s.whatsapp.net", "dm", "")
	s.InsertMessage(Message{ChatJID: "5511222@s.whatsapp.net", ID: "p1", SenderJID: "5511222@s.whatsapp.net", TS: 10, Type: "text", Text: "e aí"})
	s.ResolveIdentity("5511222@s.whatsapp.net", "", "Zezinho")
	c, err := s.ResolveChat("zezinho")
	if err != nil || c.JID != "5511222@s.whatsapp.net" || c.Name != "Zezinho" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestResolveChatRanking(t *testing.T) {
	s := mustMem(t)
	for jid, name := range map[string]string{
		"1@s.whatsapp.net": "Amor☀️💛",
		"2@s.whatsapp.net": "zamora",
		"3@s.whatsapp.net": "Otavio Silva - VW Zamora",
		"4@s.whatsapp.net": "Maria",
	} {
		s.AddContact(name, jid)
		s.UpsertChat(jid, "dm", "")
	}
	cases := map[string]string{"amor": "1@s.whatsapp.net", "vw": "3@s.whatsapp.net", "ria": "4@s.whatsapp.net", "AMOR": "1@s.whatsapp.net"}
	for ref, want := range cases {
		c, err := s.ResolveChat(ref)
		if err != nil || c.JID != want {
			t.Fatalf("%s: %+v %v", ref, c, err)
		}
	}
	_, err := s.ResolveChat("zam")
	if err == nil || !strings.Contains(err.Error(), "zamora") || !strings.Contains(err.Error(), "VW Zamora") || strings.Contains(err.Error(), "Amor") {
		t.Fatalf("expected ambiguity between the two Zamoras, got %v", err)
	}
}

func TestFindContactRanking(t *testing.T) {
	s := mustMem(t)
	ids := map[string]int64{}
	for jid, name := range map[string]string{
		"1@s.whatsapp.net": "Amor☀️💛",
		"2@s.whatsapp.net": "zamora",
		"3@s.whatsapp.net": "Otavio Silva - VW Zamora",
		"4@s.whatsapp.net": "Maria",
	} {
		ids[name], _ = s.AddContact(name, jid)
	}
	for ref, want := range map[string]string{"amor": "Amor☀️💛", "vw": "Otavio Silva - VW Zamora", "ria": "Maria"} {
		id, err := s.findContact(ref)
		if err != nil || id != ids[want] {
			t.Fatalf("%s: %d %v", ref, id, err)
		}
	}
	if _, err := s.findContact("zam"); err == nil || !strings.Contains(err.Error(), "ambíguo") {
		t.Fatalf("expected ambiguity, got %v", err)
	}
}

func TestListChatsNumberAndLIDName(t *testing.T) {
	s := mustMem(t)
	seed(t, s)
	s.ResolveIdentity("777@lid", "", "")
	s.UpsertChat("777@lid", "dm", "")
	s.InsertMessage(Message{ChatJID: "777@lid", ID: "l1", SenderJID: "777@lid", TS: 5, Type: "text", Text: "?"})
	chats, err := s.ListChats(0, "dm")
	if err != nil {
		t.Fatal(err)
	}
	byJID := map[string]Chat{}
	for _, c := range chats {
		byJID[c.JID] = c
	}
	if c := byJID["5511888@s.whatsapp.net"]; c.Number != "5511888" || c.Name != "Maria" {
		t.Fatalf("%+v", c)
	}
	if c := byJID["777@lid"]; c.Number != "" || c.Name != "desconhecido" {
		t.Fatalf("%+v", c)
	}
	c, err := s.ResolveChat("5511888")
	if err != nil || c.Number != "5511888" {
		t.Fatalf("%+v %v", c, err)
	}
}
