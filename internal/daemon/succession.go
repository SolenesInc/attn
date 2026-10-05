package daemon

import (
	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/store"
)

// opened opens a session in terminal t for a conversation no session holds; it takes only the terminal,
// and from closes into the ledger whole. The caller holds from's lifecycle lock.
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

// shows puts owner, which holds the conversation t reports and runs in no live terminal, in t: a closed
// owner reopens in place, an open one leaves its dead panes. The caller holds both lifecycle locks.
func (d *Daemon) shows(t harness.TerminalID, from *protocol.Session, owner string, observation agentConversationObservation) error {
	return d.succeed(t, from, store.Succession{
		To:    owner,
		Close: store.SessionClose{Reason: "resumed; its terminal moved on to " + owner},
	}, observation)
}

func (d *Daemon) succeed(t harness.TerminalID, from *protocol.Session, sc store.Succession, observation agentConversationObservation) error {
	unlockEnds := d.lockTerminalEnds(from.ID)
	sc.KeepFrom = d.othersRun(from.ID, t)
	release := func() {}
	if !sc.KeepFrom {
		release = d.drainTranscriptWatcher(from.ID)
		if _, err := d.captureGardenSessionSnapshot(from); err != nil {
			d.logf("garden: preserving execution %s before its terminal moved on: %v", from.ID, err)
		}
	}
	sc.From = from.ID
	sc.Conversation = store.SessionConversation{NativeID: observation.NativeID, TranscriptPath: observation.TranscriptPath}
	changed, err := d.store.CommitSuccession(sc, string(t))
	unlockEnds()
	defer release()
	if err != nil {
		release()
		if !sc.KeepFrom {
			d.ensureTranscriptWatcherAtPath(from.ID, d.store.GetSessionConversation(from.ID).TranscriptPath)
		}
		return err
	}

	d.sessionInputs().handOverSubmit(from.ID, sc.To)
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
	d.publishArrangementChanged(changed[0].ProfileID)

	if !sc.KeepFrom {
		d.recordSessionClose(from.ID, func() (bool, error) { return true, nil })
		d.publishSessionUnregistered(from)
	}
	d.logf("terminal %s moved on from session %s to %s for conversation %s", t, from.ID, sc.To, observation.NativeID)
	return nil
}

// drainTranscriptWatcher stops a session's usage watchers, waits for their last reconcile, and refuses
// new ones until release.
func (d *Daemon) drainTranscriptWatcher(sessionID string) (release func()) {
	d.watchersMu.Lock()
	if d.usageDraining == nil {
		d.usageDraining = make(map[string]bool)
	}
	d.usageDraining[sessionID] = true
	watcher := d.transcriptWatch[sessionID]
	pluginWatcher := d.pluginUsageWatch[sessionID]
	delete(d.transcriptWatch, sessionID)
	delete(d.pluginUsageWatch, sessionID)
	idle := d.usageIdle[sessionID]
	d.watchersMu.Unlock()
	if pluginWatcher != nil {
		close(pluginWatcher.stopCh)
	}
	if watcher != nil {
		close(watcher.stopCh)
	}
	if idle != nil {
		<-idle
	}
	d.reconcileDeferredUsage(sessionID)
	return func() {
		d.watchersMu.Lock()
		delete(d.usageDraining, sessionID)
		d.watchersMu.Unlock()
	}
}
