package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDelegationOperationReservesActiveTicket(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	first, claimed, err := s.ClaimDelegationOperation("request-1", "operation-1", "session-1", "", "planned", `{"ticket_id":"planned"}`, now)
	if err != nil || !claimed {
		t.Fatalf("first claim = %+v, %v, %v", first, claimed, err)
	}
	if protocol.Deref(first.Operation.TicketID) != "planned" {
		t.Fatalf("reserved ticket = %q", protocol.Deref(first.Operation.TicketID))
	}
	if first.Operation.SeedID != nil {
		t.Fatalf("legacy operation seed = %q, want absent", protocol.Deref(first.Operation.SeedID))
	}
	_, _, err = s.ClaimDelegationOperation("request-2", "operation-2", "session-2", "", "planned", `{"ticket_id":"planned"}`, now)
	if !errors.Is(err, ErrTicketDelegationReserved) {
		t.Fatalf("second claim error = %v, want ErrTicketDelegationReserved", err)
	}
	if err := s.UpdateDelegationOperation("operation-1", protocol.DelegationOperationStateFailed, "failed", "", "", "", nil, errors.New("failed"), now); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := s.ClaimDelegationOperation("request-3", "operation-3", "session-3", "", "planned", `{"ticket_id":"planned"}`, now); err != nil || !claimed {
		t.Fatalf("claim after terminal operation = %v, %v", claimed, err)
	}
}

func TestCompletedDelegationPreservesRecordedWorktreeRootAndHandoverSnapshot(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	snapshot := DelegationHandoverSnapshot{SeedRev: 17, TenderSession: "predecessor", TenderMember: "alder"}
	record, claimed, err := s.ClaimDelegationOperationWithHandoverSnapshot(
		"request", "operation", "successor", "", "s-seed", `{"assignment":{"kind":"seed"}}`, "", "base-commit", "s-parent", snapshot, now,
	)
	if err != nil || !claimed {
		t.Fatalf("claim = %+v, %v, %v", record, claimed, err)
	}
	root := filepath.Join(t.TempDir(), "worktree")
	if err := s.MarkDelegationWorktreeOwned(record.Operation.OperationID, root, "owner-token", now); err != nil {
		t.Fatal(err)
	}
	result := &protocol.DelegateResult{SessionID: "successor", SeedID: "s-seed", Directory: filepath.Join(root, "subdir")}
	if err := s.UpdateDelegationOperation(record.Operation.OperationID, protocol.DelegationOperationStateCompleted, "ready", "", "", "", result, nil, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDelegationOperation(record.Operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(got.Operation.WorktreePath) != root {
		t.Fatalf("worktree path = %q, want root %q", protocol.Deref(got.Operation.WorktreePath), root)
	}
	if protocol.Deref(got.Operation.SeedID) != "s-seed" || got.Operation.TicketID != nil {
		t.Fatalf("resource identities = seed %q ticket %q, want seed only", protocol.Deref(got.Operation.SeedID), protocol.Deref(got.Operation.TicketID))
	}
	if got.HandoverSeedRev != snapshot.SeedRev || got.HandoverTenderSession != snapshot.TenderSession || got.HandoverTenderMember != snapshot.TenderMember {
		t.Fatalf("handover snapshot = rev %d session %q member %q", got.HandoverSeedRev, got.HandoverTenderSession, got.HandoverTenderMember)
	}
	if got.BaseCommit != "base-commit" {
		t.Fatalf("base commit = %q, want acceptance snapshot", got.BaseCommit)
	}
	if got.ParentSeedID != "s-parent" {
		t.Fatalf("parent seed = %q, want acceptance snapshot", got.ParentSeedID)
	}
}

func TestExplicitSeedDelegationsAreNotLegacySalvageRows(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	request := `{"assignment":{"kind":"seed","seed_id":"s-current"}}`
	record, claimed, err := s.ClaimDelegationOperationWithHandoverSnapshot(
		"request-current", "operation-current", "session-current", "", "s-current", request, "", "", "", DelegationHandoverSnapshot{}, now,
	)
	if err != nil || !claimed {
		t.Fatalf("claim = %+v, %v, %v", record, claimed, err)
	}
	if err := s.UpdateDelegationOperation(record.Operation.OperationID, protocol.DelegationOperationStateFailed, "failed", "", "", "", nil, errors.New("failed"), now); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListLegacyDelegationOperations()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("legacy salvage rows = %+v, want explicit seed operation excluded", rows)
	}
}

func TestPendingDelegationReservesItsSuccessorSession(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	record, claimed, err := s.ClaimDelegationOperation("request-reserved", "operation-reserved", "session-reserved", "", "", `{}`, now)
	if err != nil || !claimed {
		t.Fatalf("claim = %+v, %v, %v", record, claimed, err)
	}
	if !s.DelegationSessionReserved("session-reserved") {
		t.Fatal("accepted successor was not reserved")
	}
	if err := s.UpdateDelegationOperation(record.Operation.OperationID, protocol.DelegationOperationStateFailed, "failed", "", "", "", nil, errors.New("failed"), now); err != nil {
		t.Fatal(err)
	}
	if s.DelegationSessionReserved("session-reserved") {
		t.Fatal("failed operation kept reserving its successor")
	}
}
