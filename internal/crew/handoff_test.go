package crew

import (
	"slices"
	"testing"
	"time"
)

func TestHandoffNamesSortFreshestFirstWhateverTheClocksZone(t *testing.T) {
	tokyo := time.FixedZone("UTC+9", 9*3600)
	denver := time.FixedZone("UTC-6", -6*3600)
	filed := []struct {
		at   time.Time
		name string
	}{
		{time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC).In(denver), "2026-08-12T09-00Z-trellis.md"},
		{time.Date(2026, 8, 14, 19, 30, 12, 0, time.UTC).In(tokyo), "2026-08-14T19-30Z-trellis.md"},
		{time.Date(2026, 8, 13, 22, 20, 0, 0, time.UTC), "2026-08-13T22-20Z-trellis.md"},
		{time.Date(2026, 8, 14, 3, 0, 0, 0, time.UTC).In(tokyo), "2026-08-14T03-00Z-trellis.md"},
	}
	var names []string
	for _, letter := range filed {
		name := HandoffFileName("trellis", letter.at)
		if name != letter.name {
			t.Errorf("filed at %s as %s, want %s", letter.at, name, letter.name)
		}
		names = append(names, name)
	}
	SortHandoffNames(names)
	want := []string{"2026-08-14T19-30Z-trellis.md", "2026-08-14T03-00Z-trellis.md", "2026-08-13T22-20Z-trellis.md", "2026-08-12T09-00Z-trellis.md"}
	if !slices.Equal(names, want) {
		t.Fatalf("sorted to %v, want %v", names, want)
	}
}
