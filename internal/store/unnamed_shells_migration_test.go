package store

import (
	"path/filepath"
	"testing"
)

func TestUnnamedShellMigrationClearsOnlyPlaceholderNames(t *testing.T) {
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
		{"shell", "shop", "/tmp/workshop", "shop"},
		{"shell", "shell 1 build", "/tmp/shop", "shell 1 build"},
		{"shell", "server", "/tmp/shop", "server"},
		{"shell", "", "/tmp/shop", ""},
		{"claude", "shell", "/tmp/shop", "shell"},
		{"codex", "shop", "/tmp/shop", "shop"},
	}
	for i, c := range cases {
		if _, err := db.Exec(`INSERT INTO sessions (id, agent, label, directory, state, state_since, state_updated_at, last_seen) VALUES (?, ?, ?, ?, 'idle', '', '', '')`, i, c.agent, c.label, c.directory); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, c := range cases {
		var label string
		if err := db.QueryRow(`SELECT label FROM sessions WHERE id=?`, i).Scan(&label); err != nil {
			t.Fatal(err)
		}
		if label != c.want {
			t.Errorf("%s %q in %q became %q, want %q", c.agent, c.label, c.directory, label, c.want)
		}
	}
}
