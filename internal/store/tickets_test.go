package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ticketBase is a fixed clock for deterministic, injected-time tests.
var ticketBase = time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)

func TestTicketCRUDRoundTrip(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	var maxVersion int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if maxVersion != latestSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", maxVersion, latestSchemaVersion())
	}

	created, err := s.CreateTicket(Ticket{
		ID:          "store-migration",
		Title:       "Migrate store to X",
		Description: "Move the store onto the new backend.",
		Assignee:    "agent7",
		Cwd:         "/tmp/project",
		LastAgentID: "agent7",
	}, "chief", ticketBase)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.Status != TicketStatusTodo {
		t.Fatalf("default status = %q, want todo", created.Status)
	}
	if created.ClosedAt != nil {
		t.Fatalf("ClosedAt = %v, want nil for a fresh todo", created.ClosedAt)
	}

	got, err := s.GetTicket("store-migration")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got == nil {
		t.Fatal("GetTicket = nil, want ticket")
	}
	if got.Title != "Migrate store to X" || got.Description != "Move the store onto the new backend." {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Assignee != "agent7" || got.Cwd != "/tmp/project" || got.LastAgentID != "agent7" {
		t.Fatalf("round-trip mismatch on session fields: %+v", got)
	}
	if !got.CreatedAt.Equal(ticketBase) || !got.UpdatedAt.Equal(ticketBase) {
		t.Fatalf("timestamps = created %v / updated %v, want %v", got.CreatedAt, got.UpdatedAt, ticketBase)
	}

	missing, err := s.GetTicket("nope")
	if err != nil || missing != nil {
		t.Fatalf("GetTicket(missing) = %v, %v; want nil, nil", missing, err)
	}
}

func TestTicketValidation(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "Has Spaces", Title: "x"}, "you", ticketBase); !errors.Is(err, ErrInvalidTicketID) {
		t.Fatalf("invalid id err = %v, want ErrInvalidTicketID", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "no-title"}, "you", ticketBase); !errors.Is(err, ErrTicketTitleRequired) {
		t.Fatalf("missing title err = %v, want ErrTicketTitleRequired", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "bad-status", Title: "x", Status: "weird"}, "you", ticketBase); !errors.Is(err, ErrInvalidTicketStatus) {
		t.Fatalf("bad status err = %v, want ErrInvalidTicketStatus", err)
	}
}

func TestTicketIDCollision(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "dup", Title: "first"}, "you", ticketBase); err != nil {
		t.Fatalf("first CreateTicket: %v", err)
	}
	_, err := s.CreateTicket(Ticket{ID: "dup", Title: "second"}, "you", ticketBase)
	if !errors.Is(err, ErrTicketIDTaken) {
		t.Fatalf("collision err = %v, want ErrTicketIDTaken", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "pick a new name") || !strings.Contains(msg, "dup-2") {
		t.Fatalf("collision message lacks guidance: %q", msg)
	}
	got, _ := s.GetTicket("dup")
	if got == nil || got.Title != "first" {
		t.Fatalf("original overwritten: %+v", got)
	}
}

func TestTicketStatusTransitions(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "you", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	t1 := ticketBase.Add(1 * time.Minute)
	if _, err := s.SetTicketStatus("tk", TicketStatusWorking, "agent7", "picking it up", t1); err != nil {
		t.Fatalf("SetTicketStatus working: %v", err)
	}

	t2 := ticketBase.Add(2 * time.Minute)
	got, err := s.SetTicketStatus("tk", TicketStatusDone, "agent7", "shipped", t2)
	if err != nil {
		t.Fatalf("SetTicketStatus done: %v", err)
	}
	if got.ClosedAt == nil || !got.ClosedAt.Equal(t2) {
		t.Fatalf("ClosedAt = %v, want %v", got.ClosedAt, t2)
	}

	t3 := ticketBase.Add(3 * time.Minute)
	got, err = s.SetTicketStatus("tk", TicketStatusWorking, "you", "more to do", t3)
	if err != nil {
		t.Fatalf("SetTicketStatus reopen: %v", err)
	}
	if got.ClosedAt != nil {
		t.Fatalf("ClosedAt = %v, want nil after reopen", got.ClosedAt)
	}

	full, err := s.GetTicket("tk")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if len(full.Activity) != 3 {
		t.Fatalf("activity len = %d, want 3", len(full.Activity))
	}
	wantFrom := []TicketStatus{TicketStatusTodo, TicketStatusWorking, TicketStatusDone}
	wantTo := []TicketStatus{TicketStatusWorking, TicketStatusDone, TicketStatusWorking}
	for i, a := range full.Activity {
		if a.Kind != TicketActivityStatusChange {
			t.Fatalf("activity[%d].Kind = %q, want status_change", i, a.Kind)
		}
		if a.FromStatus != wantFrom[i] || a.ToStatus != wantTo[i] {
			t.Fatalf("activity[%d] = %s->%s, want %s->%s", i, a.FromStatus, a.ToStatus, wantFrom[i], wantTo[i])
		}
	}
	if full.Activity[1].Comment != "shipped" {
		t.Fatalf("done comment = %q, want shipped", full.Activity[1].Comment)
	}

	if _, err := s.SetTicketStatus("ghost", TicketStatusDone, "", "", t3); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("status on missing = %v, want ErrTicketNotFound", err)
	}
	if _, err := s.SetTicketStatus("tk", "bogus", "", "", t3); !errors.Is(err, ErrInvalidTicketStatus) {
		t.Fatalf("bad status = %v, want ErrInvalidTicketStatus", err)
	}
}

func TestTicketCommentsAndEdits(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "you", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	t1 := ticketBase.Add(1 * time.Minute)
	if _, err := s.AddTicketComment("tk", "you", "any update?", t1); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}
	t2 := ticketBase.Add(2 * time.Minute)
	if _, err := s.AddTicketComment("tk", "agent7", "almost there", t2); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}
	if err := s.EditTicketDescription("tk", "Revised brief.", "you", t2); err != nil {
		t.Fatalf("EditTicketDescription: %v", err)
	}
	if err := s.AssignTicket("tk", "agent9", "you", t2); err != nil {
		t.Fatalf("AssignTicket: %v", err)
	}
	if err := s.SetTicketSession("tk", "/repo", "agent9", t2); err != nil {
		t.Fatalf("SetTicketSession: %v", err)
	}

	got, err := s.GetTicket("tk")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Description != "Revised brief." || got.Assignee != "agent9" || got.Cwd != "/repo" || got.LastAgentID != "agent9" {
		t.Fatalf("edits not applied: %+v", got)
	}
	if !got.UpdatedAt.Equal(t2) {
		t.Fatalf("UpdatedAt = %v, want %v (bumped by edits)", got.UpdatedAt, t2)
	}
	if len(got.Activity) != 2 {
		t.Fatalf("activity len = %d, want 2 comments", len(got.Activity))
	}
	if got.Activity[0].Kind != TicketActivityComment || got.Activity[0].Comment != "any update?" {
		t.Fatalf("activity[0] = %+v, want first comment", got.Activity[0])
	}
	if got.Activity[1].Author != "agent7" {
		t.Fatalf("activity[1].Author = %q, want agent7", got.Activity[1].Author)
	}

	if err := s.EditTicketDescription("ghost", "x", "you", t2); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("edit missing = %v, want ErrTicketNotFound", err)
	}
	if _, err := s.AddTicketComment("ghost", "", "x", t2); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("comment missing = %v, want ErrTicketNotFound", err)
	}
}

func TestTicketAttachments(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "tk", Title: "work"}, "you", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	t1 := ticketBase.Add(1 * time.Minute)
	att, err := s.AddTicketAttachment(TicketAttachment{
		TicketID: "tk",
		Filename: "results.json",
		Path:     "/attach/results.json",
		Note:     "benchmark output",
	}, "agent7", t1)
	if err != nil {
		t.Fatalf("AddTicketAttachment: %v", err)
	}
	if att.ID == 0 {
		t.Fatal("attachment id = 0, want assigned id")
	}

	got, err := s.GetTicket("tk")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments len = %d, want 1", len(got.Attachments))
	}
	if got.Attachments[0].Filename != "results.json" || got.Attachments[0].Note != "benchmark output" {
		t.Fatalf("attachment round-trip = %+v", got.Attachments[0])
	}
	if !got.UpdatedAt.Equal(t1) {
		t.Fatalf("UpdatedAt = %v, want %v (bumped by attach)", got.UpdatedAt, t1)
	}

	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "tk"}, "agent7", t1); err == nil {
		t.Fatal("AddTicketAttachment with no filename: want error")
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "ghost", Filename: "x"}, "agent7", t1); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("attach to missing = %v, want ErrTicketNotFound", err)
	}
}

func TestTicketListAndArchive(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "backlog", Title: "later"}, "you", ticketBase); err != nil {
		t.Fatalf("create backlog: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "active", Title: "now", Status: TicketStatusWorking}, "you", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("create active: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "shipped", Title: "old", Status: TicketStatusDone}, "you", ticketBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("create shipped: %v", err)
	}

	all, err := s.ListTickets(TicketListFilter{})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("list len = %d, want 3", len(all))
	}
	if all[0].ID != "shipped" || all[2].ID != "backlog" {
		t.Fatalf("ordering = %s..%s, want shipped..backlog", all[0].ID, all[2].ID)
	}

	working, err := s.ListTickets(TicketListFilter{Status: TicketStatusWorking})
	if err != nil {
		t.Fatalf("ListTickets(working): %v", err)
	}
	if len(working) != 1 || working[0].ID != "active" {
		t.Fatalf("working filter = %+v, want [active]", working)
	}

	if err := s.ArchiveTicket("active", ticketBase.Add(3*time.Minute)); !errors.Is(err, ErrTicketNotClosed) {
		t.Fatalf("archive open = %v, want ErrTicketNotClosed", err)
	}
	if err := s.ArchiveTicket("shipped", ticketBase.Add(3*time.Minute)); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	board, err := s.ListTickets(TicketListFilter{})
	if err != nil {
		t.Fatalf("ListTickets after archive: %v", err)
	}
	if len(board) != 2 {
		t.Fatalf("board len = %d, want 2 (archived hidden)", len(board))
	}
	withArchived, err := s.ListTickets(TicketListFilter{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListTickets(IncludeArchived): %v", err)
	}
	if len(withArchived) != 3 {
		t.Fatalf("IncludeArchived len = %d, want 3", len(withArchived))
	}
}

func TestArchivedTicketReopenedBecomesVisible(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateTicket(Ticket{ID: "zombie", Title: "shipped", Status: TicketStatusDone}, "you", ticketBase); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.ArchiveTicket("zombie", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if board, err := s.ListTickets(TicketListFilter{}); err != nil || len(board) != 0 {
		t.Fatalf("board after archive = %d (err %v), want 0", len(board), err)
	}

	reopened, err := s.SetTicketStatus("zombie", TicketStatusWorking, "you", "back to it", ticketBase.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}
	if reopened.ArchivedAt != nil {
		t.Fatalf("reopened ArchivedAt = %v, want nil (un-archived)", reopened.ArchivedAt)
	}
	if reopened.ClosedAt != nil {
		t.Fatalf("reopened ClosedAt = %v, want nil", reopened.ClosedAt)
	}

	board, err := s.ListTickets(TicketListFilter{})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	if len(board) != 1 || board[0].ID != "zombie" {
		t.Fatalf("board after reopen = %+v, want [zombie]", board)
	}
	if board[0].ArchivedAt != nil {
		t.Fatalf("persisted ArchivedAt = %v, want nil", board[0].ArchivedAt)
	}
}

func TestAutomationTicketRetentionKeepsUserHistoryForever(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	const ttl = 30 * 24 * time.Hour
	now := ticketBase

	if _, err := s.CreateTicket(Ticket{ID: "open", Title: "live"}, "you", now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("create open: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "recent-user", Title: "recent", Status: TicketStatusDone}, "you", now.Add(-5*24*time.Hour)); err != nil {
		t.Fatalf("create recent user ticket: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "stale-user", Title: "stale"}, "you", now.Add(-100*24*time.Hour)); err != nil {
		t.Fatalf("create stale user ticket: %v", err)
	}
	closedAt := now.Add(-60 * 24 * time.Hour)
	if _, err := s.SetTicketStatus("stale-user", TicketStatusDone, "agent7", "done long ago", closedAt); err != nil {
		t.Fatalf("close stale user ticket: %v", err)
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "stale-user", Filename: "old.txt"}, "agent7", closedAt); err != nil {
		t.Fatalf("attach stale user ticket: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "auto-0123456789abcdef", Title: "human despite its id"}, "you", now.Add(-100*24*time.Hour)); err != nil {
		t.Fatalf("create automation-shaped user ticket: %v", err)
	}
	if _, err := s.SetTicketStatus("auto-0123456789abcdef", TicketStatusFailed, "agent7", "failed long ago", closedAt); err != nil {
		t.Fatalf("close automation-shaped user ticket: %v", err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{
		ID: "stale-automation", Title: "scheduled work", Status: TicketStatusDone,
		AutomationRunID: "run-stale",
	}, "automation:nightly", TicketRoleChiefOfStaff, closedAt); err != nil {
		t.Fatalf("create stale automation ticket: %v", err)
	}
	if _, err := s.AddTicketAttachment(TicketAttachment{TicketID: "stale-automation", Filename: "automation.txt"}, "automation:nightly", closedAt); err != nil {
		t.Fatalf("attach stale automation ticket: %v", err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{
		ID: "recent-automation", Title: "recent scheduled work", Status: TicketStatusDone,
		AutomationRunID: "run-recent",
	}, "automation:nightly", TicketRoleChiefOfStaff, now.Add(-5*24*time.Hour)); err != nil {
		t.Fatalf("create recent automation ticket: %v", err)
	}
	if _, err := s.EnsureAutomationTicket(Ticket{
		ID: "open-automation", Title: "open scheduled work", Status: TicketStatusWorking,
		AutomationRunID: "run-open",
	}, "automation:nightly", TicketRoleChiefOfStaff, now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("create open automation ticket: %v", err)
	}

	userBefore, err := s.GetTicket("stale-user")
	if err != nil {
		t.Fatalf("read stale user ticket: %v", err)
	}
	childCounts := func(ticketID string) []int {
		t.Helper()
		counts := make([]int, 0, 5)
		for _, table := range []string{"ticket_activity", "ticket_attachments", "ticket_events", "ticket_event_cursors", "ticket_subscriptions"} {
			var count int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE ticket_id=?`, ticketID).Scan(&count); err != nil {
				t.Fatalf("count %s for %s: %v", table, ticketID, err)
			}
			counts = append(counts, count)
		}
		return counts
	}
	userChildrenBefore := childCounts("stale-user")

	removed, err := s.SweepExpiredAutomationTickets(now, ttl)
	if err != nil {
		t.Fatalf("SweepExpiredAutomationTickets: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 stale automation ticket", removed)
	}

	if gone, _ := s.GetTicket("stale-automation"); gone != nil {
		t.Fatal("stale automation ticket survived the sweep")
	}
	if kept, _ := s.GetTicket("recent-automation"); kept == nil {
		t.Fatal("recent automation ticket was swept early")
	}
	if kept, _ := s.GetTicket("open-automation"); kept == nil {
		t.Fatal("open automation ticket was swept")
	}
	if kept, _ := s.GetTicket("open"); kept == nil {
		t.Fatal("open backlog ticket was swept")
	}
	if kept, _ := s.GetTicket("auto-0123456789abcdef"); kept == nil {
		t.Fatal("automation-shaped user ticket was swept without provenance")
	}
	userAfter, err := s.GetTicket("stale-user")
	if err != nil {
		t.Fatalf("read stale user ticket after sweep: %v", err)
	}
	if !reflect.DeepEqual(userAfter, userBefore) {
		t.Fatalf("stale user ticket changed:\nbefore: %#v\nafter:  %#v", userBefore, userAfter)
	}
	if after := childCounts("stale-user"); !reflect.DeepEqual(after, userChildrenBefore) {
		t.Fatalf("stale user child rows changed: before=%v after=%v", userChildrenBefore, after)
	}
	if children := childCounts("stale-automation"); !reflect.DeepEqual(children, []int{0, 0, 0, 0, 0}) {
		t.Fatalf("expired automation ticket left child rows: %v", children)
	}
}

func TestTicketAssigneesOwnedByRole(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if ids := s.TicketAssigneesOwnedByRole(""); len(ids) != 0 {
		t.Fatalf("empty role = %v, want none", ids)
	}

	if _, err := s.CreateRoleOwnedTicket(Ticket{
		ID:       "delegated",
		Title:    "do the thing",
		Assignee: "sess-delegated",
	}, "chief-1", TicketRoleChiefOfStaff, ticketBase); err != nil {
		t.Fatalf("CreateTicket delegated: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{
		ID:       "self-authored",
		Title:    "user work",
		Assignee: "sess-other",
	}, "you", ticketBase); err != nil {
		t.Fatalf("CreateTicket self: %v", err)
	}

	ids := s.TicketAssigneesOwnedByRole(TicketRoleChiefOfStaff)
	if !ids["sess-delegated"] {
		t.Fatalf("delegated session missing from set: %v", ids)
	}
	if ids["sess-other"] {
		t.Fatalf("non-chief-authored session leaked into set: %v", ids)
	}

	if _, err := s.SetTicketStatus("delegated", TicketStatusDone, "sess-delegated", "shipped", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("SetTicketStatus terminal: %v", err)
	}
	if ids := s.TicketAssigneesOwnedByRole(TicketRoleChiefOfStaff); !ids["sess-delegated"] {
		t.Fatalf("delegated session lost after terminal report: %v", ids)
	}

	if err := s.ArchiveTicket("delegated", ticketBase.Add(2*time.Minute)); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if ids := s.TicketAssigneesOwnedByRole(TicketRoleChiefOfStaff); ids["sess-delegated"] {
		t.Fatalf("archived ticket still in set: %v", ids)
	}
}

func TestTicketPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{ID: "persist", Title: "survives restart"}, "you", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := s.SetTicketStatus("persist", TicketStatusInReview, "agent7", "ready", ticketBase.Add(time.Minute)); err != nil {
		t.Fatalf("SetTicketStatus: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, err := reopened.GetTicket("persist")
	if err != nil {
		t.Fatalf("GetTicket after reopen: %v", err)
	}
	if got == nil || got.Status != TicketStatusInReview {
		t.Fatalf("persisted ticket = %+v, want in_review", got)
	}
	if len(got.Activity) != 1 || got.Activity[0].ToStatus != TicketStatusInReview {
		t.Fatalf("persisted activity = %+v", got.Activity)
	}
}

func TestTicketResumeSessionID(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	const sessionID = "agent-sess-1"
	if _, err := s.CreateTicket(Ticket{
		ID:          "resume-me",
		Title:       "Resume the conversation",
		Assignee:    sessionID,
		Cwd:         "/tmp/project",
		LastAgentID: "claude",
	}, "chief", ticketBase); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if got := s.GetTicketResumeSessionID(sessionID); got != "" {
		t.Fatalf("resume id before capture = %q, want empty", got)
	}

	before, err := s.GetTicket("resume-me")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if err := s.SetTicketResumeSessionID(sessionID, "claude-conv-abc"); err != nil {
		t.Fatalf("SetTicketResumeSessionID: %v", err)
	}
	after, err := s.GetTicket("resume-me")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("updated_at churned: %q -> %q", before.UpdatedAt, after.UpdatedAt)
	}
	if len(after.Activity) != len(before.Activity) {
		t.Fatalf("activity changed: %d -> %d", len(before.Activity), len(after.Activity))
	}

	// No session row exists here, which is the post-close state the key has to
	// survive.
	if got := s.GetResumeSessionID(sessionID); got != "" {
		t.Fatalf("session-table resume id = %q, want empty (no session row)", got)
	}
	if got := s.GetTicketResumeSessionID(sessionID); got != "claude-conv-abc" {
		t.Fatalf("ticket resume id = %q, want %q", got, "claude-conv-abc")
	}

	if err := s.SetTicketResumeSessionID("unbound", "x"); err != nil {
		t.Fatalf("SetTicketResumeSessionID unbound: %v", err)
	}
	if got := s.GetTicketResumeSessionID("unbound"); got != "" {
		t.Fatalf("unbound resume id = %q, want empty", got)
	}
}

func TestCrashedTicketsForAssignee(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	mk := func(id string, status TicketStatus, assignee string) {
		t.Helper()
		if _, err := s.CreateTicket(Ticket{ID: id, Title: id, Status: status, Assignee: assignee}, "chief", ticketBase); err != nil {
			t.Fatalf("CreateTicket %s: %v", id, err)
		}
	}
	mk("crashed-mine", TicketStatusCrashed, "sess-1")
	mk("crashed-archived", TicketStatusCrashed, "sess-1")
	if err := s.ArchiveTicket("crashed-archived", ticketBase); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	mk("working-mine", TicketStatusWorking, "sess-1")
	mk("crashed-other", TicketStatusCrashed, "sess-2")

	got, err := s.CrashedTicketsForAssignee("sess-1")
	if err != nil {
		t.Fatalf("CrashedTicketsForAssignee: %v", err)
	}
	if len(got) != 1 || got[0].ID != "crashed-mine" {
		ids := make([]string, len(got))
		for i, tk := range got {
			ids[i] = tk.ID
		}
		t.Fatalf("got %v, want exactly [crashed-mine]", ids)
	}

	if got, err := s.CrashedTicketsForAssignee(""); err != nil || got != nil {
		t.Fatalf("empty assignee = %v, %v, want nil, nil", got, err)
	}
}

func TestSubmitTicketAttachIsAtomicAndIdempotent(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.CreateTicket(Ticket{ID: "attach", Title: "Attach", Status: TicketStatusWorking}, "chief", ticketBase); err != nil {
		t.Fatal(err)
	}
	status := TicketStatusInReview
	first, err := s.SubmitTicketAttach("attach", "agent", "fingerprint", "fingerprint\n{}", "Handed over: plan.md\n\nDecision context", &status, ticketBase.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first.EventSeq == 0 || first.Status != TicketStatusInReview || first.Deduplicated {
		t.Fatalf("first receipt = %+v", first)
	}
	retry, err := s.SubmitTicketAttach("attach", "agent", "fingerprint", "fingerprint\n{}", "Handed over: plan.md\n\nDecision context", &status, ticketBase.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Deduplicated || retry.EventSeq != first.EventSeq || retry.Status != first.Status {
		t.Fatalf("retry receipt = %+v, first = %+v", retry, first)
	}
	ticket, _ := s.GetTicket("attach")
	var attachments int
	for _, activity := range ticket.Activity {
		if activity.Kind == TicketActivityAttach {
			attachments++
		}
	}
	if attachments != 1 || ticket.Status != TicketStatusInReview {
		t.Fatalf("ticket = %+v, attachments=%d", ticket, attachments)
	}
}

func TestStrandedTicketsAreTheDeadOnesStillOnTheBoard(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	mk := func(id string, status TicketStatus, automationRunID string) {
		t.Helper()
		if _, err := s.CreateTicket(Ticket{
			ID: id, Title: id, Assignee: "sess-" + id, AutomationRunID: automationRunID,
		}, "chief", ticketBase); err != nil {
			t.Fatalf("CreateTicket %s: %v", id, err)
		}
		if status != TicketStatusTodo {
			if _, err := s.SetTicketStatus(id, status, TicketAuthorAttn, "", ticketBase); err != nil {
				t.Fatalf("SetTicketStatus %s: %v", id, err)
			}
		}
	}
	mk("crashed-one", TicketStatusCrashed, "")
	mk("failed-one", TicketStatusFailed, "")
	mk("working-one", TicketStatusWorking, "")
	mk("todo-one", TicketStatusTodo, "")
	mk("done-one", TicketStatusDone, "")
	mk("crashed-automation", TicketStatusCrashed, "run-1")
	mk("crashed-archived", TicketStatusCrashed, "")
	if err := s.ArchiveTicket("crashed-archived", ticketBase); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}

	stranded, err := s.StrandedTickets()
	if err != nil {
		t.Fatalf("StrandedTickets: %v", err)
	}
	got := make([]string, 0, len(stranded))
	for _, ticket := range stranded {
		got = append(got, ticket.ID)
	}
	want := "crashed-one failed-one"
	if joined := strings.Join(got, " "); joined != want && joined != "failed-one crashed-one" {
		t.Fatalf("stranded = %q, want the two dead unarchived non-automation tickets (%q)", joined, want)
	}
}
