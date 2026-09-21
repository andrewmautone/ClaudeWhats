package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.LogAppend([]string{"olá ção", "second"})
	if err != nil || id != 0 {
		t.Fatal(id, err)
	}
	e, err := s.LogGet(0)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != 0 || e.Text != "olá ção" || len(e.Date) != 10 {
		t.Fatalf("%+v", e)
	}
	fi, err := os.Stat(filepath.Join(s.Dir, "LOG.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 2*LogRec {
		t.Fatal(fi.Size())
	}
	if n, _ := s.LogLen(); n != 2 {
		t.Fatal(n)
	}
	e1, _ := s.LogGet(1)
	if e1.ID != 1 || e1.Text != "second" {
		t.Fatalf("%+v", e1)
	}
	id, err = s.LogAppend([]string{"third"})
	if err != nil || id != 2 {
		t.Fatal(id, err)
	}
	es, err := s.LogSlice(1, 3)
	if err != nil || len(es) != 2 || es[0].ID != 1 || es[1].Text != "third" {
		t.Fatalf("%+v %v", es, err)
	}
}

func TestOpenMissingDir(t *testing.T) {
	d := filepath.Join(t.TempDir(), "nope")
	if _, err := Open(d); err == nil {
		t.Fatal("Open must refuse a missing dir, as the python store() does")
	}
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Fatal("Open must not create the dir")
	}
}

func TestFixtureParses(t *testing.T) {
	s, err := Open("testdata/fx")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := s.LogLen()
	if n != 40 {
		t.Fatal(n)
	}
	e, _ := s.LogGet(17)
	if e.Text != "memoria numero 17 com acento é ção 17" {
		t.Fatalf("%q", e.Text)
	}
	if e.ID != 17 || e.Date != "2026-09-21" {
		t.Fatalf("%+v", e)
	}
	txt, ok, err := s.TreeGet(0, 16)
	if err != nil || !ok || txt != "resumo 0-15" {
		t.Fatal(txt, ok, err)
	}
	if _, ok, err := s.TreeGet(0, 64); err != nil || ok {
		t.Fatal("level 64 does not exist in the fixture", ok, err)
	}
	if _, ok, err := s.TreeGet(64, 96); err != nil || ok {
		t.Fatal("block past the end of level 32 must be absent", ok, err)
	}
}

func TestPadMatchesPython(t *testing.T) {
	raw, err := os.ReadFile("testdata/fx/LOG.txt")
	if err != nil {
		t.Fatal(err)
	}
	rec := raw[:LogRec]
	e, ok := Parse(rec)
	if !ok {
		t.Fatal("parse")
	}
	got := Pad("#0 "+e.Date+" memoria numero 0 com acento é ção 0", LogRec)
	if !bytes.Equal(got, rec) {
		t.Fatalf("pad differs:\n%q\n%q", got, rec)
	}
	if len(Pad("x", TreeRec)) != TreeRec {
		t.Fatal("tree record width")
	}
}

func TestParseTorn(t *testing.T) {
	if _, ok := Parse([]byte("garbage")); ok {
		t.Fatal("no leading #")
	}
	if _, ok := Parse([]byte("#x 2026-01-01 t")); ok {
		t.Fatal("non-numeric id")
	}
	e, ok := Parse([]byte("#3 2026-01-01 a b  \n"))
	if !ok || e.ID != 3 || e.Text != "a b" {
		t.Fatalf("%+v %v", e, ok)
	}
}

func TestCheckLimit(t *testing.T) {
	if err := Check(strings.Repeat("é", 141)); err == nil || !strings.Contains(err.Error(), "Too long: 282 bytes, limit 280") {
		t.Fatal(err)
	}
	if err := Check(strings.Repeat("é", 140)); err != nil {
		t.Fatal(err)
	}
	if err := Check("a\nb"); err == nil || err.Error() != "2 lines. A memory is one line: merge them, or note them separately." {
		t.Fatal("newline must be rejected as the python does", err)
	}
	if err := Check("  \n "); err == nil || err.Error() != "Empty. A memory is one line of text." {
		t.Fatal(err)
	}
}

func TestTreePutDrop(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.LogAppend([]string{"x0", "x1", "x2", "x3"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.TreePut(0, 2, "a"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.TreePut(0, 2, "again"); ok {
		t.Fatal("slot 0 of level 2 is taken: put must refuse")
	}
	if ok, _ := s.TreePut(4, 6, "skip"); ok {
		t.Fatal("slot 2 of level 2 is not the next one: put must refuse")
	}
	if ok, _ := s.TreePut(2, 4, "b"); !ok {
		t.Fatal()
	}
	if ok, _ := s.TreePut(0, 4, "ab"); !ok {
		t.Fatal()
	}
	if txt, ok, _ := s.TreeGet(2, 4); !ok || txt != "b" {
		t.Fatal(txt, ok)
	}
	// The python truncates each level back to the block, so a LATER block at
	// the same level goes too; an earlier one stays.
	gone, err := s.TreeDrop(2, 4)
	if err != nil || gone != 2 {
		t.Fatal(gone, err) // 2-3 and 0-3 dropped
	}
	if _, ok, _ := s.TreeGet(0, 4); ok {
		t.Fatal("parent must be gone")
	}
	if _, ok, _ := s.TreeGet(0, 2); !ok {
		t.Fatal("earlier block must stay")
	}
	if _, ok, _ := s.TreeGet(2, 4); ok {
		t.Fatal("dropped block must be gone")
	}
	if gone, _ := s.TreeDrop(2, 4); gone != 0 {
		t.Fatal("nothing left to drop", gone)
	}
	s.TreePut(2, 4, "b")
	s.TreePut(0, 4, "ab")
	if gone, _ := s.TreeDrop(0, 2); gone != 3 {
		t.Fatal("0-1, 2-3 (later at level 2) and 0-3 go", gone)
	}
}

func TestRepairTornTail(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.LogAppend([]string{"a"})
	p := filepath.Join(s.Dir, "LOG.txt")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write([]byte("#1 2026-01-01 torn"))
	f.Close()
	if n, _ := s.LogLen(); n != 1 {
		t.Fatal("len counts whole records only", n)
	}
	id, err := s.LogAppend([]string{"b"})
	if err != nil || id != 1 {
		t.Fatal(id, err)
	}
	fi, _ := os.Stat(p)
	if fi.Size() != 2*LogRec {
		t.Fatal("torn tail must be truncated before append", fi.Size())
	}
	e, _ := s.LogGet(1)
	if e.Text != "b" {
		t.Fatalf("%+v", e)
	}
}

func TestLock(t *testing.T) {
	s, _ := Open(t.TempDir())
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := os.Stat(filepath.Join(s.Dir, "LOCK")); err != nil {
		t.Fatal(err)
	}
	unlock, err = s.Lock()
	if err != nil {
		t.Fatal("relock after unlock", err)
	}
	unlock()
}
