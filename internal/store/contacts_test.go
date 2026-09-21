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

func TestContactNameNeverShowsLID(t *testing.T) {
	s := mustMem(t)
	// LID-only identity with a push name
	s.ResolveIdentity("111@lid", "", "Pushy")
	if got := s.ContactName("111@lid"); got != "Pushy" {
		t.Fatalf("push name: %q", got)
	}
	// LID linked to a named PN contact
	s.AddContact("Zé", "5511777")
	s.ResolveIdentity("222@lid", "5511777@s.whatsapp.net", "")
	if got := s.ContactName("222@lid"); got != "Zé" {
		t.Fatalf("linked name: %q", got)
	}
	// push name seen on the PN identity serves the LID too, and vice versa
	s.ResolveIdentity("5511999@s.whatsapp.net", "", "Fulano")
	s.ResolveIdentity("333@lid", "5511999@s.whatsapp.net", "")
	if got := s.ContactName("333@lid"); got != "Fulano" {
		t.Fatalf("sibling push name: %q", got)
	}
	// linked PN without any name: the number
	s.ResolveIdentity("444@lid", "5511555@s.whatsapp.net", "")
	if got := s.ContactName("444@lid"); got != "5511555" {
		t.Fatalf("number: %q", got)
	}
	// nothing at all
	s.ResolveIdentity("555@lid", "", "")
	if got := s.ContactName("555@lid"); got != "desconhecido" {
		t.Fatalf("bare lid: %q", got)
	}
	if got := s.ContactName("666@lid"); got != "desconhecido" {
		t.Fatalf("unknown lid: %q", got)
	}
	if got := s.ContactName("5511000@s.whatsapp.net"); got != "5511000" {
		t.Fatalf("unknown pn: %q", got)
	}
	// numbers
	if n := s.ContactNumber("222@lid"); n != "5511777" {
		t.Fatalf("number for linked lid: %q", n)
	}
	if n := s.ContactNumber("555@lid"); n != "" {
		t.Fatalf("number for bare lid: %q", n)
	}
	if n := s.ContactNumber("5511000@s.whatsapp.net"); n != "5511000" {
		t.Fatalf("number for pn: %q", n)
	}
}
