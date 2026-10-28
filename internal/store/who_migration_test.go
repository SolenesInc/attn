package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func TestWhoMigrationPreservesPartiesActorsAndCollisionWinners(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty undeclared crew", true: "attributed history"}[declared], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "who.db")
			old, err := newStoreAtVersion(path, 1793111475755305-1)
			if err != nil {
				t.Fatal(err)
			}
			if declared {
				if _, err := old.DefineDocumentCollection(crew.MembersSchema(), time.Now()); err != nil {
					t.Fatal(err)
				}
				schema, _, err := old.DocumentCollection(crew.Namespace, crew.CollectionMembers)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := old.db.Exec(`
INSERT INTO sessions(id,user_label,directory,created_at,member_key,closed_at,closed_by) VALUES
 ('bound','Keel','/tmp/who','','','',''),
 ('old-day','Keel','/tmp/who','','keel','2026-10-01T00:00:00Z','bound'),
 ('plain','Plain','/tmp/who','','','2026-10-01T00:00:00Z','user'),
 ('unknown-close','Unknown','/tmp/who','','','2026-10-01T00:00:00Z','');
INSERT INTO peer_messages(id,sender_session_id,body,created_at) VALUES
 ('bound-peer','bound','body','2026-10-01T00:00:00Z'),('old-peer','old-day','body','2026-10-01T00:00:00Z'),('plain-peer','plain','body','2026-10-01T00:00:00Z');
INSERT INTO inbox_items(id,address,kind,source_id,created_at) VALUES
 ('bound-peer','member:keel','peer_message','bound-peer','2026-10-01T00:00:00Z'),
 ('old-peer','session:plain','peer_message','old-peer','2026-10-01T00:00:00Z'),
 ('plain-peer','chief:profile','peer_message','plain-peer','2026-10-01T00:00:00Z');
INSERT INTO pull_request_watches(address,session_id,pr_id,mode,created_at) VALUES
 ('session:bound','bound','pr','codex','2026-10-01T00:00:00Z'),
 ('member:keel','bound','pr','codex','2026-10-02T00:00:00Z'),
 ('chief:profile','plain','chief-pr','codex','');
INSERT INTO automode_proposals(kind,value,proposed_by,created_at) VALUES
 ('host','same','old-day','2026-10-01T00:00:00Z'),('host','same','bound','2026-10-02T00:00:00Z'),('host','legacy','','2026-10-01T00:00:00Z'),('host','pi display','pi session Keel','2026-10-01T00:00:00Z');
INSERT INTO kept_conversations(resume_id,agent,source_path,bytes,stored_bytes,copied_at,deleted_at,deleted_by) VALUES
 ('swept','claude','/tmp/source',0,0,'2026-10-01T00:00:00Z','2026-10-02T00:00:00Z','sweep'),
 ('forgotten','claude','/tmp/source',0,0,'2026-10-01T00:00:00Z','2026-10-02T00:00:00Z','user');
`); err != nil {
					t.Fatal(err)
				}
				if _, err := old.PutDocument(*schema, "keel", []byte(`{"id":"keel","binding_session":"bound"}`), time.Now(), nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := newStoreAtVersion(path, 1793111475839445)
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			if !declared {
				return
			}
			before := upgraded.SessionLedgerEntry("unknown-close")
			if before == nil || before.ClosedBy != nil {
				t.Fatalf("unknown close actor: %+v", before)
			}
			if err := upgraded.ReopenSession("unknown-close"); err != nil {
				t.Fatalf("reopen unknown actor: %v", err)
			}
			after := upgraded.SessionLedgerEntry("unknown-close")
			if after == nil || after.ClosedBy != nil || after.ClosedAt != nil {
				t.Fatalf("reopened unknown actor: %+v", after)
			}
			for _, row := range []struct{ id, party, address string }{{"bound-peer", "member:keel", "member:keel"}, {"old-peer", "member:keel", "session:plain"}, {"plain-peer", "session:plain", "chief:profile"}} {
				var sender, address string
				if err := upgraded.db.QueryRow("SELECT p.sender,i.address FROM peer_messages p JOIN inbox_items i ON i.source_id=p.id AND i.kind='peer_message' WHERE p.id=?", row.id).Scan(&sender, &address); err != nil || sender != row.party || address != row.address {
					t.Fatalf("peer %s: sender=%q address=%q err=%v", row.id, sender, address, err)
				}
			}
			key, _ := who.ParseMemberKey("keel")
			watch, ok := upgraded.PullRequestWatch(who.Member(key).Address(), "pr")
			if !ok || watch.CreatedAt != "2026-10-02T00:00:00Z" {
				t.Fatalf("watch collision: %+v", watch)
			}
			watch, ok = upgraded.PullRequestWatch(who.ToSession("plain"), "chief-pr")
			if !ok || watch.Watcher.String() != "session:plain" {
				t.Fatalf("chief watch: %+v", watch)
			}
			for _, row := range []struct{ id, actor string }{{"old-day", "session:bound"}, {"plain", "user"}} {
				entry := upgraded.SessionLedgerEntry(protocol.SessionID(row.id))
				if entry == nil || entry.ClosedBy == nil || string(entry.ClosedBy.Ref) != row.actor {
					t.Fatalf("ledger %s: %+v", row.id, entry)
				}
			}
			proposals, err := upgraded.ListAutoModeProposals("")
			if err != nil {
				t.Fatal(err)
			}
			if len(proposals) != 3 || proposals[0].ProposedBy.String() != "member:keel" || !proposals[1].ProposedBy.IsZero() || !proposals[2].ProposedBy.IsZero() {
				t.Fatalf("proposal collision: %+v", proposals)
			}
			for _, row := range []struct {
				id    string
				actor who.Actor
			}{{"swept", who.Attn()}, {"forgotten", who.User()}} {
				kept, ok := upgraded.KeptConversation("claude", row.id)
				if !ok || kept.DeletedBy != row.actor {
					t.Fatalf("kept %s: %+v", row.id, kept)
				}
			}
		})
	}
}

func TestChiefEpicUpgradesTheLatestMainDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	all := timestampedMigrations
	defer func() { timestampedMigrations = all }()
	epic := map[int]bool{
		1793111475126440: true, 1793111475404723: true,
		1793111475500692: true, 1793111475583214: true,
		1793111475670935: true, 1793111475755305: true,
		1793111475839445: true, 1793111475923199: true,
		1793111476009795: true, 1793111476093878: true,
		1793111476175147: true, 1793111476257370: true,
		1793111476342423: true, 1793111476425448: true,
	}
	timestampedMigrations = nil
	for _, m := range all {
		if !epic[m.version] {
			timestampedMigrations = append(timestampedMigrations, m)
		}
	}
	old, _, err := openLegacySchemaStore(path)
	timestampedMigrations = all
	if err != nil {
		t.Fatal(err)
	}
	profile := migrationFixtureProfile(t, old, "")
	if _, err := old.db.Exec("INSERT INTO crew_profiles(member_id,profile_id) VALUES('keel',?)", profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO sessions(id,user_label,directory,profile_id,created_at) VALUES ('day','Keel','/tmp/who',?,'')`, profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO pull_request_watches(address,session_id,pr_id,mode,created_at,cursor_json) VALUES
		('session:day','day','pr','codex','2026-10-01T00:00:00Z','{"head_sha":"older"}'),
		('member:keel','day','pr','codex','2026-10-02T00:00:00Z','{"head_sha":"newer"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.DefineDocumentCollection(crew.MembersSchema(), time.Now()); err != nil {
		t.Fatal(err)
	}
	schema, _, err := old.DocumentCollection(crew.Namespace, crew.CollectionMembers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.PutDocument(*schema, "keel", []byte(`{"id":"keel","binding_session":"day"}`), time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 10, 25, 1, 2, 3, 0, time.UTC)
	if _, err := old.DefineDocumentCollection(garden.SeedsSchema(), stamp); err != nil {
		t.Fatal(err)
	}
	seeds, _, err := old.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"id": "s-upgrade", "profile_id": profile.ID, "status": "planted", "planter_session": "day",
		"check_results": map[string]any{
			"s-plot/C1": map[string]any{"report": map[string]any{"summary": "Saved proof", "commit": "original-head", "by_session": "day", "at": "report-time"}},
			"C2":        map[string]any{"verdict": map[string]any{"confirmed": true, "at": "verdict-time"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	migrationFixtureDocument(t, old, *seeds, "s-upgrade", body, stamp)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	key, err := who.ParseMemberKey("keel")
	if err != nil {
		t.Fatal(err)
	}
	watch, found := upgraded.PullRequestWatch(who.Member(key).Address(), "pr")
	if !found || watch.CreatedAt != "2026-10-02T00:00:00Z" || watch.Cursor.HeadSHA != "newer" {
		t.Fatalf("member watch after main upgrade: %+v, found=%v", watch, found)
	}
	entry := upgraded.SessionLedgerEntry("day")
	if entry == nil || protocol.Deref(entry.MemberKey) != "keel" || protocol.Deref(entry.MemberName) != "Keel" {
		t.Fatalf("crew session after main upgrade: %+v", entry)
	}
	seeds, _, err = upgraded.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
	if err != nil {
		t.Fatal(err)
	}
	doc, found, err := upgraded.GetDocument(*seeds, "s-upgrade")
	if err != nil || !found {
		t.Fatalf("check history after main upgrade: found=%v, %v", found, err)
	}
	seed, err := garden.Decode(doc.Body)
	if err != nil {
		t.Fatal(err)
	}
	report, verdict := seed.CheckResults["s-plot/C1"].Report, seed.CheckResults["C2"].Verdict
	if report == nil || report.By.String() != "member:keel" || report.Summary != "Saved proof" || report.Commit != "original-head" || report.At != "report-time" || verdict == nil || verdict.By.String() != "user" || !verdict.Confirmed || verdict.At != "verdict-time" || doc.Rev != 1 || !doc.UpdatedAt.Equal(stamp) {
		t.Fatalf("check history after main upgrade: report=%+v verdict=%+v doc=%+v", report, verdict, doc)
	}
	if chief, err := upgraded.ProfileChief(profile.ID); err != nil || chief.IsZero() {
		t.Fatalf("profile chief after main upgrade: %v, %v", chief, err)
	}
}
