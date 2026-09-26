package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func resolvedHandoverRequest(seed garden.Seed, docRev int64, requestID, sourceSessionID, handoff string) *resolvedDelegationLaunch {
	return &resolvedDelegationLaunch{RequestID: requestID, SourceSessionID: protocol.Ptr(sourceSessionID),
		Handover: &protocol.SeedHandoverRequest{
			SeedID: seed.ID, Handoff: protocol.Ptr(handoff), ExpectedRev: int(docRev),
			ExpectedTenderSession: seed.TenderSession, ExpectedTenderMember: seed.TenderMember,
		},
	}
}

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

func TestAcceptedSeedHandoverRejectsHolderChangeBeforeRecoveryResolution(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	_, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	addGardenSession(t, d, "racing-session")
	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	msg := handoverRequest(d, seed, "handover-accepted-race", sourceSessionID, "This must not target a later holder.")
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	record, claimed, err := d.store.ClaimDelegationOperationWithHandoverSnapshot(
		msg.RequestID, "op-handover-accepted-race", "successor-session", "", seedID, string(encoded), "",
		"", "", store.DelegationHandoverSnapshot{SeedRev: int(doc.Rev), TenderSession: seed.TenderSession, TenderMember: seed.TenderMember}, time.Now(),
	)
	if err != nil || !claimed {
		t.Fatalf("claim = %+v, %v, %v", record, claimed, err)
	}
	if _, _, err := d.applySeedTransition(seedID, garden.VerbTend, garden.Ask{
		Actor: garden.Tender{Session: "racing-session"}, Force: true,
	}); err != nil {
		t.Fatal(err)
	}
	d.runDelegationOperation(record.Operation.OperationID)
	done := waitDelegationOperation(t, d, record.Operation.OperationID)
	if done.State != protocol.DelegationOperationStateFailed || done.Failure == nil || !strings.Contains(done.Failure.Message, "ownership changed after the delegation request was accepted") {
		t.Fatalf("operation = %+v, want accepted-holder race failure", done)
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != "racing-session" || d.store.Get(record.Operation.SessionID) != nil {
		t.Fatalf("accepted handover overwrote the winner or spawned: seed=%+v session=%+v", after, d.store.Get(record.Operation.SessionID))
	}
}

func TestAcceptedSeedHandoverPreservesSameTenderEditsBeforeRecoveryResolution(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	_, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	seed, acceptedDoc, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	msg := handoverRequest(d, seed, "handover-accepted-edit", sourceSessionID, "Continue from the latest seed body.")
	edited := editSeed(t, d, seedID, "The same tender added current implementation details.")
	_, editedDoc, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if editedDoc.Rev <= acceptedDoc.Rev {
		t.Fatalf("edited revision = %d, want newer than accepted revision %d", editedDoc.Rev, acceptedDoc.Rev)
	}

	runtime, err := d.resolveDelegateRuntimeWithHandoverSnapshot(
		msg, seedID, "", "", "successor-session", "", false,
		int(acceptedDoc.Rev), seed.TenderSession, seed.TenderMember, "", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Handover == nil || runtime.Handover.ExpectedRev != int(editedDoc.Rev) {
		t.Fatalf("handover = %+v, want current revision %d", runtime.Handover, editedDoc.Rev)
	}
	if got := protocol.Deref(runtime.Brief); got != edited.Body {
		t.Fatalf("resolved body = %q, want latest %q", got, edited.Body)
	}
}

func TestSeedHandoverFinishesAfterAnInterruptedLaunchCapturedTheNewSession(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	_, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	now := string(protocol.TimestampNow())
	newSessionID := "interrupted-handover"
	d.store.Add(&protocol.Session{
		ID: newSessionID, Label: "new worker", Agent: protocol.SessionAgentCodex,
		Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if _, err := d.captureGardenSessionExecution(d.store.Get(newSessionID)); err != nil {
		t.Fatal(err)
	}
	before, ok := d.gardenDispatch(newSessionID)
	if !ok || before.Crown != "" {
		t.Fatalf("interrupted dispatch = %+v, ok=%v", before, ok)
	}
	msg := resolvedHandoverRequest(seed, doc.Rev, "handover-recovered", sourceSessionID, "Recovered after restart.")
	if _, err := d.bindSeedHandover(msg, "op-recovered", newSessionID, d.store.Get(newSessionID).Directory, "codex", false); err != nil {
		t.Fatalf("bind recovered Handover: %v", err)
	}
	after, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != newSessionID || after.LastExecutionID != newSessionID {
		t.Fatalf("recovered Handover = %+v", after)
	}
	dispatch, ok := d.gardenDispatch(newSessionID)
	if !ok || dispatch.Crown != seedID || dispatch.OperationID != "op-recovered" {
		t.Fatalf("recovered dispatch = %+v, ok=%v", dispatch, ok)
	}
	original := handoverRequest(d, seed, "handover-recovered-request", sourceSessionID, "Recovered after restart.")
	runtime, err := d.resolveDelegateRuntimeWithHandoverSnapshot(
		original, seedID, "", "", newSessionID, "", false,
		int(doc.Rev), seed.TenderSession, seed.TenderMember, "op-recovered", "",
	)
	if err != nil || runtime.Handover == nil {
		t.Fatalf("resolve already-bound handover = %+v, %v", runtime, err)
	}
	if runtime.PreviousTenderSession != seed.TenderSession {
		t.Fatalf("recovered predecessor = %q, want accepted holder %q", runtime.PreviousTenderSession, seed.TenderSession)
	}
}
