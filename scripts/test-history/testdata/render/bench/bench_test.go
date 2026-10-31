package bench

import "testing"

func TestPasses(t *testing.T) {}

func BenchmarkLoop(b *testing.B) {
	for range b.N {
	}
}
