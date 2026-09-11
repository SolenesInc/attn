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
		"request", "operation", "successor", "", "s-seed", `{"assignment":{"kind":"seed"}}`, "", snapshot, now,
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
}
