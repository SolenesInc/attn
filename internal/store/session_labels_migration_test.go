package store

import (
	"github.com/victorarias/attn/internal/sessioncost"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSplittingLabelsPreservesNamesAndTheirOwnersAcrossUpgradeAndRestart(t *testing.T) {
	directoryName := strings.Repeat("𐐷", 49)
	cases := []struct{ id, label, directory, agent, closed, user, harness string }{
		{"named", "Checkout race", "/tmp/shop", "codex", "", "Checkout race", ""},
		{"default", "shop", "/tmp/shop", "claude", "", "", "shop"},
		{"short-id", "a3c9e1", "/tmp/shop", "claude", "", "", "a3c9e1"},
		{"seed-id", "s-3k9e1p", "/tmp/shop", "claude", "", "", "s-3k9e1p"},
		{"word", "planet", "/tmp/shop", "claude", "", "planet", ""},
		{"ticket", "SHOP-12", "/tmp/shop", "claude", "", "", "SHOP-12"},
		{"uuid", "12345678-1234-1234-1234-123456789abc", "/tmp/shop", "claude", "", "", "12345678-1234-1234-1234-123456789abc"},
		{"truncated", strings.Repeat("𐐷", 48), "/tmp/" + directoryName, "claude", "", "", strings.Repeat("𐐷", 48)},
		{"shell-auto", "", "/tmp/shop", "shell", "", "", ""},
		{"shell-named", "shop", "/tmp/shop", "shell", "", "shop", ""},
		{"shell-id", "a3c9e1", "/tmp/shop", "shell", "", "a3c9e1", ""},
		{"shell-history", "shop", "/tmp/shop", "shell", "2026-10-18T12:00:00Z", "shop", ""},
		{"history", "Checkout race", "/tmp/shop", "claude", "2026-10-18T12:00:00Z", "Checkout race", ""},
		{"history-id", "a3c9e1", "/tmp/shop", "claude", "2026-10-18T12:00:00Z", "", "a3c9e1"},
	}
	path := filepath.Join(t.TempDir(), "labels.db")
	db, err := OpenDBAtSchemaVersion(path, 1792106066149768)
	if err != nil {
		t.Fatal(err)
	}
	const cost = `{"initialized":true,"cursor":"saved-cost-cursor","usage_unavailable":true}`
	for _, c := range cases {
		if _, err := db.Exec(`INSERT INTO sessions(id,label,directory,agent,state,state_since,state_updated_at,last_seen,closed_at,session_cost_json) VALUES(?,?,?,?,'idle','','','',?,?)`, c.id, c.label, c.directory, c.agent, c.closed, cost); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for restart := range 2 {
		s, err := NewWithDB(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			id := protocol.SessionID(c.id)
			labels := s.SessionLabels(id)
			if labels.UserLabel != c.user || labels.HarnessTitle != c.harness {
				t.Errorf("restart%d %s owners=%+v, want user=%q harness=%q", restart, id, labels, c.user, c.harness)
			}
			var label string
			if c.closed == "" {
				session := s.Get(id)
				if session == nil {
					t.Fatalf("session %s disappeared", id)
				}
				label = session.Label
			} else {
				entry := s.SessionLedgerEntry(id)
				if entry == nil {
					t.Fatalf("closed session %s disappeared", id)
				}
				label = entry.Label
			}
			if label != c.label {
				t.Errorf("restart%d %s label=%q, want preserved %q", restart, id, label, c.label)
			}
			checkpoint, err := s.SessionCostCheckpoint(id)
			if err != nil || !checkpoint.Initialized || checkpoint.Sources[sessioncost.LegacySource] == nil || checkpoint.Sources[sessioncost.LegacySource].Cursor != "saved-cost-cursor" || !checkpoint.Usage().UsageUnavailable {
				t.Errorf("restart%d %s cost checkpoint=%+v (%v), want preserved cursor and unavailable flag", restart, id, checkpoint, err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
