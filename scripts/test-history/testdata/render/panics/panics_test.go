package panics

import "testing"

func TestPanics(t *testing.T) {
	var counts map[string]int
	counts["a"] = 1
}
