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

func TestRestoreLegacyTicketIsCreateOnlyAndIdempotent(t *testing.T) {
	s := New()
	defer s.Close()
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	run, _, err := s.BeginLegacyTicketRecovery(LegacyTicketRecoveryVersion, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	candidate := LegacyTicketCandidate{
		Ticket: Ticket{
			ID: "recover-me", Title: "Recovered", Description: "body", Status: TicketStatusFailed,
			Assignee: "old-session", Cwd: "/repo", LastAgentID: "claude", ProjectID: "p",
			CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-24 * time.Hour),
			ClosedAt: timePtr(now.Add(-24 * time.Hour)), ArchivedAt: timePtr(now.Add(-23 * time.Hour)),
		},
		ResumeSessionID: "native-id",
		Activity:        []TicketActivity{{Kind: TicketActivityComment, Author: "agent", Comment: "receipt", CreatedAt: now.Add(-25 * time.Hour)}},
		Attachments:     []TicketAttachment{{Filename: "proof.md", Path: "/proof.md", Note: "proof", CreatedAt: now.Add(-25 * time.Hour)}},
	}
	candidate.Fingerprint = legacyTicketCandidateFingerprint(candidate)
	item := LegacyTicketRecoveryItem{Fingerprint: candidate.Fingerprint, RunVersion: run.Version, SourceKind: "database", SourceKey: "/snapshot", TicketID: candidate.Ticket.ID, CreatedAt: run.RecoveryAt}
	if got, err := s.RestoreLegacyTicket(candidate, item); err != nil || got != "recovered" {
		t.Fatalf("first restore = %q, %v", got, err)
	}
	first, err := s.GetTicket(candidate.Ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Activity) != 1 || len(first.Attachments) != 1 || first.Title != "Recovered" {
		t.Fatalf("restored ticket = %#v", first)
	}
	if got, err := s.RestoreLegacyTicket(candidate, item); err != nil || got != "recovered" {
		t.Fatalf("second restore = %q, %v", got, err)
	}
	second, _ := s.GetTicket(candidate.Ticket.ID)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("idempotent restore changed ticket:\nfirst=%#v\nsecond=%#v", first, second)
	}

	live, err := s.CreateTicket(Ticket{ID: "live-wins", Title: "Live", Description: "current"}, "you", now)
	if err != nil {
		t.Fatal(err)
	}
	liveCandidate := candidate
	liveCandidate.Ticket.ID = live.ID
	liveCandidate.Ticket.Title = "Backup must not win"
	liveCandidate.Fingerprint = legacyTicketCandidateFingerprint(liveCandidate)
	liveItem := item
	liveItem.Fingerprint, liveItem.TicketID = liveCandidate.Fingerprint, live.ID
	if got, err := s.RestoreLegacyTicket(liveCandidate, liveItem); err != nil || got != "live_won" {
		t.Fatalf("live collision = %q, %v", got, err)
	}
	after, _ := s.GetTicket(live.ID)
	if after.Title != "Live" || after.Description != "current" || after.Status != TicketStatusTodo {
		t.Fatalf("live row was overwritten: %#v", after)
	}
}

func TestRestoreLegacyTicketAttachmentIsAdditiveAndCreateOnly(t *testing.T) {
	s := New()
	defer s.Close()
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	if _, err := s.CreateTicket(Ticket{ID: "ticket-1", Title: "Ticket", Description: "original", Status: TicketStatusDone}, "you", now); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetTicket("ticket-1")
	if err != nil {
		t.Fatal(err)
	}
	row := TicketAttachment{
		TicketID: "ticket-1", Filename: "proof.md", Path: "/notebook/tickets/ticket-1/proof.md",
		Note: "Recovered from the legacy Notebook; SHA-256 abc", CreatedAt: now.Add(-time.Hour),
	}
	item := LegacyTicketRecoveryItem{
		Fingerprint: "notebook-one", RunVersion: LegacyTicketRecoveryVersion,
		SourceKind: "notebook", SourceKey: row.Path, TicketID: row.TicketID, CreatedAt: now,
	}
	if got, err := s.RestoreLegacyTicketAttachment(row, item); err != nil || got != "recovered" {
		t.Fatalf("restore = %q, %v", got, err)
	}
	if got, err := s.RestoreLegacyTicketAttachment(row, item); err != nil || got != "recovered" {
		t.Fatalf("rerun = %q, %v", got, err)
	}
	conflict := row
	conflict.Note = "different metadata must not win"
	conflictItem := item
	conflictItem.Fingerprint = "notebook-two"
	if got, err := s.RestoreLegacyTicketAttachment(conflict, conflictItem); err != nil || got != "live_won" {
		t.Fatalf("conflict = %q, %v", got, err)
	}
	after, err := s.GetTicket("ticket-1")
	if err != nil {
		t.Fatal(err)
	}
	if before.Title != after.Title || before.Description != after.Description || before.Status != after.Status ||
		!before.CreatedAt.Equal(after.CreatedAt) || !before.UpdatedAt.Equal(after.UpdatedAt) ||
		!reflect.DeepEqual(before.Activity, after.Activity) || len(after.Attachments) != 1 || after.Attachments[0].Note != row.Note {
		t.Fatalf("attachment recovery rewrote ticket data:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestLegacyTicketRecoveryRunFreezesInventoryAndWarnsOnce(t *testing.T) {
	s := New()
	defer s.Close()
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	inventory := []LegacyTicketRecoverySource{{Path: "/b.db", Family: "routine", Size: 2, ModTimeNS: 2, SHA256: "bb"}, {Path: "/a.db", Family: "premigration", Size: 1, ModTimeNS: 1, SHA256: "aa"}}
	run, created, err := s.BeginLegacyTicketRecovery(LegacyTicketRecoveryVersion, inventory, now)
	if err != nil || !created || run.State != LegacyTicketRecoveryRunning {
		t.Fatalf("begin = %#v created=%v err=%v", run, created, err)
	}
	again, created, err := s.BeginLegacyTicketRecovery(LegacyTicketRecoveryVersion, nil, now.Add(time.Hour))
	if err != nil || created || again.InventoryJSON != run.InventoryJSON || again.RecoveryAt != run.RecoveryAt {
		t.Fatalf("second begin = %#v created=%v err=%v", again, created, err)
	}
	warning := &NotificationRecord{Kind: "legacy", Severity: NotificationWarning, Title: "warning", Body: "body", SourceKind: "legacy", SourceID: "v1"}
	id, err := s.FinishLegacyTicketRecovery(run.Version, LegacyTicketRecoveryWarned, map[string]int{"recovered": 1}, "", warning, now.Add(time.Hour))
	if err != nil || id != "legacy-ticket-recovery-v2" {
		t.Fatalf("finish id=%q err=%v", id, err)
	}
	id2, err := s.FinishLegacyTicketRecovery(run.Version, LegacyTicketRecoveryWarned, nil, "different", warning, now.Add(2*time.Hour))
	if err != nil || id2 != id {
		t.Fatalf("second finish id=%q err=%v", id2, err)
	}
	notifications, err := s.ListNotifications()
	if err != nil || len(notifications) != 1 || notifications[0].ID != id {
		t.Fatalf("notifications=%#v err=%v", notifications, err)
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

func TestEnsureTicketSeedHandoverCreatesOnceAndKeepsTheOriginalLink(t *testing.T) {
	s, seeds, notes, dispatches := legacyTicketSeedTestStore(t)
	handover := ticketSeedHandover(t, seeds, notes, dispatches, "ticket-1", "s-first", "n-first")

	created, err := s.EnsureTicketSeedHandover(handover)
	if err != nil || created.Result != "created" || created.SeedID != "s-first" || len(created.Seqs) != 2 {
		t.Fatalf("create = %#v, %v", created, err)
	}
	events, err := s.BusEventsSince(0, 10)
	if err != nil || len(events) != 2 ||
		events[0].Subject != "core/garden/seeds/s-first" || events[1].Subject != "core/garden/notes/n-first" {
		t.Fatalf("document facts = %#v, %v", events, err)
	}
	seedBefore, found, err := s.GetDocument(seeds, "s-first")
	if err != nil || !found {
		t.Fatalf("seed after create: found=%v err=%v", found, err)
	}
	if _, found, err := s.GetDocument(notes, "n-first"); err != nil || !found {
		t.Fatalf("note after create: found=%v err=%v", found, err)
	}
	link, err := s.TicketSeedLink("ticket-1")
	if err != nil || link == nil || link.SeedID != "s-first" || link.OriginalTicketStatus != TicketStatusDone {
		t.Fatalf("link = %#v, %v", link, err)
	}

	again := ticketSeedHandover(t, seeds, notes, dispatches, "ticket-1", "s-second", "n-second")
	again.SeedDescription = "new data must not win"
	adopted, err := s.EnsureTicketSeedHandover(again)
	if err != nil || adopted.Result != "adopted_link" || adopted.SeedID != "s-first" {
		t.Fatalf("rerun = %#v, %v", adopted, err)
	}
	seedAfter, _, err := s.GetDocument(seeds, "s-first")
	if err != nil || !reflect.DeepEqual(seedBefore, seedAfter) {
		t.Fatalf("rerun changed the linked seed:\nbefore=%#v\nafter=%#v err=%v", seedBefore, seedAfter, err)
	}
	if _, found, err := s.GetDocument(seeds, "s-second"); err != nil || found {
		t.Fatalf("rerun created replacement seed: found=%v err=%v", found, err)
	}
	afterEvents, err := s.BusEventsSince(0, 10)
	if err != nil || len(afterEvents) != len(events) {
		t.Fatalf("rerun document facts = %#v, %v", afterEvents, err)
	}
}

func TestEnsureTicketSeedHandoverAdoptsOnlyExactMachineLineage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lineage  func(t *testing.T, s *Store, seeds, notes, dispatches docstore.CollectionSchema)
		sessions []string
	}{
		{
			name: "cutover note",
			lineage: func(t *testing.T, s *Store, seeds, notes, _ docstore.CollectionSchema) {
				putLegacySeedDocument(t, s, seeds, "s-existing", "Different existing title", "untouched")
				body, err := (garden.Note{ID: "n-lineage", Seed: "s-existing", Kind: garden.NoteKindNote,
					Body: "converted from backlog ticket `ticket-1` at the garden cutover; machine receipt"}).Encode()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.PutDocument(notes, "n-lineage", body, time.Now(), nil); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dispatch relationship",
			lineage: func(t *testing.T, s *Store, seeds, _ docstore.CollectionSchema, dispatches docstore.CollectionSchema) {
				putLegacySeedDocument(t, s, seeds, "s-existing", "Different existing title", "untouched")
				body, err := (garden.Dispatch{SessionID: "session-1", Crown: "s-existing"}).Encode()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.PutDocument(dispatches, "session-1", body, time.Now(), nil); err != nil {
					t.Fatal(err)
				}
			},
			sessions: []string{"session-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, seeds, notes, dispatches := legacyTicketSeedTestStore(t)
			tc.lineage(t, s, seeds, notes, dispatches)
			before, _, err := s.GetDocument(seeds, "s-existing")
			if err != nil {
				t.Fatal(err)
			}
			handover := ticketSeedHandover(t, seeds, notes, dispatches, "ticket-1", "s-proposed", "n-proposed")
			handover.SessionIDs = tc.sessions
			got, err := s.EnsureTicketSeedHandover(handover)
			if err != nil || got.Result != "adopted_lineage" || got.SeedID != "s-existing" {
				t.Fatalf("adopt = %#v, %v", got, err)
			}
			after, _, err := s.GetDocument(seeds, "s-existing")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("adoption changed existing seed:\nbefore=%#v\nafter=%#v err=%v", before, after, err)
			}
			if _, found, err := s.GetDocument(seeds, "s-proposed"); err != nil || found {
				t.Fatalf("adoption also created a seed: found=%v err=%v", found, err)
			}
		})
	}
}

func TestEnsureTicketSeedHandoverLeavesAmbiguityUntouched(t *testing.T) {
	t.Run("several lineage receipts", func(t *testing.T) {
		s, seeds, notes, dispatches := legacyTicketSeedTestStore(t)
		for _, id := range []string{"s-one", "s-two"} {
			putLegacySeedDocument(t, s, seeds, id, id, "existing")
			body, err := (garden.Note{ID: "n-" + id, Seed: id, Kind: garden.NoteKindNote,
				Body: "replanted from ticket `ticket-1`, exact machine receipt"}).Encode()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PutDocument(notes, "n-"+id, body, time.Now(), nil); err != nil {
				t.Fatal(err)
			}
		}
		handover := ticketSeedHandover(t, seeds, notes, dispatches, "ticket-1", "s-proposed", "n-proposed")
		got, err := s.EnsureTicketSeedHandover(handover)
		if err != nil || got.Result != "ambiguous_lineage" || got.SeedID != "" {
			t.Fatalf("ambiguity = %#v, %v", got, err)
		}
		assertNoLegacySeedCreation(t, s, seeds, "ticket-1", "s-proposed")
	})

	t.Run("same title and body without lineage", func(t *testing.T) {
		s, seeds, notes, dispatches := legacyTicketSeedTestStore(t)
		putLegacySeedDocument(t, s, seeds, "s-manual", "Recovered work", "the original brief")
		handover := ticketSeedHandover(t, seeds, notes, dispatches, "ticket-1", "s-proposed", "n-proposed")
		got, err := s.EnsureTicketSeedHandover(handover)
		if err != nil || got.Result != "ambiguous_content" || got.SeedID != "" {
			t.Fatalf("content ambiguity = %#v, %v", got, err)
		}
		assertNoLegacySeedCreation(t, s, seeds, "ticket-1", "s-proposed")
	})
}

func TestEnsureTicketSeedHandoverRollsBackEveryWriteOnAConflict(t *testing.T) {
	s, seeds, notes, dispatches := legacyTicketSeedTestStore(t)
	existing := []byte(`{"id":"n-conflict","seed":"s-other","kind":"note","body":"keep me","author_session":"","author_member":""}`)
	if _, err := s.PutDocument(notes, "n-conflict", existing, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	handover := ticketSeedHandover(t, seeds, notes, dispatches, "ticket-1", "s-proposed", "n-conflict")
	if _, err := s.EnsureTicketSeedHandover(handover); err == nil || !docstore.IsConflict(err) {
		t.Fatalf("conflicting note error = %v", err)
	}
	assertNoLegacySeedCreation(t, s, seeds, "ticket-1", "s-proposed")
	doc, found, err := s.GetDocument(notes, "n-conflict")
	if err != nil || !found || string(doc.Body) != string(existing) {
		t.Fatalf("rollback changed existing note: %#v found=%v err=%v", doc, found, err)
	}
	if events, err := s.BusEventsSince(0, 10); err != nil || len(events) != 0 {
		t.Fatalf("rolled-back document facts = %#v, %v", events, err)
	}
}

func putLegacySeedDocument(t *testing.T, s *Store, schema docstore.CollectionSchema, id, title, body string) {
	t.Helper()
	encoded, err := (garden.Seed{ID: id, Title: title, Body: body, Status: garden.StatusHarvested,
		StepSlug: garden.StepSlug(title), Edges: []garden.Edge{}, Vars: []garden.Var{}}).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutDocument(schema, id, encoded, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
}

func assertNoLegacySeedCreation(t *testing.T, s *Store, seeds docstore.CollectionSchema, ticketID, proposedSeedID string) {
	t.Helper()
	if _, found, err := s.GetDocument(seeds, proposedSeedID); err != nil || found {
		t.Fatalf("proposed seed exists: found=%v err=%v", found, err)
	}
	link, err := s.TicketSeedLink(ticketID)
	if err != nil || link != nil {
		t.Fatalf("ambiguous recovery link = %#v, %v", link, err)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
