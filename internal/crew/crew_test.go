package crew

import "testing"

func TestDisplayName(t *testing.T) {
	for id, want := range map[string]string{
		"trellis":   "Trellis",
		"a":         "A",
		"mary-jane": "Mary-jane",
		"Trellis":   "Trellis",
		"":          "",
		"  keel  ":  "Keel",
		"ólafur":    "Ólafur",
		DaemonID:    DaemonID,
	} {
		if got := DisplayName(id); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", id, got, want)
		}
	}
}
