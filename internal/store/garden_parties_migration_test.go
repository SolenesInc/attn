package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/garden/events"
)

func TestGardenPartiesMigration(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprintf("declared=%v", declared), func(t *testing.T) {
			s := migrationFixtureStore(t, 1791638943956452-1)
			now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			if declared {
				profile, err := s.MostRecentlyUsedProfile()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`INSERT INTO crew_members(member_key,profile_id,name) VALUES('keel',?,'Keel')`, profile.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`INSERT INTO sessions(id,label,directory,state_since,state_updated_at,last_seen,profile_id,member_key,closed_at) VALUES ('bound','Keel','/tmp/crew','','','',?,'',''),('old-day','Keel','/tmp/crew','','','',?,'keel','closed'),('plain','Plain','/tmp/crew','','','',?,'','')`, profile.ID, profile.ID, profile.ID); err != nil {
					t.Fatal(err)
				}
				for _, schema := range []docstore.CollectionSchema{crew.MembersSchema(), {Namespace: garden.Namespace, Collection: garden.CollectionSeeds, Fields: []docstore.FieldSpec{{Name: "tender_session", Type: docstore.FieldString}}}, {Namespace: garden.Namespace, Collection: garden.CollectionNotes, Fields: []docstore.FieldSpec{{Name: "author_session", Type: docstore.FieldString}}}, garden.DispatchesSchema()} {
					if _, err := s.DefineDocumentCollection(schema, now); err != nil {
						t.Fatal(err)
					}
				}
				put := func(collection, id string, body map[string]any) {
					t.Helper()
					schema, _, err := s.DocumentCollection(map[bool]string{true: crew.Namespace, false: garden.Namespace}[collection == crew.CollectionMembers], collection)
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := s.PutDocument(*schema, id, encoded, now, nil); err != nil {
						t.Fatal(err)
					}
				}
				put(crew.CollectionMembers, "keel", map[string]any{"id": "keel", "binding_session": "bound"})
				for _, row := range []struct{ id, member, session string }{{"s-named1", "Bob", ""}, {"s-named2", "Bob", "plain"}, {"s-member", "KeEl", "bound"}, {"s-7k3f9m", "", "bound"}, {"s-closed", "", "old-day"}, {"s-plain1", "", "plain"}} {
					put(garden.CollectionSeeds, row.id, map[string]any{"id": row.id, "profile_id": profile.ID, "status": "growing", "tender_member": row.member, "tender_session": row.session, "planter_session": "bound", "harvest_when": map[string]any{"pull_request": "github.com:a/b#1", "url": "https://github.com/a/b/pull/1", "set_at": "now", "set_by_member": "attn"}})
				}
				put(garden.CollectionNotes, "n-legacy", map[string]any{"id": "n-legacy", "seed": "s-7k3f9m", "kind": "note", "body": "done", "author_member": "attn"})
				put(garden.CollectionDispatches, "delegate", map[string]any{"session_id": "delegate", "crown": "s-7k3f9m", "dispatcher_session": "bound"})
				if _, err := s.db.Exec(`INSERT INTO garden_seed_watches(watcher_session_id,seed_id,created_at) VALUES ('bound','s-7k3f9m','2026-10-01'),('old-day','s-7k3f9m','2026-10-02'),('plain','s-7k3f9m','2026-10-03'); INSERT INTO delegation_operations(request_id,operation_id,request_json,state,progress,session_id,created_at,updated_at,handover_tender_session,handover_tender_member) VALUES('req','op-req','{"source_session_id":"bound"}','accepted','','delegate','','','bound','keel'); INSERT INTO bus_events(name,subject,payload,created_at) VALUES('garden.seed.tended','s-7k3f9m','{"attention_requested":true,"caused_by_session_id":"bound","directly_notified_session_id":"plain"}','now'),('garden.seed.body.edited','s-7k3f9m','{}','now')`); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrateDB(s.db, s.dbPath); err != nil {
				t.Fatal(err)
			}
			if !declared {
				var count int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM document_collections WHERE namespace='core/garden' OR namespace='core/crew'`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("empty migration wrote collections: %d %v", count, err)
				}
				return
			}
			for _, schema := range []docstore.CollectionSchema{garden.SeedsSchema(), garden.NotesSchema()} {
				if _, err := s.DefineDocumentCollection(schema, now); err != nil {
					t.Fatal(err)
				}
			}
			seeds, _, err := s.DocumentCollection(garden.Namespace, garden.CollectionSeeds)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range []struct{ id, ref string }{{"s-named1", ""}, {"s-named2", ""}, {"s-member", "member:keel"}, {"s-7k3f9m", "member:keel"}, {"s-closed", "member:keel"}, {"s-plain1", "session:plain"}} {
				doc, found, err := s.GetDocument(*seeds, row.id)
				if err != nil || !found {
					t.Fatalf("read %s: %v", row.id, err)
				}
				seed, err := garden.Decode(doc.Body)
				if err != nil {
					t.Fatal(err)
				}
				tender, _ := seed.Claim.Tender()
				if tender.String() != row.ref || seed.Status != "growing" || seed.Planter.String() != "member:keel" || seed.HarvestWhen.SetBy.String() != "attn" || doc.Rev != 1 || !doc.UpdatedAt.Equal(now) {
					t.Fatalf("migrated %s: %+v %+v", row.id, seed, doc)
				}
			}
			_, _, err = s.DocumentCollection(garden.Namespace, garden.CollectionNotes)
			if err != nil {
				t.Fatal(err)
			}
			read, _, err := s.ReadQuery(docstore.Query{Namespace: garden.Namespace, Collection: garden.CollectionNotes})
			if err != nil {
				t.Fatal(err)
			}
			if len(read.Documents) != 3 {
				t.Fatalf("migration notes: %d", len(read.Documents))
			}
			for _, doc := range read.Documents {
				note, err := garden.DecodeNote(doc.Body)
				if err != nil || note.Author.String() != "attn" {
					t.Fatalf("note: %s %v", doc.Body, err)
				}
			}
			watches, err := s.GardenSeedWatches()
			if err != nil || len(watches) != 2 {
				t.Fatalf("watches: %+v %v", watches, err)
			}
			var oldest string
			if err := s.db.QueryRow(`SELECT created_at FROM garden_seed_watches WHERE watcher='member:keel'`).Scan(&oldest); err != nil || oldest != "2026-10-01" {
				t.Fatalf("oldest watch: %s %v", oldest, err)
			}
			op, err := s.GetDelegationOperation("req")
			if err != nil || op.Dispatcher.String() != "member:keel" || op.HandoverTender.String() != "member:keel" {
				t.Fatalf("operation: %+v %v", op, err)
			}
			model, _, err := events.BuildGardenModel()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.Query(`SELECT name,subject,payload FROM bus_events WHERE name LIKE 'garden.seed.%'`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var name, subject, payload string
				if err := rows.Scan(&name, &subject, &payload); err != nil {
					t.Fatal(err)
				}
				if _, err := model.Interpret(name, subject, []byte(payload)); err != nil {
					t.Fatalf("migrated event %s: %s %v", name, payload, err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
