package review

// diffBudget bounds the search steps of one diff. Measured on 2026-11-10 on
// the user's Mac: a 5,000-line file rewritten whole takes 20M steps (46 ms),
// a 50,000-line file with a tenth of its lines changed 46M (89 ms); half of
// that file changed would take seconds. Past the budget the rest of the diff
// is one replaced block, and the caller says so.
const diffBudget = 100_000_000

// changedLines marks which lines of a and b are not part of a longest common
// subsequence, by Myers' O(ND) algorithm in linear space. The unmarked lines
// of a and b pair up in order. complete is false when the search ran past
// diffBudget and part of the change is marked as one replaced block.
func changedLines(a, b []string) (aChanged, bChanged []bool, complete bool) {
	ids := map[string]int{}
	intern := func(lines []string) []int {
		out := make([]int, len(lines))
		for i, line := range lines {
			id, ok := ids[line]
			if !ok {
				id = len(ids)
				ids[line] = id
			}
			out[i] = id
		}
		return out
	}
	d := differ{a: intern(a), b: intern(b), aChanged: make([]bool, len(a)), bChanged: make([]bool, len(b))}
	d.compare(0, len(a), 0, len(b))
	return d.aChanged, d.bChanged, d.steps <= diffBudget
}

type differ struct {
	a, b               []int
	aChanged, bChanged []bool
	steps              int
}

func (d *differ) compare(aLo, aHi, bLo, bHi int) {
	for aLo < aHi && bLo < bHi && d.a[aLo] == d.b[bLo] {
		aLo++
		bLo++
	}
	for aLo < aHi && bLo < bHi && d.a[aHi-1] == d.b[bHi-1] {
		aHi--
		bHi--
	}
	if aLo == aHi || bLo == bHi || d.steps > diffBudget {
		d.replace(aLo, aHi, bLo, bHi)
		return
	}
	x, y, ok := d.bisect(aLo, aHi, bLo, bHi)
	if !ok || (x == aLo && y == bLo) || (x == aHi && y == bHi) {
		d.replace(aLo, aHi, bLo, bHi)
		return
	}
	d.compare(aLo, x, bLo, y)
	d.compare(x, aHi, y, bHi)
}

func (d *differ) replace(aLo, aHi, bLo, bHi int) {
	for i := aLo; i < aHi; i++ {
		d.aChanged[i] = true
	}
	for j := bLo; j < bHi; j++ {
		d.bChanged[j] = true
	}
}

// bisect finds a point on an optimal edit path by running the search from
// both ends until the two meet.
func (d *differ) bisect(aLo, aHi, bLo, bHi int) (int, int, bool) {
	n, m := aHi-aLo, bHi-bLo
	most := (n + m + 1) / 2
	offset := most
	size := 2*most + 2
	forward := make([]int, size)
	backward := make([]int, size)
	for i := range forward {
		forward[i], backward[i] = -1, -1
	}
	forward[offset+1], backward[offset+1] = 0, 0
	delta := n - m
	odd := delta%2 != 0
	var fStart, fEnd, bStart, bEnd int
	for step := 0; step < most && d.steps <= diffBudget; step++ {
		for k := -step + fStart; k <= step-fEnd; k += 2 {
			at := offset + k
			var x int
			if k == -step || (k != step && forward[at-1] < forward[at+1]) {
				x = forward[at+1]
			} else {
				x = forward[at-1] + 1
			}
			y := x - k
			snake := x
			for x < n && y < m && d.a[aLo+x] == d.b[bLo+y] {
				x++
				y++
			}
			d.steps += x - snake + 1
			forward[at] = x
			switch {
			case x > n:
				fEnd += 2
			case y > m:
				fStart += 2
			case odd:
				other := offset + delta - k
				if other >= 0 && other < size && backward[other] != -1 && x >= n-backward[other] {
					return aLo + x, bLo + y, true
				}
			}
		}
		for k := -step + bStart; k <= step-bEnd; k += 2 {
			at := offset + k
			var x int
			if k == -step || (k != step && backward[at-1] < backward[at+1]) {
				x = backward[at+1]
			} else {
				x = backward[at-1] + 1
			}
			y := x - k
			snake := x
			for x < n && y < m && d.a[aLo+n-x-1] == d.b[bLo+m-y-1] {
				x++
				y++
			}
			d.steps += x - snake + 1
			backward[at] = x
			switch {
			case x > n:
				bEnd += 2
			case y > m:
				bStart += 2
			case !odd:
				other := offset + delta - k
				if other >= 0 && other < size && forward[other] != -1 {
					fx := forward[other]
					fy := offset + fx - other
					if fx >= n-x {
						return aLo + fx, bLo + fy, true
					}
				}
			}
		}
	}
	return 0, 0, false
}
