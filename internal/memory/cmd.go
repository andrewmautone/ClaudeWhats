package memory

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// The commands. Each returns exactly what the python prints to stdout, one
// "\n" per printed line, and returns as an error exactly what the python
// prints before exiting 1 (without the trailing newline).

// Paginate splits the document into parts that survive any harness's output
// cap.
func Paginate(lines []string) [][]string {
	var parts [][]string
	var cur []string
	size := 0
	for _, line := range lines {
		n := len(line) + 1
		if len(cur) > 0 && (len(cur) >= PartLines || size+n > PartChars) {
			parts = append(parts, cur)
			cur, size = nil, 0
		}
		cur = append(cur, line)
		size += n
	}
	if len(cur) > 0 {
		parts = append(parts, cur)
	}
	return parts
}

// Wake renders part `part` (0 = 1) of the memory as of T (0 = now): the
// cover's blocks, oldest first, then the footer that says whether the read
// is finished, then the first pending compression if any.
func (s *Store) Wake(part, T int) (string, error) {
	now, err := s.LogLen()
	if err != nil {
		return "", err
	}
	if part == 0 {
		part = 1
	}
	if T == 0 {
		T = now
	} else if T > now {
		return "", fmt.Errorf("T=%d, but the log holds %s. Run: %s wake", T, Plural(now, "memory"), ToolName)
	}
	// A part is rendered as of T, so a note landing between two parts cannot
	// shift a boundary and drop a line.
	var out strings.Builder
	if T == 0 {
		fmt.Fprintf(&out, "No memories yet. Record the first with: %s note \"<one line>\"\n", ToolName)
		out.WriteString("You are awake.\n")
		return out.String(), nil
	}
	var lines []string
	for _, b := range Cover(T, WakeLines) {
		lo, hi := b[0], b[1]
		if hi-lo == 1 {
			e, err := s.LogGet(lo)
			if err != nil {
				return "", err
			}
			lines = append(lines, e.String())
			continue
		}
		sum, ok, err := s.TreeGet(lo, hi)
		if err != nil {
			return "", err
		}
		if !ok {
			nap, err := s.NextNap(T)
			if err != nil {
				return "", err
			}
			if nap != "" {
				// The ONLY reason to refuse: this document cannot be
				// written without that summary. Work that the document
				// does not need is handed over after the read instead,
				// costing no round trip.
				n, err := s.PendingCount(T)
				if err != nil {
					return "", err
				}
				return "", fmt.Errorf("Cannot wake: the memory context needs #%d-%d, which is not compressed yet.\nDo the %s below, then run %s wake again.\n\n%s",
					lo, hi-1, Plural(n, "compression"), ToolName, nap)
			}
			sum, ok, err = s.TreeGet(lo, hi) // a parallel session may have paid it
			if err != nil {
				return "", err
			}
		}
		if !ok {
			// Nothing is pending, so the record exists but is blank -- a
			// corrupt write. Drop it and the next nap rebuilds it.
			return "", fmt.Errorf("The summary of #%d-%d is blank. Run: %s forget %d-%d", lo, hi-1, ToolName, lo, hi-1)
		}
		lines = append(lines, fmt.Sprintf("#%d-%d %s", lo, hi-1, sum))
	}
	parts := Paginate(lines)
	if part < 1 || part > len(parts) {
		return "", fmt.Errorf("No part %d: the memory has %s. Run: %s wake", part, Plural(len(parts), "part"), ToolName)
	}
	if len(parts) > 1 {
		// The count is here so the T in `wake 2 296` reads as what it is:
		// the snapshot this document was written from.
		fmt.Fprintf(&out, "Your memory, part %d of %d, oldest first (%s).\n", part, len(parts), Plural(T, "memory"))
	}
	out.WriteString(strings.Join(parts[part-1], "\n"))
	out.WriteString("\n")
	if part < len(parts) {
		// This footer is the only instruction that survives every harness's
		// truncation (pi drops the HEAD of a long output), so it has to say
		// both that the read is unfinished and how to continue it.
		fmt.Fprintf(&out, "Not awake yet. Run: %s wake %d %d\n", ToolName, part+1, T)
		return out.String(), nil
	}
	// always, even for a one-part memory: the contract an agent is given
	// is "run parts until one says awake", so it must always arrive
	out.WriteString("You are awake.\n")
	nap, err := s.NextNap(T)
	if err != nil {
		return "", err
	}
	if nap != "" {
		out.WriteString("\n" + nap + "\n")
	}
	return out.String(), nil
}

// Note records one memory and hands over the first pending compression.
func (s *Store) Note(text string) (string, error) {
	text, err := clean(text)
	if err != nil {
		return "", err
	}
	i, err := s.LogAppend([]string{text})
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("Saved as #%d.\n", i)
	nap, err := s.NextNap(i + 1)
	if err != nil {
		return "", err
	}
	if nap != "" {
		out += "\n" + nap + "\n"
	}
	return out, nil
}

// Nap saves the compression `text` of block `id` when given, then prints the
// next pending compression. With id "" it only prints the next one.
func (s *Store) Nap(id, text string) (string, error) {
	T, err := s.LogLen()
	if err != nil {
		return "", err
	}
	var out strings.Builder
	said := id != ""
	if said {
		lo, hi, err := BlockID(id)
		if err != nil {
			return "", err
		}
		todo, err := s.Pending(T, 1)
		if err != nil {
			return "", err
		}
		if len(todo) == 0 {
			return "Nothing left to compress.\n", nil
		}
		if todo[0] != [2]int{lo, hi} {
			_, ok, err := s.TreeGet(lo, hi)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("Wrong block: %s. Blocks are built in order; the next is %d-%d. Run: %s nap", id, todo[0][0], todo[0][1]-1, ToolName)
			}
			fmt.Fprintf(&out, "%d-%d is already settled.\n", lo, hi-1)
		} else {
			text, err := clean(text)
			if err != nil {
				return "", err
			}
			ok, err := s.TreePut(lo, hi, text)
			if err != nil {
				return "", err
			}
			if !ok {
				fmt.Fprintf(&out, "%d-%d was settled or forgotten meanwhile.\n", lo, hi-1)
			} else {
				fmt.Fprintf(&out, "%d-%d saved.\n", lo, hi-1)
			}
		}
	}
	nap, err := s.NextNap(T)
	if err != nil {
		return "", err
	}
	if nap == "" {
		out.WriteString("Nothing left to compress.\n")
		return out.String(), nil
	}
	if said {
		out.WriteString("\n")
	}
	out.WriteString(nap + "\n")
	return out.String(), nil
}

// Forget drops a summary and everything built on top of it; the next nap
// computes them again. The log is untouched, so nothing is ever lost.
func (s *Store) Forget(id string) (string, error) {
	lo, hi, err := BlockID(id)
	if err != nil {
		return "", err
	}
	gone, err := s.TreeDrop(lo, hi)
	if err != nil {
		return "", err
	}
	if gone == 0 {
		return "", fmt.Errorf("No summary at %s.", id)
	}
	return fmt.Sprintf("Forgot %s, from %d-%d up. Run: %s nap\n", Plural(gone, "summary"), lo, hi-1, ToolName), nil
}

// scan streams every memory to fn, in one pass over the log, never holding
// it: at a million memories that is 300 MB.
func (s *Store) scan(fn func(e Entry)) error {
	f, err := os.Open(s.logPath())
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, LogRec*4096)
	for {
		n, err := io.ReadFull(f, buf)
		for _, e := range records(buf[:n]) {
			fn(e)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func compile(pattern string) (*regexp.Regexp, error) {
	pat, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("bad regex: %s", strings.TrimPrefix(err.Error(), "error parsing regexp: "))
	}
	return pat, nil
}

// Recall searches every memory, case-insensitively, and prints the newest
// matches that fit one part: a vague regex matches the whole log, and the
// whole log does not fit in a harness's output or in memory.
func (s *Store) Recall(pattern string) (string, error) {
	pat, err := compile(pattern)
	if err != nil {
		return "", err
	}
	hits, size := 0, 0
	var out []string
	head := 0 // out[head:] is the window kept; the front is dropped lazily
	err = s.scan(func(e Entry) {
		line := e.String()
		if !pat.MatchString(line) {
			return
		}
		hits++
		out = append(out, line)
		size += len(line) + 1
		for size > PartChars {
			size -= len(out[head]) + 1
			head++
		}
		if head > 0 && head*2 >= len(out) {
			out = append([]string(nil), out[head:]...)
			head = 0
		}
	})
	if err != nil {
		return "", err
	}
	if hits == 0 {
		return "No match.\n", nil
	}
	out = out[head:]
	res := strings.Join(out, "\n") + "\n"
	if len(out) < hits {
		res += fmt.Sprintf("Newest %d of %s. Narrow the regex.\n", len(out), Plural(hits, "match"))
	} else {
		res += fmt.Sprintf("%s.\n", Plural(hits, "match"))
	}
	return res, nil
}

// RecallLines is Recall for programs: the raw matching lines, oldest first,
// keeping only the newest `max` of them (0 = all).
func (s *Store) RecallLines(pattern string, max int) ([]string, error) {
	pat, err := compile(pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	head := 0
	err = s.scan(func(e Entry) {
		line := e.String()
		if !pat.MatchString(line) {
			return
		}
		out = append(out, line)
		if max > 0 && len(out)-head > max {
			head++
		}
		if head > 0 && head*2 >= len(out) {
			out = append([]string(nil), out[head:]...)
			head = 0
		}
	})
	if err != nil {
		return nil, err
	}
	return out[head:], nil
}

// Zoom opens one node of the tree: its two halves, each rendered as wake
// renders it -- a summary, or the raw memory once a half is single.
func (s *Store) Zoom(id string) (string, error) {
	lo, hi, err := BlockID(id)
	if err != nil {
		return "", err
	}
	T, err := s.LogLen()
	if err != nil {
		return "", err
	}
	if lo >= T {
		return "", fmt.Errorf("#%s is beyond the memory: it holds %s. Run: %s wake", id, Plural(T, "memory"), ToolName)
	}
	mid := (lo + hi) / 2
	var out strings.Builder
	for _, h := range [][2]int{{lo, mid}, {mid, hi}} {
		a, b := h[0], h[1]
		if a >= T {
			continue // the future: no memories there yet
		}
		if b-a == 1 {
			e, err := s.LogGet(a)
			if err != nil {
				return "", err
			}
			out.WriteString(e.String() + "\n")
			continue
		}
		sum, ok, err := s.TreeGet(a, b)
		if err != nil {
			return "", err
		}
		if !ok {
			sum = "not compressed yet"
		}
		fmt.Fprintf(&out, "#%d-%d %s\n", a, b-1, sum)
	}
	return out.String(), nil
}
