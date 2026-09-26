package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
)

func writeLegacyTicketFixture(t *testing.T, path string, version int, ticketID string, automation bool) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	extra := ""
	if version >= 57 {
		extra += ", resume_session_id TEXT NOT NULL DEFAULT ''"
	}
	if version >= 60 {
		extra += ", reconciled_at TEXT NOT NULL DEFAULT ''"
	}
	if version >= 73 {
		extra += ", automation_run_id TEXT"
	}
	ddl := fmt.Sprintf(`
		CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations VALUES (%d, '2026-01-01T00:00:00Z');
		CREATE TABLE tickets (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, assignee TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '',
			last_agent_id TEXT NOT NULL DEFAULT '', project_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL, closed_at TEXT NOT NULL DEFAULT '',
			archived_at TEXT NOT NULL DEFAULT ''%s
		);
		CREATE TABLE ticket_activity (
			id INTEGER PRIMARY KEY AUTOINCREMENT, ticket_id TEXT NOT NULL, kind TEXT NOT NULL,
			author TEXT NOT NULL DEFAULT '', from_status TEXT NOT NULL DEFAULT '',
			to_status TEXT NOT NULL DEFAULT '', comment TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
		);
		CREATE TABLE ticket_attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT, ticket_id TEXT NOT NULL, filename TEXT NOT NULL,
			path TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
		);`, version, extra)
	if version >= 73 {
		ddl += `
			CREATE TABLE automation_runs(id TEXT PRIMARY KEY, ticket_id TEXT NOT NULL);
			CREATE TABLE automation_ticket_occurrence_events(run_id TEXT PRIMARY KEY, ticket_id TEXT NOT NULL);
			CREATE TABLE automation_continuity_bindings(definition_id TEXT, continuity_key TEXT, ticket_id TEXT NOT NULL);
		`
	}
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create schema %d fixture: %v", version, err)
	}
	columns := "id,title,description,status,assignee,cwd,last_agent_id,project_id,created_at,updated_at,closed_at,archived_at"
	values := "?,?,?,?,?,?,?,?,?,?,?,?"
	args := []any{ticketID, "A closed ticket", "original body", "done", "session-1", "/repo", "codex", "project",
		"2026-01-01T00:00:00Z", "2026-01-03T00:00:00Z", "2026-01-03T00:00:00Z", "2026-01-04T00:00:00Z"}
	if version >= 57 {
		columns += ",resume_session_id"
		values += ",?"
		args = append(args, "native-session")
	}
	if version >= 60 {
		columns += ",reconciled_at"
		values += ",?"
		args = append(args, "2026-01-05T00:00:00Z")
	}
	if version >= 73 {
		columns += ",automation_run_id"
		values += ",?"
		runID := any(nil)
		if automation {
			runID = "run-1"
		}
		args = append(args, runID)
	}
	if _, err := db.Exec(`INSERT INTO tickets (`+columns+`) VALUES (`+values+`)`, args...); err != nil {
		t.Fatalf("insert schema %d ticket: %v", version, err)
	}
	if _, err := db.Exec(`INSERT INTO ticket_activity
		(ticket_id,kind,author,from_status,to_status,comment,created_at)
		VALUES (?,'status_change','agent','working','done','finished','2026-01-03T00:00:00Z')`, ticketID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO ticket_attachments
		(ticket_id,filename,path,note,created_at)
		VALUES (?,'receipt.md','/old/receipt.md','proof','2026-01-03T00:00:00Z')`, ticketID); err != nil {
		t.Fatal(err)
	}
}

func TestReadLegacyTicketSnapshotAcrossShippedSchemas(t *testing.T) {
	for _, version := range []int{55, 57, 60, 73, LatestSchemaVersion()} {
		t.Run(fmt.Sprintf("schema-%d", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.db")
			writeLegacyTicketFixture(t, path, version, fmt.Sprintf("ticket-v%d", version), version == 73)
			before, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			read, err := ReadLegacyTicketSnapshot(path)
			if err != nil {
				t.Fatalf("ReadLegacyTicketSnapshot: %v", err)
			}
			if read.SchemaVersion != version || len(read.Candidates) != 1 || len(read.Warnings) != 0 {
				t.Fatalf("read = %#v", read)
			}
			candidate := read.Candidates[0]
			if candidate.Ticket.Status != TicketStatusDone || len(candidate.Activity) != 1 || len(candidate.Attachments) != 1 {
				t.Fatalf("candidate = %#v", candidate)
			}
			if (version >= 57) != (candidate.ResumeSessionID == "native-session") {
				t.Fatalf("schema %d resume id = %q", version, candidate.ResumeSessionID)
			}
			if (version >= 60) != (candidate.Ticket.ReconciledAt != nil) {
				t.Fatalf("schema %d reconciled = %v", version, candidate.Ticket.ReconciledAt)
			}
			if candidate.AutomationOwned != (version == 73) {
				t.Fatalf("schema %d automation = %v", version, candidate.AutomationOwned)
			}
			after, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("immutable read created sidecars: before=%v after=%v", before, after)
			}
		})
	}
}

func TestReadLegacyTicketSnapshotRejectsFutureAndInconsistentSchemas(t *testing.T) {
	future := filepath.Join(t.TempDir(), "future.db")
	writeLegacyTicketFixture(t, future, LatestSchemaVersion()+1, "future-ticket", false)
	if _, err := ReadLegacyTicketSnapshot(future); err == nil || !strings.Contains(err.Error(), "future schema") {
		t.Fatalf("future schema error = %v", err)
	}

	broken := filepath.Join(t.TempDir(), "broken.db")
	writeLegacyTicketFixture(t, broken, 55, "broken-ticket", false)
	db, err := sql.Open("sqlite3", broken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET version=60`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := ReadLegacyTicketSnapshot(broken); err == nil || !strings.Contains(err.Error(), "tickets.") {
		t.Fatalf("inconsistent schema error = %v", err)
	}
}

func legacyTicketSeedTestStore(t *testing.T) (*Store, docstore.CollectionSchema, docstore.CollectionSchema, docstore.CollectionSchema) {
	t.Helper()
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	for _, schema := range []docstore.CollectionSchema{garden.SeedsSchema(), garden.NotesSchema(), garden.DispatchesSchema()} {
		if _, err := s.DefineDocumentCollection(schema, time.Now()); err != nil {
			t.Fatalf("define %s: %v", schema.Collection, err)
		}
	}
	return s,
		declOf(t, s, garden.Namespace, garden.CollectionSeeds),
		declOf(t, s, garden.Namespace, garden.CollectionNotes),
		declOf(t, s, garden.Namespace, garden.CollectionDispatches)
}

func ticketSeedHandover(t *testing.T, seedSchema, noteSchema, dispatchSchema docstore.CollectionSchema, ticketID, seedID, noteID string) TicketSeedHandover {
	t.Helper()
	seed := garden.Seed{
		ID: seedID, Title: "Recovered work", Body: "the original brief", Status: garden.StatusHarvested,
		StepSlug: "recovered-work", Edges: []garden.Edge{}, Vars: []garden.Var{}, Reason: "recovered from legacy ticket " + ticketID,
	}
	seedBody, err := seed.Encode()
	if err != nil {
		t.Fatal(err)
	}
	noteBody, err := (garden.Note{ID: noteID, Seed: seedID, Kind: garden.NoteKindNote, Body: "recovery provenance"}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	return TicketSeedHandover{
		TicketID: ticketID, SeedID: seedID, SeedBody: seedBody,
		SeedFact:  BusEvent{Name: "document.changed", Subject: docstore.Address(garden.Namespace, garden.CollectionSeeds, seedID), Payload: `{}`},
		SeedTitle: seed.Title, SeedDescription: seed.Body,
		SeedSchema: seedSchema, NoteSchema: noteSchema, DispatchSchema: dispatchSchema,
		Notes: []TicketSeedNote{{
			ID: noteID, Body: noteBody,
			Fact: BusEvent{Name: "document.changed", Subject: docstore.Address(garden.Namespace, garden.CollectionNotes, noteID), Payload: `{}`},
		}},
		HandoverKind: "database", EvidenceFingerprint: "fingerprint-" + ticketID,
		OriginalTicketStatus: TicketStatusDone, CreatedAt: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
	}
}

type ticketSeedGarden struct {
	store                    *Store
	seeds, notes, dispatches docstore.CollectionSchema
	existing                 []ticketSeedDocument
}

type ticketSeedDocument struct {
	schema docstore.CollectionSchema
	id     string
}

func (g *ticketSeedGarden) put(t *testing.T, schema docstore.CollectionSchema, id string, body []byte) {
	t.Helper()
	if _, err := g.store.PutDocument(schema, id, body, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	g.existing = append(g.existing, ticketSeedDocument{schema, id})
}

func (g *ticketSeedGarden) seed(t *testing.T, id, title, body string) {
	t.Helper()
	encoded, err := (garden.Seed{ID: id, Title: title, Body: body, Status: garden.StatusHarvested,
		StepSlug: garden.StepSlug(title), Edges: []garden.Edge{}, Vars: []garden.Var{}}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	g.put(t, g.seeds, id, encoded)
}

func (g *ticketSeedGarden) note(t *testing.T, id, seedID, body string) {
	t.Helper()
	encoded, err := (garden.Note{ID: id, Seed: seedID, Kind: garden.NoteKindNote, Body: body}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	g.put(t, g.notes, id, encoded)
}

func TestEnsureTicketSeedHandoverDecidesOnceOverThePriorGarden(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prior    func(t *testing.T, g *ticketSeedGarden)
		sessions []string
		result   string
		seedID   string
		conflict bool
	}{
		{name: "empty garden", result: "created", seedID: "s-proposed"},
		{
			name: "earlier handover of the same ticket",
			prior: func(t *testing.T, g *ticketSeedGarden) {
				earlier := ticketSeedHandover(t, g.seeds, g.notes, g.dispatches, "ticket-1", "s-first", "n-first")
				if got, err := g.store.EnsureTicketSeedHandover(earlier); err != nil || got.Result != "created" {
					t.Fatalf("earlier handover = %#v, %v", got, err)
				}
				g.existing = append(g.existing, ticketSeedDocument{g.seeds, "s-first"}, ticketSeedDocument{g.notes, "n-first"})
			},
			result: "adopted_link", seedID: "s-first",
		},
		{
			name: "cutover note naming the ticket",
			prior: func(t *testing.T, g *ticketSeedGarden) {
				g.seed(t, "s-existing", "Different existing title", "untouched")
				g.note(t, "n-lineage", "s-existing", "converted from backlog ticket `ticket-1` at the garden cutover; machine receipt")
			},
			result: "adopted_lineage", seedID: "s-existing",
		},
		{
			name: "dispatch of one of the ticket's sessions",
			prior: func(t *testing.T, g *ticketSeedGarden) {
				g.seed(t, "s-existing", "Different existing title", "untouched")
				body, err := (garden.Dispatch{SessionID: "session-1", Crown: "s-existing"}).Encode()
				if err != nil {
					t.Fatal(err)
				}
				g.put(t, g.dispatches, "session-1", body)
			},
			sessions: []string{"session-1"},
			result:   "adopted_lineage", seedID: "s-existing",
		},
		{
			name: "several lineage receipts",
			prior: func(t *testing.T, g *ticketSeedGarden) {
				for _, id := range []string{"s-one", "s-two"} {
					g.seed(t, id, id, "existing")
					g.note(t, "n-"+id, id, "replanted from ticket `ticket-1`, exact machine receipt")
				}
			},
			result: "ambiguous_lineage",
		},
		{
			name: "same title and body without lineage",
			prior: func(t *testing.T, g *ticketSeedGarden) {
				g.seed(t, "s-manual", "Recovered work", "the original brief")
			},
			result: "ambiguous_content",
		},
		{
			name: "a different note already holds the proposed note id",
			prior: func(t *testing.T, g *ticketSeedGarden) {
				g.put(t, g.notes, "n-proposed", []byte(`{"id":"n-proposed","seed":"s-other","kind":"note","body":"keep me","author_session":"","author_member":""}`))
			},
			conflict: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, seeds, notes, dispatches := legacyTicketSeedTestStore(t)
			g := &ticketSeedGarden{store: s, seeds: seeds, notes: notes, dispatches: dispatches}
			if tc.prior != nil {
				tc.prior(t, g)
			}
			before := make([]*docstore.Document, len(g.existing))
			for i, doc := range g.existing {
				stored, _, err := s.GetDocument(doc.schema, doc.id)
				if err != nil {
					t.Fatal(err)
				}
				before[i] = stored
			}
			factsBefore, err := s.BusEventsSince(0, 100)
			if err != nil {
				t.Fatal(err)
			}

			handover := ticketSeedHandover(t, seeds, notes, dispatches, "ticket-1", "s-proposed", "n-proposed")
			handover.SessionIDs = tc.sessions
			got, err := s.EnsureTicketSeedHandover(handover)
			switch {
			case tc.conflict:
				if err == nil || !docstore.IsConflict(err) {
					t.Fatalf("handover = %#v, %v; want a conflict", got, err)
				}
			case err != nil || got.Result != tc.result || got.SeedID != tc.seedID:
				t.Fatalf("handover = %#v, %v; want %s of %q", got, err, tc.result, tc.seedID)
			}

			for i, doc := range g.existing {
				after, found, err := s.GetDocument(doc.schema, doc.id)
				if err != nil || !found || !reflect.DeepEqual(before[i], after) {
					t.Errorf("existing %s changed:\nbefore=%#v\nafter=%#v found=%v err=%v", doc.id, before[i], after, found, err)
				}
			}
			facts, err := s.BusEventsSince(0, 100)
			if err != nil {
				t.Fatal(err)
			}
			newFacts := facts[len(factsBefore):]
			link, linkErr := s.TicketSeedLink("ticket-1")
			if linkErr != nil {
				t.Fatal(linkErr)
			}
			_, proposedExists, err := s.GetDocument(seeds, "s-proposed")
			if err != nil {
				t.Fatal(err)
			}

			if tc.result == "created" {
				if len(newFacts) != 2 || newFacts[0].Subject != "core/garden/seeds/s-proposed" || newFacts[1].Subject != "core/garden/notes/n-proposed" {
					t.Errorf("created with facts %#v, want the seed and its note", newFacts)
				}
				if _, found, err := s.GetDocument(notes, "n-proposed"); err != nil || !found {
					t.Errorf("the provenance note was not written: found=%v err=%v", found, err)
				}
			} else {
				if len(newFacts) != 0 || proposedExists {
					t.Errorf("a %s handover wrote facts %#v and created the proposed seed: %v", tc.name, newFacts, proposedExists)
				}
			}
			switch tc.result {
			case "created", "adopted_link":
				if link == nil || link.SeedID != tc.seedID || link.OriginalTicketStatus != TicketStatusDone {
					t.Errorf("link = %#v, want ticket-1 tied to %s with its original status", link, tc.seedID)
				}
			case "ambiguous_lineage", "ambiguous_content", "":
				if link != nil {
					t.Errorf("link = %#v, want none", link)
				}
			}
		})
	}
}
