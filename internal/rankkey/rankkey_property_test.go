package rankkey

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestBetweenPropertiesUnderRandomReorders(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		keys := []string{After("")}

		insert := func(t *rapid.T, g int, lo, hi string) {
			k, err := Between(lo, hi)
			if err != nil {
				t.Fatalf("Between(%q, %q) on a non-empty interval: %v", lo, hi, err)
			}
			if lo != "" && !(lo < k) {
				t.Fatalf("Between(%q, %q) = %q is not above its low bound", lo, hi, k)
			}
			if hi != "" && !(k < hi) {
				t.Fatalf("Between(%q, %q) = %q is not below its high bound", lo, hi, k)
			}
			if strings.HasSuffix(k, string(digits[0])) {
				t.Fatalf("Between(%q, %q) = %q ends in the minimum digit; it has no room below it", lo, hi, k)
			}
			keys = append(keys, "")
			copy(keys[g+1:], keys[g:])
			keys[g] = k
		}

		t.Repeat(map[string]func(*rapid.T){
			"insert_front": func(t *rapid.T) {
				insert(t, 0, "", keys[0])
			},
			"insert_back": func(t *rapid.T) {
				insert(t, len(keys), keys[len(keys)-1], "")
			},
			"insert_between": func(t *rapid.T) {
				if len(keys) < 2 {
					t.Skip("needs two keys to have a gap")
				}
				g := rapid.IntRange(1, len(keys)-1).Draw(t, "gap")
				insert(t, g, keys[g-1], keys[g])
			},
			"append_new": func(t *rapid.T) {
				k := After(keys[len(keys)-1])
				if !(keys[len(keys)-1] < k) {
					t.Fatalf("After(%q) = %q is not above it", keys[len(keys)-1], k)
				}
				if strings.HasSuffix(k, string(digits[0])) {
					t.Fatalf("After(%q) = %q ends in the minimum digit", keys[len(keys)-1], k)
				}
				keys = append(keys, k)
			},
			"": func(t *rapid.T) {
				for i, k := range keys {
					if k == "" {
						t.Fatalf("key %d is the empty sentinel, which is not a key", i)
					}
					if strings.HasSuffix(k, string(digits[0])) {
						t.Fatalf("key %d (%q) ends in the minimum digit", i, k)
					}
					if i > 0 && !(keys[i-1] < k) {
						t.Fatalf("keys %d and %d are out of order: %q >= %q\nlist: %q", i-1, i, keys[i-1], k, keys)
					}
				}
			},
		})
	})
}

// No bound is asserted — repeatedly halving one gap grows the key by design — only the shape.
func TestBetweenGrowsKeysByAtMostOneDigit(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lo := rapid.StringOfN(rapid.SampledFrom([]rune(digits)), 0, 8, -1).Draw(t, "lo")
		hi := rapid.StringOfN(rapid.SampledFrom([]rune(digits)), 0, 8, -1).Draw(t, "hi")
		if lo != "" && hi != "" && lo >= hi {
			t.Skip("empty interval")
		}
		k, err := Between(lo, hi)
		if err != nil {
			t.Fatalf("Between(%q, %q): %v", lo, hi, err)
		}
		bound := max(len(lo), len(hi))
		if len(k) > bound+1 {
			t.Fatalf("Between(%q, %q) = %q is %d digits, more than one past its longest bound (%d)",
				lo, hi, k, len(k), bound)
		}
	})
}
