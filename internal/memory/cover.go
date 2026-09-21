package memory

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A BLOCK is an aligned power-of-two range of memories, [lo,hi), compressed
// into one line. Blocks form a binary merge tree over LOG.txt: block [lo,hi)
// is the compression of [lo,mid) and [mid,hi).

// cover tiles [0,T) with aligned power-of-two blocks; keep a block whole iff
// its size is at most `alpha` times its age. Bigger alpha = coarser = fewer
// lines.
func cover(T int, alpha float64) [][2]int {
	root := 1
	for root < T {
		root *= 2
	}
	var out [][2]int
	stack := [][2]int{{0, root}}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		lo, hi := b[0], b[1]
		if lo >= T {
			continue
		}
		size := hi - lo
		if size > 1 && (hi > T || float64(size) > alpha*float64(T-lo)) {
			mid := (lo + hi) / 2
			stack = append(stack, [2]int{mid, hi}, [2]int{lo, mid})
		} else {
			out = append(out, [2]int{lo, hi})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// Cover is the blocks wake prints: at most `budget` of them, finest near T.
//
// Detail decays with age, so recent memories stay verbatim and ancient ones
// collapse. If everything fits, nothing is compressed at all.
func Cover(T, budget int) [][2]int {
	if T <= 0 {
		return nil
	}
	if T <= budget {
		out := make([][2]int, T)
		for i := range out {
			out[i] = [2]int{i, i + 1}
		}
		return out
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if len(cover(T, mid)) > budget {
			lo = mid
		} else {
			hi = mid
		}
	}
	out := cover(T, hi)
	// Block sizes jump in powers of two, so alpha alone can undershoot the
	// budget. Spend what is left on the present, where detail is worth most.
	for len(out) < budget {
		i := -1
		for j, b := range out {
			if b[1]-b[0] > 1 {
				i = j
			}
		}
		if i < 0 {
			break
		}
		lo_, hi_ := out[i][0], out[i][1]
		mid := (lo_ + hi_) / 2
		out = append(out[:i], append([][2]int{{lo_, mid}, {mid, hi_}}, out[i+1:]...)...)
	}
	return out
}

// Plural renders "1 memory", "2 memories", "3 matches".
func Plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "y") {
		word = word[:len(word)-1] + "ie"
	} else if strings.HasSuffix(word, "s") || strings.HasSuffix(word, "h") || strings.HasSuffix(word, "x") {
		word += "e"
	}
	return fmt.Sprintf("%d %ss", n, word)
}

var blockRe = regexp.MustCompile(`^(\d+)-(\d+)$`)

// BlockID parses `<lo>-<hi>` as wake and the nap prompts print it: inclusive
// at both ends, and a real block -- an aligned power-of-two range. Without
// the shape check, `4-5` and `5-6` read the same record. Returns [lo,hi).
func BlockID(s string) (lo, hi int, err error) {
	m := blockRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("'%s' is not a block id. Copy it from the prompt.", s)
	}
	lo, _ = strconv.Atoi(m[1])
	hi, _ = strconv.Atoi(m[2])
	hi++
	n := hi - lo
	if n < 2 || n&(n-1) != 0 || lo%n != 0 {
		return 0, 0, fmt.Errorf("%s is not a block. Copy the id printed by wake, like 16-31.", s)
	}
	return lo, hi, nil
}

// Pending lists the blocks that can be built and have not been, smallest
// first, at most `limit` of them (0 = all). Each level file holds a dense
// prefix, so its length says exactly how far that level got: this costs one
// stat per level, never a scan.
func (s *Store) Pending(T, limit int) ([][2]int, error) {
	var todo [][2]int
	for size := 2; size <= T; size *= 2 {
		have, err := count(s.treePath(size), TreeRec)
		if err != nil {
			return nil, err
		}
		for k := have; k < T/size; k++ {
			todo = append(todo, [2]int{k * size, (k + 1) * size})
			if limit > 0 && len(todo) >= limit {
				return todo, nil
			}
		}
	}
	return todo, nil
}

// PendingCount is how many blocks Pending would list, without listing them.
// A level can hold MORE blocks than T needs -- T is a snapshot, and memories
// keep arriving while an agent reads -- so each level is clamped at zero.
func (s *Store) PendingCount(T int) (int, error) {
	n := 0
	for size := 2; size <= T; size *= 2 {
		have, err := count(s.treePath(size), TreeRec)
		if err != nil {
			return 0, err
		}
		if d := T/size - have; d > 0 {
			n += d
		}
	}
	return n, nil
}

// NapPrompt is the compression request for block [lo,hi), with `left` the
// number of compressions that remain after it.
func (s *Store) NapPrompt(lo, hi, left int) (string, error) {
	var body string
	if hi-lo <= RawMax {
		es, err := s.LogSlice(lo, hi)
		if err != nil {
			return "", err
		}
		lines := make([]string, len(es))
		for i, e := range es {
			lines[i] = "  " + e.String()
		}
		body = strings.Join(lines, "\n")
	} else {
		mid := (lo + hi) / 2
		var halves []string
		for _, h := range [][2]int{{lo, mid}, {mid, hi}} {
			a, b := h[0], h[1]
			sum, ok, err := s.TreeGet(a, b)
			if err != nil {
				return "", err
			}
			if !ok {
				// pending() lists a block only after its halves settled, so
				// a missing half is a blank record -- a corrupt write. Drop
				// it and the next nap rebuilds it.
				return "", fmt.Errorf("The summary of #%d-%d is blank. Run: %s forget %d-%d", a, b-1, ToolName, a, b-1)
			}
			halves = append(halves, fmt.Sprintf("  #%d-%d %s", a, b-1, sum))
		}
		body = strings.Join(halves, "\n")
	}
	tail := ""
	if left == 1 {
		tail = "\n1 compression remains after this one."
	} else if left != 0 {
		tail = fmt.Sprintf("\n%d compressions remain after this one.", left)
	}
	return fmt.Sprintf("Compress memories #%d-%d into one line of at most %d bytes.\n"+
		"Keep what has lasting effect, drop what does not. Invent nothing.\n\n"+
		"%s\n%s\n"+
		"Run: %s nap %d-%d \"<your line>\"",
		lo, hi-1, EntryChars, body, tail, ToolName, lo, hi-1), nil
}

// NextNap is the prompt for the first pending block as of T, or "" when
// nothing is pending.
func (s *Store) NextNap(T int) (string, error) {
	todo, err := s.Pending(T, 1)
	if err != nil || len(todo) == 0 {
		return "", err
	}
	n, err := s.PendingCount(T)
	if err != nil {
		return "", err
	}
	return s.NapPrompt(todo[0][0], todo[0][1], n-1)
}
