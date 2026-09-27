package workflow

import "testing"

func TestIsCacheHitTruthTable(t *testing.T) {
	base := JournalEntry{Ordinal: "ord", PromptHash: "ph", SchemaHash: "sh", Status: "ok"}
	cases := []struct {
		name             string
		ord, prom, schem string
		want             bool
	}{
		{"all match", "ord", "ph", "sh", true},
		{"ordinal mismatch", "other", "ph", "sh", false},
		{"prompt mismatch", "ord", "other", "sh", false},
		{"schema mismatch", "ord", "ph", "other", false},
		{"ordinal+prompt mismatch", "other", "other", "sh", false},
		{"all mismatch", "x", "y", "z", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsCacheHit(base, tc.ord, tc.prom, tc.schem)
			if got != tc.want {
				t.Errorf("IsCacheHit=%v want %v", got, tc.want)
			}
		})
	}

	for _, status := range []string{"running", ""} {
		t.Run("non-terminal status "+status, func(t *testing.T) {
			e := JournalEntry{Ordinal: "ord", PromptHash: "ph", SchemaHash: "sh", Status: status}
			if IsCacheHit(e, "ord", "ph", "sh") {
				t.Errorf("IsCacheHit=true for non-terminal status %q, want false", status)
			}
		})
	}

	for _, status := range []string{"ok", "skipped", "errored"} {
		t.Run("terminal status "+status, func(t *testing.T) {
			e := JournalEntry{Ordinal: "ord", PromptHash: "ph", SchemaHash: "sh", Status: status}
			if !IsCacheHit(e, "ord", "ph", "sh") {
				t.Errorf("IsCacheHit=false for terminal status %q, want true", status)
			}
		})
	}
}
