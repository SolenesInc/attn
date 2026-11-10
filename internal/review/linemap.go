package review

import "github.com/victorarias/attn/internal/protocol"

// lineMap carries line numbers of one text into another. A line that survives
// maps to its line in the other text; a changed line maps to the lines that
// replaced it.
type lineMap struct {
	kept     []int
	lo, hi   []int
	length   int
	complete bool
}

func newLineMap(from, to []string) lineMap {
	fromChanged, toChanged, complete := changedLines(from, to)
	m := lineMap{kept: make([]int, len(from)), lo: make([]int, len(from)), hi: make([]int, len(from)), length: len(to), complete: complete}
	i, j := 0, 0
	for {
		i0, j0 := i, j
		for i < len(from) && fromChanged[i] {
			i++
		}
		for j < len(to) && toChanged[j] {
			j++
		}
		for k := i0; k < i; k++ {
			m.lo[k], m.hi[k] = j0+1, j
		}
		if i >= len(from) || j >= len(to) {
			return m
		}
		m.kept[i] = j + 1
		i++
		j++
	}
}

// span maps the lines from..to, 1-based and inside the text, to the lines that
// show them in the other text. A range whose lines are all gone maps to the
// one line where they were. ok is false when the other text is empty, so no
// line shows them.
func (m lineMap) span(from, to int) (start, end int, ok bool) {
	if m.length == 0 {
		return 0, 0, false
	}
	start, end = m.kept[from-1], m.kept[to-1]
	if start == 0 {
		start = m.lo[from-1]
	}
	if end == 0 {
		end = m.hi[to-1]
	}
	if end < start {
		start = min(max(start, 1), m.length)
		end = start
	}
	return start, end, true
}

// rows are the lines now..to of the text now with the change from base:
// unchanged and added lines, and the removed lines that sat between them.
func rows(base, now []string, from, to int) ([]protocol.ReviewRow, bool) {
	baseChanged, nowChanged, complete := changedLines(base, now)
	var out []protocol.ReviewRow
	i, j := 0, 0
	for i < len(base) || j < len(now) {
		switch {
		case i < len(base) && baseChanged[i]:
			if next := j + 1; next >= from && next <= to+1 {
				out = append(out, protocol.ReviewRow{Kind: "-", Old: protocol.Ptr(i + 1), Text: base[i]})
			}
			i++
		case j < len(now) && nowChanged[j]:
			if line := j + 1; line >= from && line <= to {
				out = append(out, protocol.ReviewRow{Kind: "+", Now: protocol.Ptr(line), Text: now[j]})
			}
			j++
		default:
			if line := j + 1; line >= from && line <= to {
				out = append(out, protocol.ReviewRow{Kind: " ", Old: protocol.Ptr(i + 1), Now: protocol.Ptr(line), Text: now[j]})
			}
			i++
			j++
		}
	}
	return out, complete
}
