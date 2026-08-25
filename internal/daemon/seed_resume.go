package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// Resume writes NOTHING to the seed: reopening a delegate is not a note about its
// work, and the seed's lifecycle is only ever moved by a deliberate verb.

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
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(seed.TenderSession)
	if sessionID != "" {
		if existing := d.store.Get(sessionID); existing != nil {
			// The tender is still tracked — re-spawning its id would poison the local store;
			// a dead-but-recoverable pane revives itself via the attach path on mount.
			return &seedResumeOutcome{
				SessionID:      existing.ID,
				WorkspaceID:    existing.WorkspaceID,
				AlreadyRunning: true,
			}, nil
		}
	}

	dispatch, hasDispatch := d.gardenDispatch(sessionID)
	cwd, agent, resumeID := "", "", ""
	seedFallback := false
	if hasDispatch {
		cwd = strings.TrimSpace(dispatch.Cwd)
		agent = strings.TrimSpace(dispatch.Agent)
	} else {
		resumeID, cwd, agent = strings.TrimSpace(seed.ResumeSessionID), strings.TrimSpace(seed.ResumeCwd), strings.TrimSpace(seed.ResumeAgent)
		if resumeID == "" || cwd == "" || agent == "" {
			if sessionID == "" {
				return nil, fmt.Errorf("%s is untended — there is nobody to reopen; `attn seed notes %s` has its log", seedID, seedID)
			}
			return nil, fmt.Errorf("%s was tended by session %s, which attn did not launch — nothing to reopen", seedID, sessionID)
		}
		seedFallback = true
		if sessionID == "" {
			sessionID = resumeID
		}
		if existing := d.store.Get(sessionID); existing != nil {
			return &seedResumeOutcome{
				SessionID: existing.ID, WorkspaceID: existing.WorkspaceID, AlreadyRunning: true,
			}, nil
		}
	}
	if cwd == "" || agent == "" {
		return nil, fmt.Errorf("%s has no agent session to resume", seedID)
	}

	var directResume *string
	if seedFallback {
		directResume = protocol.Ptr(resumeID)
	}

	// A worktree may have been removed since the session closed — validate before any
	// side effects so a missing directory is a clean error, not a phantom workspace.
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

	// ResumePicker (not a passed ResumeSessionID) keeps handleSpawnSession the single
	// resume-id resolver, downgrading to the cwd picker when the transcript is gone.
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
		ResumePicker:    protocol.Ptr(true),
		ResumeSessionID: directResume,
	})
	if _, err := readInternalActionResult(spawnClient); err != nil {
		return nil, rollback.fail(fmt.Errorf("spawn resume session: %w", err))
	}

	if session := d.store.Get(sessionID); session == nil {
		return nil, rollback.fail(fmt.Errorf("resume session was not persisted"))
	}
	if seedFallback {
		if err := d.recordGardenDispatch(sessionID, seed.ID, "", directory, agent, false); err != nil {
			d.logf("resume: reopened %s but could not bind its fallback session %s: %v", seed.ID, sessionID, err)
		} else {
			d.rememberDispatchResume(sessionID, resumeID)
		}
	}

	d.logf("resume: reopened seed %q as session %s in %s", seedID, sessionID, directory)
	return &seedResumeOutcome{SessionID: sessionID, WorkspaceID: workspaceID}, nil
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
