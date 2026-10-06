package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) startDelegation(msg *protocol.DelegateMessage) (*protocol.DelegationOperation, error) {
	return d.startDelegationForeground(msg)
}

func (d *Daemon) startDelegationForeground(msg *protocol.DelegateMessage) (*protocol.DelegationOperation, error) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if strings.HasPrefix(requestID, "op-") {
		return nil, fmt.Errorf("request_id uses reserved operation prefix op-")
	}
	msg.RequestID = requestID
	msg.Cmd = protocol.CmdDelegate
	if protocol.TrimID(protocol.Deref(msg.SourceSessionID)) == "" {
		msg.SourceSessionID = nil
	}
	if err := validateDelegateRequestShape(msg); err != nil {
		return nil, err
	}
	profile, err := d.resolveGardenProfile(protocol.Deref(msg.SourceSessionID), protocol.Deref(msg.ProfileID), "")
	if err != nil {
		return nil, err
	}
	msg.ProfileID = protocol.Ptr(profile.ID)
	if seedID := protocol.Deref(msg.Assignment.SeedID); seedID != "" {
		if err := d.requireSeedInProfile(seedID, profile.ID, false); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode delegation request: %w", err)
	}

	if existing, lookupErr := d.store.GetDelegationOperation(requestID); lookupErr == nil {
		var saved protocol.DelegateMessage
		if err := json.Unmarshal([]byte(existing.RequestJSON), &saved); err != nil {
			return nil, err
		}
		if protocol.TrimID(protocol.Deref(saved.SourceSessionID)) == "" {
			saved.SourceSessionID = nil
		}
		normalized, err := json.Marshal(saved)
		if err != nil {
			return nil, err
		}
		if string(normalized) != string(encoded) {
			return nil, store.ErrDelegationRequestConflict
		}
		if existing.Operation.State == protocol.DelegationOperationStateAccepted || existing.Operation.State == protocol.DelegationOperationStatePreparing {
			operationID := existing.Operation.OperationID
			d.life.Go("runDelegationOperation", func() { d.runDelegationOperation(operationID) })
		}
		return &existing.Operation, nil
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return nil, lookupErr
	}
	if ref := strings.TrimSpace(protocol.Deref(msg.Desktop)); ref != "" {
		sourceID := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
		if source := d.store.Get(sourceID); source != nil || sourceID == "" {
			if _, _, err := d.delegationDestination(source, ref, protocol.Deref(msg.ProfileID)); err != nil {
				return nil, err
			}
		}
	}
	resolved, err := d.resolveDelegationPreferences(msg)
	if err != nil {
		return nil, err
	}
	resolvedJSON := ""
	if resolved != nil {
		raw, err := json.Marshal(resolved)
		if err != nil {
			return nil, err
		}
		resolvedJSON = string(raw)
	}
	var chiefSessionID protocol.SessionID
	if d.isChiefOfStaffSession(protocol.Deref(msg.SourceSessionID)) {
		chiefSessionID = protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	}
	seedID := ""
	parentSeedID := ""
	handoverSnapshot := store.DelegationHandoverSnapshot{}
	if msg.Assignment.Kind == protocol.DelegateAssignmentKindSeed {
		seedID = strings.TrimSpace(protocol.Deref(msg.Assignment.SeedID))
		if msg.Assignment.Handover != nil {
			seed, doc, err := d.readSeed(seedID)
			if err != nil {
				return nil, err
			}
			if garden.Closed(seed.Status) {
				return nil, fmt.Errorf("seed %s is %s; replant it before delegating", seedID, seed.Status)
			}
			handoverSnapshot = store.DelegationHandoverSnapshot{
				SeedRev: int(doc.Rev), TenderSession: seed.TenderSession, TenderMember: seed.TenderMember,
			}
		}
	}
	if msg.Assignment.Kind == protocol.DelegateAssignmentKindNew {
		if sourceID := protocol.TrimID(protocol.Deref(msg.SourceSessionID)); sourceID != "" {
			parentSeedID, _ = d.gardenDispatchCrown(sourceID)
		}
	}
	baseCommit, err := d.resolveAcceptedDelegationBase(msg)
	if err != nil {
		return nil, err
	}
	record, claimed, err := d.store.ClaimDelegationOperationWithHandoverSnapshot(requestID, "op-"+uuid.NewString(), protocol.SessionID(uuid.NewString()), chiefSessionID, seedID, string(encoded), resolvedJSON, baseCommit, parentSeedID, handoverSnapshot, time.Now())
	if err != nil {
		return nil, err
	}
	if claimed || record.Operation.State == protocol.DelegationOperationStateAccepted || record.Operation.State == protocol.DelegationOperationStatePreparing {
		operationID := record.Operation.OperationID
		d.life.Go("runDelegationOperation", func() { d.runDelegationOperation(operationID) })
	}
	return &record.Operation, nil
}

func (d *Daemon) runDelegationOperation(id string) {
	if !d.beginDelegationRun(id) {
		return
	}
	defer d.endDelegationRun(id)
	select {
	case <-d.recoverySettledSignal():
	case <-d.life.Done():
		return
	}
	release, held := d.life.Hold("runDelegationOperation")
	if !held {
		return
	}
	defer release()
	_ = d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		d.runDelegationOperationProtected(protection, id)
		return nil
	})
}

func (d *Daemon) runDelegationOperationProtected(protection foregroundCleanupProtection, id string) {
	record, err := d.store.GetDelegationOperation(id)
	if err != nil {
		d.logf("delegate operation %s disappeared: %v", id, err)
		return
	}
	if record.Operation.State == protocol.DelegationOperationStateCompleted || record.Operation.State == protocol.DelegationOperationStateFailed {
		return
	}
	_ = d.store.UpdateDelegationOperation(id, protocol.DelegationOperationStatePreparing,
		"validating delegation request", "", "", "", nil, nil, time.Now())
	var shape struct {
		Assignment json.RawMessage `json:"assignment"`
	}
	if err := json.Unmarshal([]byte(record.RequestJSON), &shape); err != nil {
		d.finishDelegationFailure(id, fmt.Errorf("decode accepted delegation request: %w", err))
		return
	}
	if len(shape.Assignment) == 0 || string(shape.Assignment) == "null" {
		if existing := d.store.Get(record.Operation.SessionID); existing != nil && d.sessionHasLiveWorker(existing.ID) {
			result := d.completedDelegationResult(existing, record.WorktreeOwned)
			if seedID, ok := d.gardenDispatchCrown(existing.ID); ok {
				result.SeedID = seedID
			}
			d.persistDelegationTerminal(id, protocol.DelegationOperationStateCompleted, "reconciled legacy delegated session", existing.ProfileID, protocol.Deref(record.Operation.WorktreePath), result, nil)
			return
		}
		d.finishDelegationFailure(id, fmt.Errorf("%w; no live successor can prove the old implicit launch intent. Submit a new request with the known seed, cwd, and checkout", errLegacyDelegationRequest))
		return
	}
	var msg protocol.DelegateMessage
	if err := json.Unmarshal([]byte(record.RequestJSON), &msg); err != nil {
		d.finishDelegationFailure(id, fmt.Errorf("decode accepted delegation request: %w", err))
		return
	}
	var resolved *delegationprefs.Resolved
	if record.ResolvedPreferences != "" {
		if err := json.Unmarshal([]byte(record.ResolvedPreferences), &resolved); err != nil {
			d.finishDelegationFailure(id, fmt.Errorf("decode resolved preferences: %w", err))
			return
		}
	}
	runtime, err := d.resolveDelegateRuntimeWithHandoverSnapshot(
		&msg, protocol.Deref(record.Operation.SeedID), record.BaseCommit, record.HandoffNoteID,
		record.Operation.SessionID, protocol.Deref(record.Operation.WorktreePath), record.WorktreeOwned,
		record.HandoverSeedRev, record.HandoverTenderSession, record.HandoverTenderMember, id, record.ParentSeedID,
	)
	if err != nil {
		d.finishDelegationFailure(id, err)
		return
	}
	resolvedSeedID := strings.TrimSpace(protocol.Deref(runtime.Plot))
	if runtime.Handover != nil {
		resolvedSeedID = strings.TrimSpace(runtime.Handover.SeedID)
	}
	resolvedBranch, baseCommit := "", ""
	if runtime.Worktree != nil {
		resolvedBranch = strings.TrimSpace(runtime.Worktree.Branch)
		baseCommit = strings.TrimSpace(protocol.Deref(runtime.Worktree.StartingFrom))
	} else if runtime.Checkout != nil {
		resolvedBranch = strings.TrimSpace(runtime.Checkout.Branch)
	}
	handoffNoteID := ""
	if runtime.Handover != nil {
		handoffNoteID = strings.TrimSpace(protocol.Deref(runtime.Handover.NoteID))
	}
	if err := d.store.RecordDelegationResolution(id, resolvedSeedID, runtime.Cwd, resolvedBranch, baseCommit, handoffNoteID, time.Now()); err != nil {
		d.finishDelegationFailure(id, fmt.Errorf("record resolved delegation: %w", err))
		return
	}
	result, launchErr := d.delegateOperationProtected(protection, runtime, id, record.Operation.SessionID, protocol.Deref(record.Operation.WorktreePath), record.WorktreeOwned, record.WorktreeToken, record.ChiefSessionID, resolved)
	if errors.Is(launchErr, errDelegationInterrupted) {
		return
	}
	if launchErr != nil {
		d.finishDelegationFailure(id, launchErr)
		return
	}
	d.persistDelegationTerminal(id, protocol.DelegationOperationStateCompleted,
		"delegation ready", protocol.Deref(result.ProfileID), "", result, nil)
}

func (d *Daemon) finishDelegationFailure(id string, err error) {
	// A failure during stop is the teardown's (git executor closed, sessions killed); the next daemon resumes it.
	if d.stopping() {
		d.logf("delegate operation %s stays pending across daemon stop: %v", id, err)
		return
	}
	d.persistDelegationTerminal(id, protocol.DelegationOperationStateFailed,
		"delegation failed", "", "", nil, err)
}

func (d *Daemon) persistDelegationTerminal(id string, state protocol.DelegationOperationState, progress, profileID, worktreePath string, result *protocol.DelegateResult, operationErr error) {
	delay := 100 * time.Millisecond
	for {
		if err := d.store.UpdateDelegationOperation(id, state, progress, profileID, "", worktreePath, result, operationErr, time.Now()); err == nil {
			return
		} else {
			d.logf("persist terminal delegation operation %s: %v", id, err)
		}
		select {
		case <-d.life.Done():
			return
		case <-time.After(delay):
			if delay < 5*time.Second {
				delay *= 2
			}
		}
	}
}

func (d *Daemon) beginDelegationRun(id string) bool {
	d.delegationMu.Lock()
	defer d.delegationMu.Unlock()
	if d.delegationRunning == nil {
		d.delegationRunning = make(map[string]bool)
	}
	if d.delegationRunning[id] {
		return false
	}
	d.delegationRunning[id] = true
	return true
}

func (d *Daemon) endDelegationRun(id string) {
	d.delegationMu.Lock()
	delete(d.delegationRunning, id)
	d.delegationMu.Unlock()
}

func (d *Daemon) delegationOperation(id string) (*protocol.DelegationOperation, error) {
	record, err := d.store.GetDelegationOperation(strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("delegation operation not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	operation := record.Operation
	facts := []string{operation.Progress}
	if session := d.store.Get(operation.SessionID); session != nil {
		if d.sessionHasLiveWorker(session.ID) {
			facts = append(facts, "worker is running")
		} else {
			facts = append(facts, "worker is not running")
		}
	} else {
		facts = append(facts, "worker state is unknown")
	}
	if seedID := strings.TrimSpace(protocol.Deref(operation.SeedID)); seedID != "" {
		if seed, _, seedErr := d.readSeed(seedID); seedErr == nil {
			holder := seed.Tender().DisplayName()
			if holder == "" {
				holder = "nobody"
			}
			facts = append(facts, fmt.Sprintf("seed %s is held by %s", seedID, holder))
		} else {
			facts = append(facts, fmt.Sprintf("seed %s state is unknown", seedID))
		}
	}
	operation.Progress = strings.Join(facts, "; ")
	return &operation, nil
}

func (d *Daemon) scopedDelegationOperation(id string, sessionID protocol.SessionID, requested string, selected string) (*protocol.DelegationOperation, error) {
	profile, err := d.resolveGardenProfile(sessionID, requested, selected)
	if err != nil {
		return nil, err
	}
	if selected != "" && profile.ID != selected {
		app, err := d.store.GetProfile(selected)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("source session belongs to profile %q; app belongs to profile %q", profile.Name, app.Name)
	}
	record, err := d.store.GetDelegationOperation(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	var request protocol.DelegateMessage
	if err := json.Unmarshal([]byte(record.RequestJSON), &request); err != nil {
		return nil, err
	}
	ownerID := protocol.Deref(request.ProfileID)
	if ownerID != "" && ownerID != profile.ID {
		owner, err := d.store.GetProfile(ownerID)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("delegation %s belongs to profile %q; caller belongs to profile %q", id, owner.Name, profile.Name)
	}
	if seedID := protocol.Deref(record.Operation.SeedID); seedID != "" {
		schema, err := d.seedsCollection()
		if err != nil {
			return nil, err
		}
		_, found, err := d.store.GetDocument(*schema, seedID)
		if err != nil {
			return nil, err
		}
		if found {
			if err := d.requireSeedInProfile(seedID, profile.ID, false); err != nil {
				return nil, err
			}
		}
	}
	return d.delegationOperation(id)
}

func (d *Daemon) resumePendingDelegations() {
	records, err := d.store.PendingDelegationOperations()
	if err != nil {
		d.logf("load pending delegation operations: %v", err)
		return
	}
	for i := range records {
		operationID := records[i].Operation.OperationID
		d.life.Go("runDelegationOperation", func() { d.runDelegationOperation(operationID) })
	}
}

func (d *Daemon) handleDelegateStatusWS(client *wsClient, msg *protocol.DelegateStatusMessage) {
	operation, err := d.scopedDelegationOperation(msg.ID, protocol.Deref(msg.SourceSessionID), protocol.Deref(msg.ProfileID), client.selectedProfile())
	response := protocol.DelegationOperationMessage{
		Event:     protocol.EventDelegationOperation,
		Success:   err == nil,
		Operation: operation,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, response)
}
