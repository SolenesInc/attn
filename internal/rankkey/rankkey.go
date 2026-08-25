// Invariant: a generated key never ends in '0' — a trailing '0' is a numeric no-op
// (0.v == 0.v0) that would break "byte order == numeric order".
package rankkey

import (
	"fmt"
	"strings"
)

const digits = "0123456789abcdefghijklmnopqrstuvwxyz"

const base = len(digits)

func digitVal(b byte) int {
	return strings.IndexByte(digits, b)
}

func digitAt(s string, i, def int) int {
	if i < len(s) {
		return digitVal(s[i])
	}
	return def
}

func Between(a, b string) (string, error) {
	if a != "" && b != "" && a >= b {
		return "", fmt.Errorf("rankkey: empty interval: a=%q must be < b=%q", a, b)
	}

	bMax := b == ""

	var out strings.Builder
	for i := 0; ; i++ {
		da := digitAt(a, i, 0)
		db := base
		if !bMax {
			db = digitAt(b, i, base)
		}

		if da == db {
			out.WriteByte(digits[da])
			continue
		}

		if db-da >= 2 {
			// >= da+1 >= 1, so never the trailing minimum digit.
			out.WriteByte(digits[(da+db)/2])
			return out.String(), nil
		}

		out.WriteByte(digits[da])
		bMax = true

		for i++; ; i++ {
			da = digitAt(a, i, 0)
			if da+1 < base {
				// Midpoint of (da, base): >= da+1 >= 1, so never the minimum digit.
				out.WriteByte(digits[(da+base)/2])
				return out.String(), nil
			}
			out.WriteByte(digits[da])
		}
	}
}

func Seed(n int) []string {
	if n <= 0 {
		return nil
	}
	keys := make([]string, n)
	seedRange(keys, 0, n, "", "")
	return keys
}

func seedRange(keys []string, lo, hi int, low, high string) {
	if lo >= hi {
		return
	}
	mid := (lo + hi) / 2
	k, err := Between(low, high)
	if err != nil {
		panic(fmt.Sprintf("rankkey: Seed subdivision failed between %q and %q: %v", low, high, err))
	}
	keys[mid] = k
	seedRange(keys, lo, mid, low, k)
	seedRange(keys, mid+1, hi, k, high)
}

func After(max string) string {
	if max == "" {
		return string(digits[base/2])
	}
	k, _ := Between(max, "")
	return k
}
