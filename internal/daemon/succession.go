package daemon

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// opened opens a session in terminal t for a conversation no session holds; it takes only the terminal,
// and from closes into the ledger whole unless it runs on elsewhere. The caller holds from's lifecycle lock.
func (d *Daemon) opened(t harness.TerminalID, from *protocol.Session, observation agentConversationObservation) error {
	launch, _ := d.store.LaunchIntent(from.ID)
	launch.ChiefOfStaff = false
	to := uuid.NewString()
	return d.succeed(t, from, store.Succession{
		To:     to,
		Label:  defaultSessionLabel(from.Directory, to),
		Launch: launch,
		Close:  store.SessionClose{Reason: "cleared; its terminal moved on to " + to},
	}, observation)
}

// shows puts owner, which holds the conversation t reports, in t: a closed owner reopens in place, an
// open one drops its dead panes and keeps its live ones, all in t's workspace. The caller holds both lifecycle locks.
func (d *Daemon) shows(t harness.TerminalID, from *protocol.Session, owner string, observation agentConversationObservation) error {
	return d.succeed(t, from, store.Succession{
		To:    owner,
		Close: store.SessionClose{Reason: "resumed; its terminal moved on to " + owner},
	}, observation)
}

// succeed moves t on from from to sc.To. A from that another live terminal still runs stays open there.
func (d *Daemon) succeed(t harness.TerminalID, from *protocol.Session, sc store.Succession, observation agentConversationObservation) error {
	live := d.liveTerminals(context.Background())
	sc.KeepFrom = slices.ContainsFunc(d.terminals().Of(harness.SessionID(from.ID)), func(id harness.TerminalID) bool {
		_, running := live[id]
		return running && id != t
	})
	if !sc.KeepFrom {
		d.drainTranscriptWatcher(from.ID)
		if _, err := d.captureGardenSessionSnapshot(from); err != nil {
			d.logf("garden: preserving execution %s before its terminal moved on: %v", from.ID, err)
		}
	}
	sc.From = from.ID
	sc.Conversation = store.SessionConversation{NativeID: observation.NativeID, TranscriptPath: observation.TranscriptPath}
	var layouts []workspacelayout.WorkspaceLayout
	err := d.commitWorkspaceLayouts(func() (_ []workspacelayout.WorkspaceLayout, err error) {
		if layouts, err = d.successionLayouts(t, sc.To, live); err != nil {
			return nil, err
		}
		return layouts, d.store.CommitSuccession(sc, layouts, time.Now())
	})
	if err != nil {
		d.ensureTranscriptWatcherAtPath(from.ID, d.store.GetSessionConversation(from.ID).TranscriptPath)
		return err
	}

	workspaceID := layouts[0].WorkspaceID
	d.sessionInputs().handOverSubmit(from.ID, sc.To)
	if left := d.workspaces.workspaceIDForSession(sc.To); left != "" && left != workspaceID {
		d.dissociateSessionFromWorkspace(sc.To)
	}
	d.associateSessionWithWorkspace(sc.To, workspaceID)
	terminal, _ := d.evidenceTable().snapshot(from.ID)
	d.startEvidence(sc.To, sessionstate.Evidence{
		Heartbeat:      terminal.Heartbeat,
		Process:        terminal.Process,
		ReviewerInLoop: terminal.ReviewerInLoop,
		LastBusyAt:     terminal.LastBusyAt,
		LastMovement:   terminal.LastMovement,
	})
	d.startTranscriptWatcherAtPath(sc.To, from.Agent, from.Directory, d.sessionStartedAt(sc.To), observation.TranscriptPath)
	d.publishFact(FactSessionRegistered, sc.To, nil)
	for _, layout := range layouts {
		if d.store.GetWorkspace(layout.WorkspaceID) != nil {
			d.broadcastWorkspaceLayoutUpdated(layout.WorkspaceID)
		}
	}

	if !sc.KeepFrom {
		d.recordSessionClose(from.ID, func() (bool, error) { return true, nil })
		d.publishSessionUnregistered(from)
		d.dissociateSessionFromWorkspace(from.ID)
	}
	d.recomputeAndBroadcastWorkspaceForSession(sc.To)
	d.logf("terminal %s moved on from session %s to %s for conversation %s", t, from.ID, sc.To, observation.NativeID)
	return nil
}

// successionLayouts points t's panes at to and drops the other panes that place to whose terminals
// are not live. It returns every layout it changed, t's first.
func (d *Daemon) successionLayouts(t harness.TerminalID, to string, live map[harness.TerminalID]struct{}) ([]workspacelayout.WorkspaceLayout, error) {
	r := d.terminals()
	var layouts []workspacelayout.WorkspaceLayout
	edit := func(term harness.TerminalID, change func(*workspacelayout.WorkspaceLayout)) error {
		workspaceID := r.workspaceOf(term)
		i := slices.IndexFunc(layouts, func(l workspacelayout.WorkspaceLayout) bool { return l.WorkspaceID == workspaceID })
		if i < 0 {
			layout := d.store.GetWorkspaceLayout(workspaceID)
			if layout == nil {
				return fmt.Errorf("no layout places terminal %s", term)
			}
			layouts = append(layouts, *layout)
			i = len(layouts) - 1
		}
		change(&layouts[i])
		return nil
	}
	if err := edit(t, func(layout *workspacelayout.WorkspaceLayout) {
		for i := range layout.Panes {
			if layout.Panes[i].RuntimeID == string(t) {
				layout.Panes[i].SessionID = to
			}
		}
	}); err != nil {
		return nil, err
	}
	for _, dead := range r.Of(harness.SessionID(to)) {
		if _, kept := live[dead]; kept {
			continue
		}
		if err := edit(dead, func(layout *workspacelayout.WorkspaceLayout) {
			layout.Panes = slices.DeleteFunc(layout.Panes, func(p workspacelayout.Pane) bool { return p.RuntimeID == string(dead) })
		}); err != nil {
			return nil, err
		}
	}
	for i := range layouts {
		layouts[i] = workspacelayout.NormalizeWorkspaceLayout(layouts[i])
	}
	return layouts, nil
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
