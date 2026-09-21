package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixture copies testdata/<name> into a temp dir so tests may write.
func copyFixture(t *testing.T, name string) *Store {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	src := filepath.Join("testdata", name)
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func blocks(bs [][2]int) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = fmt.Sprintf("%d-%d", b[0], b[1])
	}
	return strings.Join(parts, " ")
}

func TestCoverMatchesPython(t *testing.T) {
	seen := 0
	for _, line := range strings.Split(strings.TrimSpace(fixture(t, "fx.cover.txt")), "\n") {
		f := strings.Fields(line)
		switch f[0] {
		case "cover":
			var T int
			fmt.Sscan(f[1], &T)
			want := strings.Join(f[2:], " ")
			if got := blocks(Cover(T, WakeLines)); got != want {
				t.Errorf("cover(%d):\n got %s\nwant %s", T, got, want)
			}
			if len(Cover(T, WakeLines)) > WakeLines {
				t.Errorf("cover(%d) over budget", T)
			}
			seen++
		}
	}
	if seen != 7 {
		t.Fatal("fixture lines", seen)
	}
	if len(Cover(0, WakeLines)) != 0 {
		t.Fatal("cover(0)")
	}
}

func TestPendingFixture(t *testing.T) {
	s, _ := Open("testdata/fx")
	fx := fixture(t, "fx.cover.txt")
	if !strings.Contains(fx, "pending \n") || !strings.Contains(fx, "pending_count 0\n") {
		t.Fatal("fixture says the store is fully compressed")
	}
	p, err := s.Pending(40, 0)
	if err != nil || len(p) != 0 {
		t.Fatal(p, err)
	}
	n, err := s.PendingCount(40)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	// A snapshot T smaller than what the levels hold clamps at zero.
	if n, _ := s.PendingCount(20); n != 0 {
		t.Fatal(n)
	}
}

func TestPendingFresh(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.LogAppend([]string{"a", "b", "c", "d", "e"})
	p, _ := s.Pending(5, 0)
	if got := blocks(p); got != "0-2 2-4 0-4" {
		t.Fatal(got) // smallest first
	}
	p, _ = s.Pending(5, 2)
	if got := blocks(p); got != "0-2 2-4" {
		t.Fatal("limit", got)
	}
	if n, _ := s.PendingCount(5); n != 3 {
		t.Fatal(n)
	}
	s.TreePut(0, 2, "ab")
	p, _ = s.Pending(5, 0)
	if got := blocks(p); got != "2-4 0-4" {
		t.Fatal(got)
	}
	if n, _ := s.PendingCount(5); n != 2 {
		t.Fatal(n)
	}
}

func TestBlockID(t *testing.T) {
	lo, hi, err := BlockID("0-15")
	if err != nil || lo != 0 || hi != 16 {
		t.Fatal(lo, hi, err)
	}
	lo, hi, err = BlockID("16-31")
	if err != nil || lo != 16 || hi != 32 {
		t.Fatal(lo, hi, err)
	}
	for _, bad := range []string{"3-4", "4-4", "0-2", "5-6", "8-23"} {
		if _, _, err := BlockID(bad); err == nil || err.Error() != bad+" is not a block. Copy the id printed by wake, like 16-31." {
			t.Fatal(bad, err)
		}
	}
	for _, bad := range []string{"x", "0-", "-1-2", "0-15 "} {
		if _, _, err := BlockID(bad); err == nil || err.Error() != "'"+bad+"' is not a block id. Copy it from the prompt." {
			t.Fatal(bad, err)
		}
	}
}

func TestPlural(t *testing.T) {
	for in, want := range map[string]string{
		"1 memory": "1 memory", "2 memory": "2 memories", "0 memory": "0 memories",
		"3 match": "3 matches", "1 match": "1 match", "2 part": "2 parts",
		"0 compression": "0 compressions", "1 summary": "1 summary", "5 summary": "5 summaries",
	} {
		var n int
		var w string
		fmt.Sscan(in, &n, &w)
		if got := Plural(n, w); got != want {
			t.Errorf("Plural(%d,%q)=%q want %q", n, w, got, want)
		}
	}
}

func TestNextNap(t *testing.T) {
	s := copyFixture(t, "fx")
	got, err := s.NextNap(40)
	if err != nil || got != "" {
		t.Fatal(got, err)
	}
	if _, err := s.LogAppend([]string{"x", "y"}); err != nil {
		t.Fatal(err)
	}
	// The python output was captured after `note x` and `note y` today; the
	// fixture's dates are the day the fixture was built, so patch the date.
	e, _ := s.LogGet(0)
	want := strings.TrimSuffix(strings.ReplaceAll(fixture(t, "fx.nap.txt"), e.Date, today()), "\n")
	got, err = s.NextNap(42)
	if err != nil || got != want {
		t.Fatalf("got:\n%s\nwant:\n%s\n(%v)", got, want, err)
	}
	// Still nothing to do as of the old snapshot.
	if got, _ := s.NextNap(40); got != "" {
		t.Fatal(got)
	}
}

func TestNapPromptHalves(t *testing.T) {
	s := copyFixture(t, "fx")
	if gone, _ := s.TreeDrop(0, 32); gone != 1 {
		t.Fatal(gone)
	}
	want := strings.TrimSuffix(fixture(t, "fx.nap-halves.txt"), "\n")
	got, err := s.NextNap(40)
	if err != nil || got != want {
		t.Fatalf("got:\n%s\nwant:\n%s\n(%v)", got, want, err)
	}
	got, err = s.NapPrompt(0, 32, 1)
	if err != nil || !strings.Contains(got, "\n1 compression remains after this one.\nRun:") {
		t.Fatal(got, err)
	}
	got, _ = s.NapPrompt(0, 32, 2)
	if !strings.Contains(got, "\n2 compressions remain after this one.\nRun:") {
		t.Fatal(got)
	}
	s.TreeDrop(0, 16)
	if _, err := s.NapPrompt(0, 32, 0); err == nil || err.Error() != "The summary of #0-15 is blank. Run: claudewhats memory forget 0-15" {
		t.Fatal(err)
	}
}
