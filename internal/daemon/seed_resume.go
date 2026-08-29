package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type seedResumeOutcome struct {
	SessionID      string
	WorkspaceID    string
	AlreadyRunning bool
}

func (d *Daemon) resumeSeed(seedID string) (*seedResumeOutcome, error) {
	seedID = strings.TrimSpace(seedID)
	if seedID == "" {
		return nil, fmt.Errorf("seed_id is required")
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	seed, seedDoc, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	if garden.Closed(seed.Status) {
		return nil, fmt.Errorf("%s is %s; replant it before resuming its agent", seed.ID, seed.Status)
	}
	continuation := d.continuationForSeed(seed)
	if continuation == nil {
		if tender := strings.TrimSpace(seed.TenderSession); tender != "" {
			return nil, fmt.Errorf("%s was tended by session %s, but no continuation was saved", seedID, tender)
		}
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	execution := continuation.Execution
	sessionID := strings.TrimSpace(execution.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	actor := garden.Tender{Session: sessionID}
	if _, err := garden.Transition(seed, garden.VerbTend, garden.Ask{Actor: actor}, d.sessionExists); err != nil {
		return nil, err
	}
	if existing := d.gardenSession(sessionID); existing != nil {
		if _, _, err := d.applySeedTransition(seedID, garden.VerbTend, garden.Ask{Actor: actor}); err != nil {
			return nil, err
		}
		return &seedResumeOutcome{
			SessionID: existing.ID, WorkspaceID: existing.WorkspaceID, AlreadyRunning: true,
		}, nil
	}
	if !continuation.ResumeAvailable {
		reason := strings.TrimSpace(continuation.ResumeReason)
		if reason == "" {
			reason = "the original conversation is unavailable"
		}
		return nil, fmt.Errorf("%s cannot resume: %s", seedID, reason)
	}
	cwd := strings.TrimSpace(execution.Cwd)
	agent := strings.TrimSpace(execution.Agent)
	resumeID := strings.TrimSpace(execution.Resume)

	directory, err := validateDelegationDirectory(cwd)
	if err != nil {
		return nil, err
	}

	// Unregister on rollback only if this call created the workspace — a re-register is
	// idempotent and preserves a stored rename (handleRegisterWorkspace's title guard).
	workspaceID := "workspace-" + sessionID
	rollback := d.newDelegationRollback()
	if d.store.GetWorkspace(workspaceID) == nil {
		d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
			Cmd:       protocol.CmdRegisterWorkspace,
			ID:        workspaceID,
			Title:     seed.Title,
			Directory: directory,
		})
		if d.store.GetWorkspace(workspaceID) == nil {
			return nil, fmt.Errorf("create resume workspace")
		}
		rollback.onWorkspaceCreated(workspaceID)
	}

	paneID := "pane-" + sessionID
	paneClient := newInternalWSClient()
	d.handleWorkspaceLayoutAddSessionPane(paneClient, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(seed.Title),
	})
	if _, err := readInternalActionResult(paneClient); err != nil {
		return nil, rollback.fail(fmt.Errorf("create resume pane: %w", err))
	}
	rollback.onPaneCreated(sessionID)

	spawnClient := newInternalWSClient()
	d.handleSpawnSession(spawnClient, &protocol.SpawnSessionMessage{
		Cmd:             protocol.CmdSpawnSession,
		ID:              sessionID,
		Cwd:             directory,
		WorkspaceID:     workspaceID,
		Agent:           agent,
		Cols:            80,
		Rows:            24,
		Label:           protocol.Ptr(seed.Title),
		ResumeSessionID: protocol.Ptr(resumeID),
	})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		return nil, rollback.fail(fmt.Errorf("spawn resume session: %w", err))
	}

	if session := d.store.Get(sessionID); session == nil {
		return nil, rollback.fail(fmt.Errorf("resume session was not persisted"))
	}
	rollback.onSessionSpawned(sessionID)
	if err := d.bindResumedSeed(seed, seedDoc, sessionID, directory, agent, resumeID); err != nil {
		return nil, rollback.fail(err)
	}
	rollback.abandon()

	d.logf("resume: reopened seed %q as session %s in %s", seedID, sessionID, directory)
	return &seedResumeOutcome{SessionID: sessionID, WorkspaceID: workspaceID}, nil
}

func (d *Daemon) bindResumedSeed(
	seed garden.Seed,
	seedDoc docstore.Document,
	sessionID, directory, agent, resumeID string,
) error {
	next, err := garden.Transition(seed, garden.VerbTend, garden.Ask{
		Actor: garden.Tender{Session: sessionID},
	}, d.sessionExists)
	if err != nil {
		return fmt.Errorf("reclaim %s after resume: %w", seed.ID, err)
	}
	if next.Status != seed.Status {
		next.StateChangedAt = formatGardenTime(d.gardenTime())
	}
	next.LastExecutionID = sessionID

	seedSchema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return err
	}
	seedBody, err := next.Encode()
	if err != nil {
		return err
	}

	dispatch, dispatchDoc, found, err := d.gardenDispatchDocument(sessionID)
	if err != nil {
		return err
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("resumed session %s is not tracked", sessionID)
	}
	dispatch = mergeGardenExecution(dispatch, observedGardenExecution(session, resumeID, d.gardenTime()))
	dispatch.SessionID = sessionID
	dispatch.Crown = seed.ID
	dispatch.SupersededBy = ""
	if dispatch.Cwd == "" {
		dispatch.Cwd = directory
	}
	if dispatch.Agent == "" {
		dispatch.Agent = agent
	}
	dispatch.Resume = resumeID
	dispatchBody, err := dispatch.Encode()
	if err != nil {
		return err
	}

	seedExpected := seedDoc.Rev
	dispatchExpected := docstore.ExpectAbsent
	if found {
		dispatchExpected = dispatchDoc.Rev
	}
	seedFact := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)
	dispatchFact := documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false)
	commits := []store.DocumentCommit{
		{
			Write: store.DocumentWrite{Schema: *seedSchema, ID: seed.ID, Body: seedBody, Expected: &seedExpected},
			Fact:  seedFact,
		},
		{
			Write: store.DocumentWrite{Schema: *dispatchSchema, ID: sessionID, Body: dispatchBody, Expected: &dispatchExpected},
			Fact:  dispatchFact,
		},
	}
	written, err := d.store.CommitDocumentWrites(commits, d.gardenTime())
	if err != nil {
		if docstore.IsConflict(err) {
			return fmt.Errorf("%s changed while its conversation was resuming; refresh it and try again", seed.ID)
		}
		return err
	}
	d.announceCommittedWrite(seedFact, written[0].Seq)
	d.announceCommittedWrite(dispatchFact, written[1].Seq)
	d.publishFact(FactGardenTended, seed.ID, nil)
	d.rememberDispatchSeed(sessionID, seed.ID)
	d.rememberDispatchFromChief(sessionID, dispatch.FromChief)
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbTend], sessionID, "")
	return nil
}

func (d *Daemon) handleSeedResume(client *wsClient, msg *protocol.SeedResumeMessage) {
	requestID := protocol.Deref(msg.RequestID)
	outcome, err := d.resumeSeed(msg.SeedID)
	response := protocol.SeedResumeResultMessage{
		Event:     protocol.EventSeedResumeResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.SessionID = protocol.Ptr(outcome.SessionID)
		response.WorkspaceID = protocol.Ptr(outcome.WorkspaceID)
		if outcome.AlreadyRunning {
			response.AlreadyRunning = protocol.Ptr(true)
		}
	}
	d.sendToClient(client, response)
}
