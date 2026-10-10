package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChiefMembersMigration(t *testing.T) {
	for _, c := range []struct {
		name                                                       string
		previous, session, settings, deleted, legacy, mail, orphan bool
	}{
		{name: "empty"}, {name: "session with settings", previous: true, session: true, settings: true},
		{name: "session without settings", previous: true, session: true}, {name: "missing session", previous: true},
		{name: "deleted profile", previous: true, session: true, deleted: true}, {name: "legacy chief", legacy: true},
		{name: "read and unread mail", mail: true},
		{name: "deleted profile with mail", deleted: true, mail: true},
		{name: "absent profile with mail", orphan: true, mail: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := migrationFixtureStore(t, 1791645298598165-1)
			var profile string
			if err := s.db.QueryRow("SELECT id FROM profiles WHERE deleted_at = ''").Scan(&profile); err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := s.db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			if c.previous {
				exec("UPDATE profiles SET chief_session_id='old-chief' WHERE id=?", profile)
			}
			if c.deleted {
				exec("UPDATE profiles SET deleted_at='then' WHERE id=?", profile)
			}
			if c.session {
				intent, _ := json.Marshal(map[string]string{"model": "intent-model", "effort": "high"})
				exec("INSERT INTO sessions(id,label,directory,state_since,state_updated_at,last_seen,agent,launch_intent,profile_id) VALUES('old-chief','old','/tmp/chief','','','','codex',?,?)", string(intent), profile)
			}
			if c.settings {
				exec("INSERT INTO settings(key,value) VALUES('chief_model_codex','saved-model'),('chief_effort_codex','medium'),('chief_context_window_cap','160000')")
			}
			if c.legacy {
				exec("INSERT INTO crew_members(member_key,profile_id,name) VALUES('chief',?,'chief-crew')", profile)
			}
			mailProfile := profile
			if c.orphan {
				mailProfile = "missing-profile"
			}
			if c.mail {
				exec("INSERT INTO inbox_items(id,address,kind,created_at) VALUES('unread',?,'notice','now')", "chief:"+mailProfile)
				exec("INSERT INTO inbox_items(id,address,kind,created_at,notified_at,read_at) VALUES('read',?,'notice','now','now','then')", "chief:"+mailProfile)
				exec("INSERT INTO inbox_delivery(address,outstanding_at) VALUES(?,'now')", "chief:"+mailProfile)
			}
			if err := migrateDB(s.db, s.dbPath); err != nil {
				t.Fatal(err)
			}
			var chief string
			if err := s.db.QueryRow("SELECT chief_member FROM profiles WHERE id=?", profile).Scan(&chief); err != nil {
				t.Fatal(err)
			}
			if c.deleted {
				if chief != "" {
					t.Fatalf("deleted profile has chief %s", chief)
				}
			} else {
				if !strings.HasPrefix(chief, "m-") {
					t.Fatalf("chief key %q", chief)
				}
				var name, owner string
				if err := s.db.QueryRow("SELECT name,profile_id FROM crew_members WHERE member_key=?", chief).Scan(&name, &owner); err != nil {
					t.Fatal(err)
				}
				if name != "Chief" || owner != profile {
					t.Fatalf("chief %s in %s", name, owner)
				}
			}
			var upgrades int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM chief_upgrades").Scan(&upgrades); err != nil {
				t.Fatal(err)
			}
			wanted := 0
			if c.session && !c.deleted {
				wanted = 1
			}
			if upgrades != wanted {
				t.Fatalf("upgrades=%d, want %d", upgrades, wanted)
			}
			if wanted == 1 {
				var key, previous, agent, model, effort, cwd, label string
				if err := s.db.QueryRow("SELECT chief_member,previous_session,agent,model,effort,cwd FROM chief_upgrades").Scan(&key, &previous, &agent, &model, &effort, &cwd); err != nil {
					t.Fatal(err)
				}
				if err := s.db.QueryRow("SELECT label FROM sessions WHERE id='old-chief'").Scan(&label); err != nil {
					t.Fatal(err)
				}
				wantModel, wantEffort := "intent-model", "high"
				if c.settings {
					wantModel, wantEffort = "saved-model", "medium"
				}
				if key != chief || previous != "old-chief" || agent != "codex" || model != wantModel || effort != wantEffort || cwd != "/tmp/chief" || label != "Chief (previous)" {
					t.Fatalf("upgrade %q %q %q %q %q %q label %q", key, previous, agent, model, effort, cwd, label)
				}
			}
			if c.mail && !c.deleted && !c.orphan {
				var moved int
				if err := s.db.QueryRow("SELECT COUNT(*) FROM inbox_items WHERE address=?", "member:"+chief).Scan(&moved); err != nil {
					t.Fatal(err)
				}
				if moved != 2 {
					t.Fatalf("moved mail=%d", moved)
				}
				var address string
				if err := s.db.QueryRow("SELECT address FROM inbox_delivery").Scan(&address); err != nil {
					t.Fatal(err)
				}
				if address != "member:"+chief {
					t.Fatalf("delivery address %q", address)
				}
			}
			if c.mail && (c.deleted || c.orphan) {
				for _, table := range []string{"inbox_items", "inbox_delivery"} {
					var remaining int
					if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&remaining); err != nil {
						t.Fatal(err)
					}
					if remaining != 0 {
						t.Fatalf("%s kept %d rows for a removed profile", table, remaining)
					}
				}
			}

			var settings int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM settings WHERE key LIKE 'chief_%'").Scan(&settings); err != nil {
				t.Fatal(err)
			}
			if settings != 0 {
				t.Fatalf("chief settings=%d", settings)
			}
			if _, err := s.db.Exec("SELECT chief_session_id FROM profiles"); err == nil {
				t.Fatal("chief_session_id still exists")
			}
		})
	}
}
