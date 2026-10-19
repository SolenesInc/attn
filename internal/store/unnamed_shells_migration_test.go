package store

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestUnnamedShellMigrationClearsOnlyPlaceholderNames(t *testing.T) {
	for _, lifecycle := range []struct{ name, closedAt string }{
		{"live", ""},
		{"closed", "2026-10-15T10:00:00Z"},
	} {
		t.Run(lifecycle.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "before.db")
			db, err := openDBAtVersion(path, 1792106066149767)
			if err != nil {
				t.Fatal(err)
			}
			cases := []struct{ agent, label, directory, want string }{
				{"shell", "shell", "/tmp/shop", ""},
				{"shell", "shell 12", "/tmp/shop", ""},
				{"shell", "shop", "/tmp/shop", ""},
				{"shell", "shop", "/tmp/shop/", ""},
				{"shell", "my%shop", "/tmp/my%shop", ""},
				{"shell", "/", "/", ""},
				{"shell", "shop", "/tmp/workshop", "shop"},
				{"shell", "shell 1 build", "/tmp/shop", "shell 1 build"},
				{"shell", "server", "/tmp/shop", "server"},
				{"shell", "", "/tmp/shop", ""},
				{"claude", "shell", "/tmp/shop", "shell"},
				{"codex", "shop", "/tmp/shop", "shop"},
			}
			for i, c := range cases {
				if _, err := db.Exec(`INSERT INTO sessions (id, agent, label, directory, closed_at, state, state_since, state_updated_at, last_seen) VALUES (?, ?, ?, ?, ?, 'idle', '', '', '')`, i, c.agent, c.label, c.directory, lifecycle.closedAt); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := NewWithDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			for i, c := range cases {
				entry := upgraded.SessionLedgerEntry(protocol.SessionID(strconv.Itoa(i)))
				if entry == nil {
					t.Fatalf("session %d disappeared", i)
				}
				want := c.want
				if lifecycle.closedAt != "" {
					want = c.label
				}
				if entry.Label != want {
					t.Errorf("%s %q in %q became %q, want %q", c.agent, c.label, c.directory, entry.Label, want)
				}
			}
		})
	}
}
