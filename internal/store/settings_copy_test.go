package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/protocol"
)

func TestCopiedRowsRefuseEverySQLWriteOutsideTheCopyApplier(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows := []protocol.CopyRow{
		{Table: protocol.CopyTableSessions, Key: "copied-session", Columns: protocol.RecordUnknown{"id": "copied-session", "user_label": "home", "directory": "/tmp", "host": "outpost"}},
		{Table: protocol.CopyTableTerminalBindings, Key: "copied-terminal", Columns: protocol.RecordUnknown{"terminal_id": "copied-terminal", "session_id": "copied-session"}},
		{Table: protocol.CopyTableSettings, Key: "theme", Columns: protocol.RecordUnknown{"key": "theme", "value": "dark"}},
	}
	if err := s.ApplyCopy(context.Background(), "home", rows); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ table, key, query string }{
		{"sessions", "copied-session", `UPDATE sessions SET user_label='wrong' WHERE id='copied-session'`},
		{"sessions", "copied-session", `UPDATE sessions SET copied_from='' WHERE id='copied-session'`},
		{"sessions", "copied-session", `DELETE FROM sessions WHERE id='copied-session'`},
		{"sessions", "copied-session", `INSERT OR REPLACE INTO sessions(id,user_label,directory) VALUES('copied-session','wrong','/tmp')`},
		{"sessions", "new-copy", `INSERT INTO sessions(id,user_label,directory,copied_from) VALUES('new-copy','wrong','/tmp','home')`},
		{"terminal_bindings", "copied-terminal", `UPDATE terminal_bindings SET session_id='copied-session' WHERE terminal_id='copied-terminal'`},
		{"terminal_bindings", "copied-terminal", `DELETE FROM terminal_bindings WHERE terminal_id='copied-terminal'`},
		{"terminal_bindings", "copied-terminal", `INSERT OR REPLACE INTO terminal_bindings(terminal_id,session_id) VALUES('copied-terminal','copied-session')`},
		{"terminal_bindings", "new-terminal", `INSERT INTO terminal_bindings(terminal_id,session_id) VALUES('new-terminal','copied-session')`},
		{"settings", "theme", `UPDATE settings SET value='wrong' WHERE key='theme'`},
		{"settings", "theme", `UPDATE settings SET copied_from='' WHERE key='theme'`},
		{"settings", "theme", `DELETE FROM settings WHERE key='theme'`},
		{"settings", "theme", `INSERT OR REPLACE INTO settings(key,value) VALUES('theme','wrong')`},
		{"settings", "new-key", `INSERT INTO settings(key,value,copied_from) VALUES('new-key','wrong','home')`},
	} {
		t.Run(tc.query, func(t *testing.T) {
			_, err := s.db.Exec(tc.query)
			if err == nil || !strings.Contains(err.Error(), tc.table) || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("refusal must name table and row: %v", err)
			}
			t.Log(err)
		})
	}
	if err := s.SetSettingChecked("theme", "wrong"); err == nil || !strings.Contains(err.Error(), "settings row theme") {
		t.Fatalf("public setting write: %v", err)
	}
	if got := s.Get("copied-session"); got == nil || got.Label != "home" || s.CopiedSetting("theme", "home") != "dark" {
		t.Fatalf("a refused write changed copied rows: %+v", got)
	}
	rows[0].Columns["user_label"] = "updated"
	rows[2].Columns["value"] = "light"
	if err := s.ApplyCopy(context.Background(), "home", rows); err != nil {
		t.Fatal(err)
	}
	if s.Get("copied-session").Label != "updated" || s.CopiedSetting("theme", "home") != "light" {
		t.Fatal("copy applier could not replace its rows")
	}
	s.Remove("copied-session")
	if s.Get("copied-session") == nil {
		t.Fatal("ordinary Remove deleted a copied session")
	}
	if err := s.DropCopyNotFrom(""); err != nil {
		t.Fatal(err)
	}
	if s.Get("copied-session") != nil || s.CopiedSetting("theme", "home") != "" {
		t.Fatal("leave did not drop copied rows")
	}
	if err := s.SetSettingChecked("theme", "system"); err != nil {
		t.Fatalf("copy permission leaked or leave did not free settings: %v", err)
	}
}

func TestHomeSettingsUpgradePreservesPreviousDatabaseAndOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "previous.db")
	previous := 1792879590336371
	for _, migration := range timestampedMigrations {
		if migration.version < 1793074357713072 && migration.version > previous {
			previous = migration.version
		}
	}
	old, err := openDBAtVersion(path, previous)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO settings(key,value) VALUES ('theme','light'),('default_model_codex','local-model'),('worktree_sweep_enabled','false')`,
		`INSERT INTO outposts(id,name,address,added_at) VALUES ('one','one','localhost:1','now'),('two','two','localhost:2','now')`,
		`INSERT INTO sessions(id,user_label,directory,host) VALUES ('session','kept','/tmp','one')`,
	} {
		if _, err := old.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	current, upgrade, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if upgrade.From != previous || upgrade.BackupPath == "" {
		t.Fatalf("upgrade: %+v", upgrade)
	}
	if session := current.Get("session"); session == nil || session.Label != "kept" {
		t.Fatalf("previous session lost: %+v", session)
	}
	if got := current.GetSetting("theme"); got != "light" {
		t.Fatalf("local value lost: %q", got)
	}
	if err := current.SetSettingChecked("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"one", "two"} {
		if err := current.RequeueOutpostCopy(host); err != nil {
			t.Fatal(err)
		}
		batch, err := current.OutboxBatch(host, bridge.MaxCallRequest)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range batch.Rows {
			if row.Table == protocol.CopyTableSettings && row.Key == "theme" {
				found = true
			}
			if row.Table == protocol.CopyTableSettings && row.Key == "worktree_sweep_enabled" {
				t.Fatal("machine setting entered outbox")
			}
		}
		if !found {
			t.Fatalf("%s lost shared setting outbox row: %+v", host, batch)
		}
	}
	if err := current.ApplyCopy(context.Background(), "home", []protocol.CopyRow{{Table: protocol.CopyTableSettings, Key: "theme", Columns: protocol.RecordUnknown{"key": "theme", "value": "copied"}}}); err != nil {
		t.Fatal(err)
	}
	if current.GetSetting("theme") != "dark" || current.CopiedSetting("theme", "home") != "copied" {
		t.Fatal("copy overwrote standalone value")
	}
	if err := current.DropCopyNotFrom(""); err != nil {
		t.Fatal(err)
	}
	if current.GetSetting("theme") != "dark" {
		t.Fatal("leave lost standalone value")
	}
	t.Logf("opened previous schema %d, migrated to %d, preserved settings/session/outbox, queued shared home key for two hosts, kept machine key local, and restored standalone value", previous, upgrade.To)
}
