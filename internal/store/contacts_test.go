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

func TestPNForLID(t *testing.T) {
	s := mustMem(t)
	if _, ok := s.PNForLID("444@lid"); ok {
		t.Fatal("unknown lid must not resolve")
	}
	s.ResolveIdentity("444@lid", "", "x")
	if _, ok := s.PNForLID("444@lid"); ok {
		t.Fatal("lid without pn must not resolve")
	}
	s.ResolveIdentity("5511555@s.whatsapp.net", "444@lid", "x")
	if pn, ok := s.PNForLID("444@lid"); !ok || pn != "5511555@s.whatsapp.net" {
		t.Fatalf("%q %v", pn, ok)
	}
}

func TestSetContactNameIfAuto(t *testing.T) {
	s := mustMem(t)
	// unknown jid: creates an auto contact and names it
	if err := s.SetContactNameIfAuto("5511444@s.whatsapp.net", "Ana Livro"); err != nil {
		t.Fatal(err)
	}
	if s.ContactName("5511444@s.whatsapp.net") != "Ana Livro" {
		t.Fatal("auto contact should take the address-book name")
	}
	// manual contact keeps its name
	s.AddContact("Maria", "5511888")
	if err := s.SetContactNameIfAuto("5511888@s.whatsapp.net", "Maria Silva"); err != nil {
		t.Fatal(err)
	}
	if s.ContactName("5511888@s.whatsapp.net") != "Maria" {
		t.Fatal("manual name must win over address book")
	}
	if err := s.SetContactNameIfAuto("5511888@s.whatsapp.net", ""); err != nil {
		t.Fatal(err)
	}
}
