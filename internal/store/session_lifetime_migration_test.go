package store

import (
	"github.com/victorarias/attn/internal/protocol"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/harness"
)

func TestUpgradeKeepsEverySessionsConversationWhereItRan(t *testing.T) {
	const beforeLifetime = 1795323468946777 - 1
	saved := []struct {
		id, agent, link string
		want            harness.Lifetime
	}{
		{"shared-codex", "codex", "codex-app-server", harness.InServer},
		{"sigma", "sigma", "sigma-server", harness.InServer},
		{"plugin-server", "snipe", "snipe-server", harness.InServer},
		{"claude", "claude", "", harness.InTerminal},
		{"plain-codex", "codex", "", harness.InTerminal},
	}
	path := filepath.Join(t.TempDir(), "sessions.db")
	old, err := newStoreAtVersion(path, beforeLifetime)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range saved {
		if _, err := old.db.Exec(`INSERT INTO sessions(id,user_label,directory,agent,link) VALUES(?,'s','/tmp',?,?)`, s.id, s.agent, s.link); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for _, s := range saved {
		if agent, lifetime := upgraded.SessionLifetime(protocol.SessionID(s.id)); agent != s.agent || lifetime != s.want {
			t.Errorf("session %s with link %q after the upgrade = %s in its %s, want %s in its %s", s.id, s.link, agent, lifetime, s.agent, s.want)
		}
	}
}
