package daemon

import (
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
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if strings.HasPrefix(requestID, "op-") {
		return nil, fmt.Errorf("request_id uses reserved operation prefix op-")
	}
	msg.RequestID = requestID
	msg.Cmd = protocol.CmdDelegate
	if err := validateDelegateRequestShape(msg); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode delegation request: %w", err)
	}

	if existing, lookupErr := d.store.GetDelegationOperation(requestID); lookupErr == nil {
		if existing.RequestJSON != string(encoded) {
			return nil, store.ErrDelegationRequestConflict
		}
		if existing.Operation.State == protocol.DelegationOperationStateAccepted || existing.Operation.State == protocol.DelegationOperationStatePreparing {
			go d.runDelegationOperation(existing.Operation.OperationID)
		}
		return &existing.Operation, nil
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return nil, lookupErr
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
	chiefSessionID := ""
	if currentChief := d.chiefOfStaffSessionID(); currentChief == strings.TrimSpace(protocol.Deref(msg.SourceSessionID)) {
		chiefSessionID = currentChief
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
		if sourceID := strings.TrimSpace(protocol.Deref(msg.SourceSessionID)); sourceID != "" {
			parentSeedID, _ = d.gardenDispatchCrown(sourceID)
		}
	}
	baseCommit, err := resolveAcceptedDelegationBase(msg)
	if err != nil {
		return nil, err
	}
	record, claimed, err := d.store.ClaimDelegationOperationWithHandoverSnapshot(requestID, "op-"+uuid.NewString(), uuid.NewString(), chiefSessionID, seedID, string(encoded), resolvedJSON, baseCommit, parentSeedID, handoverSnapshot, time.Now())
	if err != nil {
		return nil, err
	}
	if claimed || record.Operation.State == protocol.DelegationOperationStateAccepted || record.Operation.State == protocol.DelegationOperationStatePreparing {
		go d.runDelegationOperation(record.Operation.OperationID)
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
	case <-d.done:
		return
	}
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
			result := d.completedDelegationResult(existing, "", record.WorktreeOwned)
			if seedID, ok := d.gardenDispatchCrown(existing.ID); ok {
				result.SeedID = seedID
			}
			d.persistDelegationTerminal(id, protocol.DelegationOperationStateCompleted, "reconciled legacy delegated session", existing.WorkspaceID, protocol.Deref(record.Operation.WorktreePath), result, nil)
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
	result, launchErr := d.delegateOperation(runtime, id, record.Operation.SessionID, protocol.Deref(record.Operation.WorktreePath), record.WorktreeOwned, record.WorktreeToken, record.ChiefSessionID, resolved)
	if launchErr != nil {
		d.finishDelegationFailure(id, launchErr)
		return
	}
	d.persistDelegationTerminal(id, protocol.DelegationOperationStateCompleted,
		"delegation ready", protocol.Deref(result.WorkspaceID), "", result, nil)
}

func (d *Daemon) finishDelegationFailure(id string, err error) {
	d.persistDelegationTerminal(id, protocol.DelegationOperationStateFailed,
		"delegation failed", "", "", nil, err)
}

func (d *Daemon) persistDelegationTerminal(id string, state protocol.DelegationOperationState, progress, workspaceID, worktreePath string, result *protocol.DelegateResult, operationErr error) {
	delay := 100 * time.Millisecond
	for {
		if err := d.store.UpdateDelegationOperation(id, state, progress, workspaceID, "", worktreePath, result, operationErr, time.Now()); err == nil {
			return
		} else {
			d.logf("persist terminal delegation operation %s: %v", id, err)
		}
		select {
		case <-d.done:
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

func (d *Daemon) resumePendingDelegations() {
	records, err := d.store.PendingDelegationOperations()
	if err != nil {
		d.logf("load pending delegation operations: %v", err)
		return
	}
	for i := range records {
		go d.runDelegationOperation(records[i].Operation.OperationID)
	}
}

func (d *Daemon) handleDelegateStatusWS(client *wsClient, msg *protocol.DelegateStatusMessage) {
	operation, err := d.delegationOperation(msg.ID)
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
