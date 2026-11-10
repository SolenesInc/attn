package review

import (
	"testing"

	"pgregory.net/rapid"
)

func linesFrom(t *rapid.T, label string) []string {
	return rapid.SliceOfN(rapid.SampledFrom([]string{"a", "b", "c", "d", "}", ""}), 0, 40).Draw(t, label)
}

func edited(t *rapid.T, from []string) []string {
	out := append([]string{}, from...)
	for _, op := range rapid.SliceOfN(rapid.IntRange(0, 2), 0, 8).Draw(t, "edits") {
		at := rapid.IntRange(0, len(out)).Draw(t, "at")
		switch {
		case op == 0:
			out = append(out[:at], append([]string{rapid.SampledFrom([]string{"a", "x", "y", ""}).Draw(t, "line")}, out[at:]...)...)
		case op == 1 && at < len(out):
			out = append(out[:at], out[at+1:]...)
		case at < len(out):
			out[at] = rapid.SampledFrom([]string{"a", "x", "z"}).Draw(t, "replacement")
		}
	}
	return out
}

func lcsLength(a, b []string) int {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	return table[0][0]
}

func TestLineMapIsTheIdentityWithNoChange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lines := linesFrom(t, "lines")
		if len(lines) == 0 {
			return
		}
		m := newLineMap(lines, lines)
		from := rapid.IntRange(1, len(lines)).Draw(t, "from")
		to := rapid.IntRange(from, len(lines)).Draw(t, "to")
		if start, end, ok := m.span(from, to); !ok || start != from || end != to {
			t.Fatalf("span(%d, %d) = %d, %d, %t with no change", from, to, start, end, ok)
		}
	})
}

func TestLineMapNeverReordersLines(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		head := linesFrom(t, "head")
		now := edited(t, head)
		m := newLineMap(head, now)
		last := 0
		for i, line := range m.kept {
			if line == 0 {
				continue
			}
			if line <= last {
				t.Fatalf("head line %d maps to %d, after an earlier line mapped to %d", i+1, line, last)
			}
			if head[i] != now[line-1] {
				t.Fatalf("head line %d %q maps to now line %d %q", i+1, head[i], line, now[line-1])
			}
			last = line
		}
		if len(head) == 0 {
			return
		}
		from := rapid.IntRange(1, len(head)).Draw(t, "from")
		later := rapid.IntRange(from, len(head)).Draw(t, "later")
		to := rapid.IntRange(later, len(head)).Draw(t, "to")
		start, end, ok := m.span(from, to)
		laterStart, laterEnd, _ := m.span(later, to)
		if ok != (len(now) > 0) {
			t.Fatalf("span(%d, %d) reports ok=%t into %d lines", from, to, ok, len(now))
		}
		if ok && (start < 1 || end > len(now)) {
			t.Fatalf("span(%d, %d) = %d, %d, outside the %d lines now", from, to, start, end, len(now))
		}
		if start > end {
			t.Fatalf("span(%d, %d) = %d, %d runs backwards", from, to, start, end)
		}
		if laterStart < start || laterEnd < end {
			t.Fatalf("span(%d, %d) = %d, %d starts before span(%d, %d) = %d, %d", later, to, laterStart, laterEnd, from, to, start, end)
		}
	})
}

func TestChangedLinesKeepALongestCommonSubsequence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := linesFrom(t, "a")
		b := edited(t, a)
		if rapid.Bool().Draw(t, "unrelated") {
			b = linesFrom(t, "b")
		}
		aChanged, bChanged, _ := changedLines(a, b)
		var aKept, bKept []string
		for i, changed := range aChanged {
			if !changed {
				aKept = append(aKept, a[i])
			}
		}
		for j, changed := range bChanged {
			if !changed {
				bKept = append(bKept, b[j])
			}
		}
		if !equalLines(aKept, bKept) {
			t.Fatalf("unchanged lines differ:\n%q\n%q", aKept, bKept)
		}
		if want := lcsLength(a, b); len(aKept) != want {
			t.Fatalf("kept %d lines; the longest common subsequence has %d", len(aKept), want)
		}
	})
}
