package daemon

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// opened opens a session in terminal t for a conversation no session holds; it takes only the terminal,
// and from closes into the ledger whole, or is removed if it was never prompted. The caller holds from's lifecycle lock.
func (d *Daemon) opened(t harness.TerminalID, from *protocol.Session, observation agentConversationObservation) error {
	d.drainTranscriptWatcher(from.ID)
	if _, err := d.captureGardenSessionSnapshot(from); err != nil {
		d.logf("garden: preserving execution %s before its terminal moved on: %v", from.ID, err)
	}
	launch, _ := d.store.LaunchIntent(from.ID)
	launch.ChiefOfStaff = false
	to := uuid.NewString()
	succession := store.Succession{
		From:         from.ID,
		To:           to,
		Label:        defaultSessionLabel(from.Directory, to),
		Conversation: store.SessionConversation{NativeID: observation.NativeID, TranscriptPath: observation.TranscriptPath},
		Launch:       launch,
		Close:        store.SessionClose{By: store.SessionClosedByUser, Reason: "cleared; its terminal moved on to " + to},
	}
	var workspaceID string
	var removed bool
	err := d.commitWorkspaceLayout(func() (workspacelayout.WorkspaceLayout, error) {
		workspaceID = d.terminals().workspaceOf(t)
		layout := d.store.GetWorkspaceLayout(workspaceID)
		if layout == nil {
			return workspacelayout.WorkspaceLayout{}, fmt.Errorf("no layout places terminal %s", t)
		}
		for i := range layout.Panes {
			if layout.Panes[i].RuntimeID == string(t) {
				layout.Panes[i].SessionID = to
			}
		}
		var err error
		removed, err = d.store.CommitSuccession(succession, *layout, time.Now())
		return *layout, err
	})
	if err != nil {
		d.ensureTranscriptWatcherAtPath(from.ID, d.store.GetSessionConversation(from.ID).TranscriptPath)
		return err
	}

	d.sessionInputs().handOverSubmit(from.ID, to)
	d.associateSessionWithWorkspace(to, workspaceID)
	terminal, _ := d.evidenceTable().snapshot(from.ID)
	d.startEvidence(to, sessionstate.Evidence{
		Heartbeat:      terminal.Heartbeat,
		Process:        terminal.Process,
		ReviewerInLoop: terminal.ReviewerInLoop,
		LastBusyAt:     terminal.LastBusyAt,
		LastMovement:   terminal.LastMovement,
	})
	d.startTranscriptWatcherAtPath(to, from.Agent, from.Directory, d.sessionStartedAt(to), observation.TranscriptPath)
	d.publishFact(FactSessionRegistered, to, nil)
	d.broadcastWorkspaceLayoutUpdated(workspaceID)

	d.recordSessionClose(from.ID, func() (bool, error) { return !removed, nil })
	d.publishSessionUnregistered(from)
	d.dissociateSessionFromWorkspace(from.ID)
	d.recomputeAndBroadcastWorkspaceForSession(to)
	d.logf("terminal %s moved on from session %s to %s for conversation %s (predecessor removed=%v)", t, from.ID, to, observation.NativeID, removed)
	return nil
}

// drainTranscriptWatcher stops a session's watcher and waits for its last usage reconcile.
func (d *Daemon) drainTranscriptWatcher(sessionID string) {
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	d.stopTranscriptWatcher(sessionID)
	if watcher != nil {
		<-watcher.doneCh
	}
}
