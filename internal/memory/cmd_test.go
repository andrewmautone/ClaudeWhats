package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestWakeFixture(t *testing.T) {
	s := copyFixture(t, "fx")
	part1, rest, _ := strings.Cut(fixture(t, "fx.wake.txt"), "No part 2")
	got, err := s.Wake(1, 0)
	if err != nil || got != part1 {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	if got, _ := s.Wake(0, 40); got != part1 {
		t.Fatal("part 0 means 1, T 40 is now")
	}
	// The python dies on `wake 2 40`: the fixture has one part.
	_, err = s.Wake(2, 40)
	if err == nil || err.Error()+"\n" != "No part 2"+rest {
		t.Fatalf("%v", err)
	}
	_, err = s.Wake(1, 41)
	if err == nil || err.Error() != "T=41, but the log holds 40 memories. Run: claudewhats memory wake" {
		t.Fatal(err)
	}
}

func TestWakeEmpty(t *testing.T) {
	s, _ := Open(t.TempDir())
	got, err := s.Wake(1, 0)
	want := "No memories yet. Record the first with: claudewhats memory note \"<one line>\"\nYou are awake.\n"
	if err != nil || got != want {
		t.Fatalf("%q %v", got, err)
	}
}

func TestWakeTree(t *testing.T) {
	s := copyFixture(t, "fx200")
	got, err := s.Wake(1, 0)
	if err != nil || got != fixture(t, "fx200.wake.txt") {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	got, err = s.Wake(1, 150)
	if err != nil || got != fixture(t, "fx200.wake150.txt") {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	_, err = s.Wake(1, 250)
	if err == nil || err.Error()+"\n" != fixture(t, "fx200.wake250.txt") {
		t.Fatal(err)
	}
	// A block the context needs is missing: refuse, and hand over the nap.
	if gone, _ := s.TreeDrop(0, 8); gone != 47 { // levels 8..128 truncated to 0
		t.Fatal(gone)
	}
	_, err = s.Wake(1, 0)
	if err == nil || err.Error()+"\n" != fixture(t, "fx200.wake-cannot.txt") {
		t.Fatalf("%v", err)
	}
}

func TestWakePending(t *testing.T) {
	s := copyFixture(t, "fx")
	s.LogAppend([]string{"x", "y"})
	got, err := s.Wake(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	nap := strings.TrimSuffix(fixture(t, "fx.nap.txt"), "\n")
	e, _ := s.LogGet(0)
	nap = strings.ReplaceAll(nap, e.Date, today())
	if !strings.HasSuffix(got, "You are awake.\n\n"+nap+"\n") {
		t.Fatalf("wake must end with the pending nap:\n%s", got)
	}
}

func TestWakeMultipart(t *testing.T) {
	s, _ := Open(t.TempDir())
	// 96 lines of ~250 bytes each: over PartChars, so wake pages.
	batch := make([]string, 96)
	for i := range batch {
		batch[i] = strings.Repeat("x", 250)
	}
	s.LogAppend(batch)
	p1, err := s.Wake(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p1, "Your memory, part 1 of 2, oldest first (96 memories).\n#0 ") {
		t.Fatal(p1[:80])
	}
	if !strings.HasSuffix(p1, "\nNot awake yet. Run: claudewhats memory wake 2 96\n") {
		t.Fatal(p1[len(p1)-80:])
	}
	p2, err := s.Wake(2, 96)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is compressed, so the last part ends with the first nap.
	if !strings.HasPrefix(p2, "Your memory, part 2 of 2, oldest first (96 memories).\n") || !strings.Contains(p2, "\nYou are awake.\n\nCompress memories #0-1 into") {
		t.Fatal(p2)
	}
	if _, err := s.Wake(3, 96); err == nil || err.Error() != "No part 3: the memory has 2 parts. Run: claudewhats memory wake" {
		t.Fatal(err)
	}
}

func TestPaginate(t *testing.T) {
	if p := Paginate(nil); len(p) != 0 {
		t.Fatal(p)
	}
	lines := make([]string, PartLines+1)
	for i := range lines {
		lines[i] = "a"
	}
	p := Paginate(lines)
	if len(p) != 2 || len(p[0]) != PartLines || len(p[1]) != 1 {
		t.Fatal(len(p))
	}
	big := []string{strings.Repeat("é", 9000), strings.Repeat("b", 1999), "c"}
	p = Paginate(big) // 18001 + 2000 = 20001 > PartChars: split before b
	if len(p) != 2 || len(p[0]) != 1 || len(p[1]) != 2 {
		t.Fatal(len(p), len(p[0]))
	}
	big[1] = strings.Repeat("b", 1998)
	if p = Paginate(big); len(p) != 2 || len(p[0]) != 2 {
		t.Fatal("exactly PartChars fits; c goes to the next part", len(p))
	}
}

func TestRecallFixture(t *testing.T) {
	s, _ := Open("testdata/fx")
	got, err := s.Recall("numero 1[0-9]")
	if err != nil || got != fixture(t, "fx.recall.txt") {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	got, err = s.Recall("zzz")
	if err != nil || got != fixture(t, "fx.recall-none.txt") {
		t.Fatalf("%q %v", got, err)
	}
	got, _ = s.Recall("NUMERO 39")
	if got != "#39 2026-09-21 memoria numero 39 com acento é ção 39\n1 match.\n" {
		t.Fatalf("case-insensitive: %q", got)
	}
	if _, err := s.Recall("("); err == nil || !strings.HasPrefix(err.Error(), "bad regex: ") {
		t.Fatal(err)
	}
	lines, err := s.RecallLines("numero 1[0-9]", 3)
	if err != nil || len(lines) != 3 || lines[0] != "#17 2026-09-21 memoria numero 17 com acento é ção 17" {
		t.Fatal(lines, err) // newest 3
	}
	lines, _ = s.RecallLines("numero 1[0-9]", 0)
	if len(lines) != 10 {
		t.Fatal(len(lines))
	}
}

func TestRecallCap(t *testing.T) {
	s, _ := Open(t.TempDir())
	batch := make([]string, 200)
	for i := range batch {
		batch[i] = fmt.Sprintf("%03d ", i) + strings.Repeat("y", 200)
	}
	s.LogAppend(batch) // ~216 bytes a line: 200 lines > PartChars
	got, err := s.Recall("y")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "Newest ") || !strings.HasSuffix(last, " of 200 matches. Narrow the regex.") {
		t.Fatal(last)
	}
	if !strings.HasPrefix(lines[len(lines)-2], "#199 ") {
		t.Fatal("newest kept", lines[len(lines)-2])
	}
	size := 0
	for _, l := range lines[:len(lines)-1] {
		size += len(l) + 1
	}
	if size > PartChars {
		t.Fatal(size)
	}
	if fmt.Sprint(len(lines)-1) != strings.Fields(last)[1] {
		t.Fatal(last)
	}
}

func TestZoomFixture(t *testing.T) {
	s := copyFixture(t, "fx")
	got, err := s.Zoom("0-15")
	if err != nil || got != fixture(t, "fx.zoom.txt") {
		t.Fatalf("%q %v", got, err)
	}
	got, _ = s.Zoom("38-39")
	if got != "#38 2026-09-21 memoria numero 38 com acento é ção 38\n#39 2026-09-21 memoria numero 39 com acento é ção 39\n" {
		t.Fatalf("%q", got)
	}
	_, err = s.Zoom("64-127")
	if err == nil || err.Error() != "#64-127 is beyond the memory: it holds 40 memories. Run: claudewhats memory wake" {
		t.Fatal(err)
	}
	if _, err := s.Zoom("3-4"); err == nil {
		t.Fatal("bad block id")
	}
	s.TreeDrop(0, 32)
	if got, _ = s.Zoom("0-31"); got != "#0-15 resumo 0-15\n#16-31 resumo 16-31\n" {
		t.Fatalf("%q", got)
	}
	s.TreeDrop(0, 16)
	if got, _ = s.Zoom("0-31"); got != "#0-15 not compressed yet\n#16-31 not compressed yet\n" {
		t.Fatalf("%q", got)
	}
	// A block reaching into the future shows only what exists (level 16 was
	// truncated above, so 32-47 reads as not built).
	if got, _ = s.Zoom("32-63"); got != "#32-47 not compressed yet\n" {
		t.Fatalf("%q", got)
	}
	s200 := copyFixture(t, "fx200")
	if got, _ := s200.Zoom("0-127"); got != fixture(t, "fx200.zoom.txt") {
		t.Fatalf("%q", got)
	}
}

func TestNoteNapForget(t *testing.T) {
	s := copyFixture(t, "fx")
	e, _ := s.LogGet(0)
	fix := func(name string) string {
		return strings.ReplaceAll(fixture(t, name), e.Date, today())
	}
	got, err := s.Note("x")
	if err != nil || got != "Saved as #40.\n" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = s.Note("  y  ")
	if err != nil || got != fix("fx.note2.txt") {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	got, err = s.Nap("", "")
	if err != nil || got != fix("fx.nap.txt") {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	if _, err := s.Zoom("64-127"); err == nil || err.Error()+"\n" != fixture(t, "fx.zoom-beyond.txt") {
		t.Fatal(err)
	}
	got, err = s.Forget("0-15")
	if err != nil || got != fixture(t, "fx.forget.txt") {
		t.Fatalf("%q %v", got, err)
	}
	got, err = s.Nap("", "")
	if err != nil || got != fix("fx.forgetnap.txt") {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	got, err = s.Nap("40-41", "resumo 40-41")
	if err != nil || got != fix("fx.napanswer.txt") {
		t.Fatalf("got:\n%s\nerr: %v", got, err)
	}
	_, err = s.Nap("0-31", "bad")
	if err == nil || err.Error()+"\n" != fixture(t, "fx.napwrong.txt") {
		t.Fatal(err)
	}
	got, err = s.Nap("40-41", "again")
	if err != nil || !strings.HasPrefix(got, "40-41 is already settled.\n\nCompress memories #0-15") {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := s.Nap("0-15", ""); err == nil || err.Error() != "Empty. A memory is one line of text." {
		t.Fatal(err)
	}
	if _, err := s.Nap("0-15", strings.Repeat("a", 281)); err == nil || !strings.HasPrefix(err.Error(), "Too long: 281 bytes, limit 280.") {
		t.Fatal(err)
	}
	got, _ = s.Nap("0-15", "resumo 0-15")
	if !strings.HasPrefix(got, "0-15 saved.\n\nCompress memories #16-31") {
		t.Fatalf("%q", got)
	}
	for _, id := range []string{"16-31", "0-31"} {
		if _, err := s.Nap(id, "resumo "+id); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ = s.Nap("", ""); got != "Nothing left to compress.\n" {
		t.Fatalf("%q", got)
	}
	if got, _ = s.Nap("0-1", "late"); got != "Nothing left to compress.\n" {
		t.Fatalf("%q", got)
	}
	if _, err := s.Note(strings.Repeat("a", 281)); err == nil || !strings.HasPrefix(err.Error(), "Too long: 281 bytes, limit 280.") {
		t.Fatal(err)
	}
}

func TestForgetNone(t *testing.T) {
	s := copyFixture(t, "fx")
	if _, err := s.Forget("64-127"); err == nil || err.Error() != "No summary at 64-127." {
		t.Fatal(err)
	}
	if _, err := s.Forget("3-4"); err == nil {
		t.Fatal("bad id")
	}
	// (fx.forget-none.txt was captured on a 42-memory copy with 0-15 and
	// 40-41 already gone: 35 there; the pristine fixture drops 37.)
	got, err := s.Forget("2-3")
	if err != nil || got != "Forgot 37 summaries, from 2-3 up. Run: claudewhats memory nap\n" {
		t.Fatalf("%q %v", got, err)
	}
}

func bigStore(tb testing.TB) *Store {
	tb.Helper()
	s, err := Open(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	batch := make([]string, 10000)
	for i := range batch {
		batch[i] = fmt.Sprintf("memoria %d sobre assunto %d", i, i%97)
	}
	if _, err := s.LogAppend(batch); err != nil {
		tb.Fatal(err)
	}
	return s
}

func TestSpeed10k(t *testing.T) {
	s := bigStore(t)
	for _, f := range []struct {
		name string
		fn   func()
	}{
		{"wake", func() { s.Wake(1, 0) }},
		{"note", func() { s.Note("nova") }},
		{"recall", func() { s.Recall("assunto 42") }},
	} {
		t0 := time.Now()
		f.fn()
		d := time.Since(t0)
		t.Logf("%s: %s", f.name, d)
		if d > 100*time.Millisecond {
			t.Fatalf("%s took %s", f.name, d)
		}
	}
	out, _ := s.Recall("assunto 42")
	if !strings.HasSuffix(out, "103 matches.\n") { // i%97==42 for i<10000
		t.Fatal(out[len(out)-40:])
	}
}

func BenchmarkWake(b *testing.B) {
	s := bigStore(b)
	for b.Loop() {
		s.Wake(1, 0)
	}
}

func BenchmarkWakeCompressed(b *testing.B) {
	s, err := Open("testdata/fx200")
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := s.Wake(1, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNote(b *testing.B) {
	s := bigStore(b)
	for b.Loop() {
		s.Note("nova")
	}
}

func BenchmarkRecall(b *testing.B) {
	s := bigStore(b)
	for b.Loop() {
		s.Recall("assunto 42")
	}
}
