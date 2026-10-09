package store

import (
	"path/filepath"
	"testing"
)

func TestUpgradeKeepsEveryProfilesNotebookFolder(t *testing.T) {
	for _, tc := range []struct{ name, stored, instance, harness, want string }{
		{name: "default", want: "~/attn-notebook"},
		{name: "named instance", instance: "dev", want: "~/attn-notebook-dev"},
		{name: "stored", stored: "~/notes", want: "~/notes"},
		{name: "stored named", stored: "~/notes", instance: "dev", want: "~/notes"},
		{name: "harness", harness: "/tmp/attn-migration/notebook", want: "/tmp/attn-migration/notebook"},
		{name: "stored wins harness", stored: "~/notes", harness: "/tmp/attn-migration/notebook", want: "~/notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ATTN_INSTANCE", tc.instance)
			if tc.harness != "" {
				t.Setenv("ATTN_HARNESS_DATA_DIR", "/tmp/attn-migration")
				t.Setenv("ATTN_HARNESS_NOTEBOOK_ROOT", tc.harness)
			}
			path := filepath.Join(t.TempDir(), "upgrade.db")
			db, err := openDBAtVersion(path, 1791587775600278)
			if err != nil {
				t.Fatal(err)
			}
			if tc.stored != "" {
				if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('notebook.root', ?)`, tc.stored); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`INSERT INTO profiles (id, name, current_desktop_id, created_at, deleted_at) VALUES ('gone', 'Gone', '', '2026-01-01', '2026-02-01')`); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			current, err := NewWithDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer current.Close()
			profiles, err := current.ListProfiles(true)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range profiles {
				if root := current.ProfileSetting(p.ID, "notebook.root"); root != tc.want {
					t.Errorf("%s root=%q, want %q", p.Name, root, tc.want)
				}
			}
			var count int
			if err := current.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'notebook.root'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("stale daemon root count %d: %v", count, err)
			}
		})
	}
}
