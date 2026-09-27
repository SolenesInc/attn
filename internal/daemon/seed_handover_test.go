package daemon

import (
	"context"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func handoverRequest(d *Daemon, seed garden.Seed, requestID, sourceSessionID, handoff string) *protocol.DelegateMessage {
	cwd := d.store.Get(sourceSessionID).Directory
	if dispatch, ok := d.gardenDispatch(seed.TenderSession); ok && dispatch.Cwd != "" {
		cwd = dispatch.Cwd
	}
	msg := &protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: requestID, SourceSessionID: protocol.Ptr(sourceSessionID),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seed.ID), Handover: &protocol.DelegateHandover{Note: protocol.Ptr(handoff)}},
		Cwd:        cwd, Agent: protocol.Ptr("codex"),
	}
	if _, err := attngit.NewClient().GetRepoRoot(context.Background(), cwd); err == nil {
		branch, branchErr := attngit.NewClient().GetCurrentBranch(context.Background(), cwd)
		if branchErr == nil && branch != "" {
			msg.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: branch}
		}
	}
	return msg
}

func TestSeedHandoverLaunchFailurePreservesTheCommittedTransfer(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	oldSessionID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	backend.spawnErr = syscall.EPERM

	op, err := d.startDelegation(handoverRequest(d, seed, "handover-fails", sourceSessionID, "This must not land."))
	if err != nil {
		t.Fatal(err)
	}
	done := waitDelegationOperation(t, d, op.OperationID)
	if done.State != protocol.DelegationOperationStateFailed {
		t.Fatalf("operation = %+v, want failed", done)
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != done.SessionID || after.LastExecutionID != done.SessionID {
		t.Fatalf("failed Handover lost committed ownership: %+v", after)
	}
	if session := d.store.Get(done.SessionID); session != nil {
		t.Fatalf("failed Handover left a worker: %+v", session)
	}
	if notes := seedNoteCount(t, d, seedID); notes != 1 {
		t.Fatalf("failed Handover retained %d notes, want its durable handoff", notes)
	}
	oldDispatch, _ := d.gardenDispatch(oldSessionID)
	if oldDispatch.SupersededBy != done.SessionID {
		t.Fatalf("predecessor dispatch was not superseded: %+v", oldDispatch)
	}
}
