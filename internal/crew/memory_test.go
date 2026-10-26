package crew_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"pgregory.net/rapid"
)

func assertMemoryCover(t *rapid.T, blocks []crew.MemoryBlock, total int) {
	next := 0
	for _, b := range blocks {
		size := b.Hi - b.Lo
		if b.Lo != next || size < 1 || size&(size-1) != 0 || b.Lo%size != 0 {
			t.Fatalf("invalid cover of %d: %v", total, blocks)
		}
		next = b.Hi
	}
	if next != total {
		t.Fatalf("covered through %d, want %d: %v", next, total, blocks)
	}
}

func TestMemoryCoverProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		total := rapid.IntRange(0, 100000).Draw(t, "entries")
		blocks := crew.MemoryCover(total, crew.MemoryWakeBudget)
		assertMemoryCover(t, blocks, total)
		if len(blocks) > crew.MemoryWakeBudget {
			t.Fatalf("%d lines exceeds wake budget %d", len(blocks), crew.MemoryWakeBudget)
		}
		for i := 1; i < len(blocks); i++ {
			if blocks[i].Hi-blocks[i].Lo > blocks[i-1].Hi-blocks[i-1].Lo {
				t.Fatalf("detail gets coarser toward the present: %v", blocks)
			}
		}
		if total <= crew.MemoryWakeBudget && len(blocks) != total {
			t.Fatalf("%d entries fit without compression, got %v", total, blocks)
		}
	})
}

func TestMemoryCoverOptMemReceipts(t *testing.T) {
	for _, tc := range []struct{ total, raw, largest int }{{100, 92, 2}, {1000, 28, 64}, {10000, 18, 1024}} {
		t.Run(fmt.Sprint(tc.total), func(t *testing.T) {
			raw, largest := 0, 0
			for _, b := range crew.MemoryCover(tc.total, 96) {
				size := b.Hi - b.Lo
				if size == 1 {
					raw++
				}
				largest = max(largest, size)
			}
			if raw != tc.raw || largest != tc.largest {
				t.Fatalf("%d entries: raw=%d largest=%d, OptMem raw=%d largest=%d", tc.total, raw, largest, tc.raw, tc.largest)
			}
		})
	}
}

func TestMemoryWakeFallsBackToHalves(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		total := rapid.IntRange(0, 4096).Draw(t, "entries")
		m := crew.Memory{Entries: make([]crew.MemoryEntry, total), Summaries: map[int][]crew.MemorySummary{}}
		for size := 2; size <= total; size *= 2 {
			have := rapid.IntRange(0, total/size).Draw(t, fmt.Sprintf("summaries-%d", size))
			m.Summaries[size] = make([]crew.MemorySummary, have)
		}
		cover := crew.MemoryCover(total, crew.MemoryWakeBudget)
		expanded := m.Expand(cover)
		assertMemoryCover(t, expanded, total)
		for _, b := range expanded {
			if b.Hi-b.Lo > 1 {
				if _, exists := m.Summary(b); !exists {
					t.Fatalf("missing summary still appears: %v", b)
				}
			}
		}
		for _, b := range cover {
			if b.Hi-b.Lo < 2 {
				continue
			}
			_, exists := m.Summary(b)
			if exists && !slices.Contains(expanded, b) {
				t.Fatalf("present summary %s was split", b)
			}
			if !exists {
				mid := (b.Lo + b.Hi) / 2
				want := m.Expand([]crew.MemoryBlock{{Lo: b.Lo, Hi: mid}, {Lo: mid, Hi: b.Hi}})
				got := m.Expand([]crew.MemoryBlock{b})
				if !slices.Equal(got, want) {
					t.Fatalf("missing %s = %v, want its halves %v", b, got, want)
				}
			}
		}
		for size := 2; size <= total; size *= 2 {
			m.Summaries[size] = make([]crew.MemorySummary, total/size)
		}
		if got := m.Expand(cover); !slices.Equal(got, cover) || len(got) > crew.MemoryWakeBudget {
			t.Fatalf("complete summaries changed the budgeted cover: %v", got)
		}
	})
}
