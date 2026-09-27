package rankkey

import (
	"strconv"
	"testing"
)

func less(a, b string) bool { return a < b }

func noTrailingMinDigit(t *testing.T, k string) {
	t.Helper()
	if k == "" {
		t.Fatalf("generated key is empty")
	}
	if k[len(k)-1] == digits[0] {
		t.Fatalf("key %q ends in the minimum digit %q (loses subdivision room)", k, string(digits[0]))
	}
}

func assertStrictlySorted(t *testing.T, keys []string) {
	t.Helper()
	for i := 1; i < len(keys); i++ {
		if !less(keys[i-1], keys[i]) {
			t.Fatalf("not strictly sorted at %d: %q >= %q (full=%v)", i, keys[i-1], keys[i], keys)
		}
	}
}

func TestSeedLeavesRoomAroundEveryKey(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 5, 10, 35, 36, 37, 64, 100, 500} {
		t.Run("n="+strconv.Itoa(n), func(t *testing.T) {
			keys := Seed(n)
			if len(keys) != n {
				t.Fatalf("Seed(%d) returned %d keys", n, len(keys))
			}
			assertStrictlySorted(t, keys)
			for i, k := range keys {
				noTrailingMinDigit(t, k)
				lo := ""
				if i > 0 {
					lo = keys[i-1]
				}
				between, err := Between(lo, k)
				if err != nil || (lo != "" && !less(lo, between)) || !less(between, k) {
					t.Fatalf("no room below %q: Between(%q, %q) = %q, %v", k, lo, k, between, err)
				}
			}
			if n > 0 {
				if last := After(keys[n-1]); !less(keys[n-1], last) {
					t.Fatalf("After(%q) = %q is not above it", keys[n-1], last)
				}
			}
		})
	}
}

func TestBruteForceBetweenAllShortPairs(t *testing.T) {
	alpha := []byte{digits[0], digits[1], digits[base/2], digits[base-1]}
	var corpus []string
	corpus = append(corpus, "")
	for _, c0 := range alpha {
		if c0 == digits[0] {
			continue
		}
		corpus = append(corpus, string(c0))
	}
	for _, c0 := range alpha {
		for _, c1 := range alpha {
			if c1 == digits[0] {
				continue
			}
			corpus = append(corpus, string([]byte{c0, c1}))
		}
	}

	for _, a := range corpus {
		for _, b := range corpus {
			k, err := Between(a, b)
			emptyInterval := a != "" && b != "" && a >= b
			if emptyInterval {
				if err == nil {
					t.Fatalf("Between(%q,%q): expected error for empty interval", a, b)
				}
				continue
			}
			if err != nil {
				t.Fatalf("Between(%q,%q): unexpected error: %v", a, b, err)
			}
			if a != "" && !less(a, k) {
				t.Fatalf("Between(%q,%q)=%q: not a < k", a, b, k)
			}
			if b != "" && !less(k, b) {
				t.Fatalf("Between(%q,%q)=%q: not k < b", a, b, k)
			}
			noTrailingMinDigit(t, k)
		}
	}
}
