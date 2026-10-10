package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func TestCrewIdentityMigrationPreservesKeysAndOpenBindings(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(map[bool]string{false: "undeclared", true: "bound"}[declared], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crew.db")
			old, err := newStoreAtVersion(path, 1791587735114907-1)
			if err != nil {
				t.Fatal(err)
			}
			if declared {
				p, err := old.MostRecentlyUsedProfile()
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{"keel", "chief", "chief-crew", "chief-crew-2", "s-7k3f9m", "s-7k3f9m-crew", "user"} {
					if _, err := old.db.Exec("INSERT INTO crew_profiles(member_id,profile_id) VALUES(?,?)", id, p.ID); err != nil {
						t.Fatal(err)
					}
				}
				sideID := "profile-side"
				if _, err := old.db.Exec("INSERT INTO profiles(id,name,created_at) VALUES(?, 'Side', 'now')", sideID); err != nil {
					t.Fatal(err)
				}
				if _, err := old.db.Exec("INSERT INTO crew_profiles(member_id,profile_id) VALUES('user-crew',?)", sideID); err != nil {
					t.Fatal(err)
				}
				if _, err := old.DefineDocumentCollection(crew.MembersSchema(), time.Now()); err != nil {
					t.Fatal(err)
				}
				schema, _, err := old.DocumentCollection(crew.Namespace, crew.CollectionMembers)
				if err != nil {
					t.Fatal(err)
				}
				for _, b := range []struct{ key, session, closed string }{{"keel", "open", ""}, {"chief", "closed", "2026-10-01T00:00:00Z"}} {
					if _, err := old.db.Exec(`INSERT INTO sessions(id,label,directory,state,state_since,state_updated_at,last_seen,closed_at) VALUES(?,?,'/tmp/crew','idle','','','',?)`, b.session, b.session, b.closed); err != nil {
						t.Fatal(err)
					}
					if _, err := old.PutDocument(*schema, b.key, []byte(`{"id":"`+b.key+`","binding_session":"`+b.session+`"}`), time.Now(), nil); err != nil {
						t.Fatal(err)
					}
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
			var count int
			if err := upgraded.db.QueryRow("SELECT COUNT(*) FROM crew_members").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if !declared {
				if count != 0 {
					t.Fatalf("empty migration made %d members", count)
				}
				return
			}
			for _, c := range []struct{ key, name, session string }{{"keel", "Keel", "open"}, {"chief", "chief-crew", ""}, {"chief-crew", "Chief-crew-3", ""}, {"chief-crew-2", "Chief-crew-2", ""}, {"s-7k3f9m", "s-7k3f9m-crew", ""}, {"s-7k3f9m-crew", "S-7k3f9m-crew-2", ""}, {"user", "user-crew", ""}, {"user-crew", "User-crew", ""}} {
				var name, latest string
				if err := upgraded.db.QueryRow("SELECT name,latest_session FROM crew_members WHERE member_key = ?", c.key).Scan(&name, &latest); err != nil {
					t.Fatal(err)
				}
				if err := crew.ValidateName(name); err != nil {
					t.Fatalf("%s migrated name %q: %v", c.key, name, err)
				}
				if name != c.name || latest != c.session {
					t.Fatalf("%s: %s %s", c.key, name, latest)
				}
			}
			if entry := upgraded.SessionLedgerEntry("open"); entry == nil || protocol.Deref(entry.MemberKey) != "keel" {
				t.Fatalf("open ledger: %+v", entry)
			}
			if entry := upgraded.SessionLedgerEntry("closed"); entry == nil || entry.MemberKey != nil {
				t.Fatalf("closed backfill: %+v", entry)
			}
			if _, err := upgraded.db.Exec("UPDATE crew_members SET name='KEEL' WHERE member_key='chief'"); err == nil {
				t.Fatal("case-variant duplicate accepted")
			}
			if _, err := upgraded.db.Exec("UPDATE profile_migration SET phase='launch_required'"); err != nil {
				t.Fatal(err)
			}
			var before, after int
			if err := upgraded.db.QueryRow("SELECT revision FROM profile_migration").Scan(&before); err != nil {
				t.Fatal(err)
			}
			key, _ := who.ParseMemberKey("keel")
			if _, err := upgraded.RenameCrewMember(key, "Alfred"); err != nil {
				t.Fatal(err)
			}
			if err := upgraded.db.QueryRow("SELECT revision FROM profile_migration").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before+1 {
				t.Fatalf("rename revision: %d -> %d", before, after)
			}
		})
	}
}
