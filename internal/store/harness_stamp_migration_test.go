package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
)

func TestUpgradingNamesTheHarnessEachCrewMemberAndTheAdvisorRanOn(t *testing.T) {
	const version = 1795338125260106
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, version-1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DefineDocumentCollection(crew.MembersSchema(), time.Now()); err != nil {
		t.Fatal(err)
	}
	schema, _, err := s.DocumentCollection(crew.Namespace, crew.CollectionMembers)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.ProfileMigration()
	if err != nil {
		t.Fatal(err)
	}
	members := map[string]string{"m-unset": "", "m-blank": "  ", "m-codex": "codex", "m-chief": ""}
	for id, agent := range members {
		body, err := json.Marshal(map[string]string{"id": id, "agent": agent})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PutDocument(*schema, id, body, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE profiles SET chief_member = 'm-chief' WHERE id = ?`, view.Manifest.ProfileID); err != nil {
		t.Fatal(err)
	}

	if err := migrateDBThrough(s.db, path, version); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"m-unset": "claude", "m-blank": "claude", "m-codex": "codex", "m-chief": ""} {
		doc, found, err := s.GetDocument(*schema, id)
		if err != nil || !found {
			t.Fatalf("read %s: %v found=%v", id, err, found)
		}
		var member crew.Member
		if err := json.Unmarshal(doc.Body, &member); err != nil {
			t.Fatal(err)
		}
		if member.Agent != want {
			t.Errorf("member %s runs %q after the upgrade, want %q", id, member.Agent, want)
		}
	}
	var advisor string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'garden.advisor' AND copied_from = ''`).Scan(&advisor); err != nil {
		t.Fatal(err)
	}
	if advisor != `{"agent":"codex"}` {
		t.Fatalf("the advisor setting is %q after the upgrade, want the Codex it ran on", advisor)
	}
}

func TestANewDatabaseLeavesTheAdvisorUnset(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if advisor := s.GetSetting("garden.advisor"); advisor != "" {
		t.Fatalf("a new database's advisor is %q, want it unset until the user picks a harness", advisor)
	}
}

func TestUpgradingKeepsAnAdvisorTheUserChose(t *testing.T) {
	const version = 1795338125260106
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, version-1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DefineDocumentCollection(crew.MembersSchema(), time.Now()); err != nil {
		t.Fatal(err)
	}
	chosen := `{"agent":"claude","model":"opus"}`
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('garden.advisor', ?)`, chosen); err != nil {
		t.Fatal(err)
	}
	if err := migrateDBThrough(s.db, path, version); err != nil {
		t.Fatal(err)
	}
	var advisor string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'garden.advisor' AND copied_from = ''`).Scan(&advisor); err != nil {
		t.Fatal(err)
	}
	if advisor != chosen {
		t.Fatalf("the advisor setting became %q, want the user's %q", advisor, chosen)
	}
}
