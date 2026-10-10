package daemon

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/who"

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
	to := protocol.SessionID(uuid.NewString())
	member, bound := d.crewMemberForSession(from.ID)
	var memberKey who.MemberKey
	if bound {
		memberKey = member.Key
	}
	return d.succeed(t, from, store.Succession{
		To:     to,
		Label:  defaultSessionLabel(from.Directory, to),
		Launch: launch,
		Close:  store.SessionClose{Reason: string("cleared; its terminal moved on to " + to)},
	}, observation, memberKey)
}

// shows puts owner, which holds the conversation t reports and runs in no live terminal, in t: a closed
// owner reopens in place, an open one leaves its dead panes. The caller holds both lifecycle locks.
func (d *Daemon) shows(t harness.TerminalID, from *protocol.Session, owner protocol.SessionID, observation agentConversationObservation) error {
	return d.succeed(t, from, store.Succession{
		To:    owner,
		Close: store.SessionClose{Reason: string("resumed; its terminal moved on to " + owner)},
	}, observation, who.MemberKey{})
}

func (d *Daemon) succeed(t harness.TerminalID, from *protocol.Session, sc store.Succession, observation agentConversationObservation, memberKey who.MemberKey) error {
	unlockEnds := d.lockTerminalEnds(from.ID)
	sc.KeepFrom = d.othersRun(from.ID, t)
	if !sc.KeepFrom && !memberKey.IsZero() {
		sc.Label = d.memberName(memberKey)
	}
	release := func() {}
	if !sc.KeepFrom {
		release = d.drainTranscriptWatcher(from.ID)
		if _, err := d.captureGardenSessionSnapshot(from); err != nil {
			d.logf("garden: preserving execution %s before its terminal moved on: %v", from.ID, err)
		}
	}
	sc.From = from.ID
	sc.Conversation = store.SessionConversation{NativeID: observation.NativeID, TranscriptPath: observation.TranscriptPath}
	_, err := d.store.CommitSuccession(sc, t)
	unlockEnds()
	defer release()
	if err != nil {
		release()
		if !sc.KeepFrom {
			d.ensureTranscriptWatcherAtPath(from.ID, d.store.GetSessionConversation(from.ID).TranscriptPath)
		}
		return err
	}

	if !sc.KeepFrom && !memberKey.IsZero() {
		pausepoint.At(pausepoint.MemberClearCommit)
		if member, bound := d.crewMemberForSession(from.ID); bound {
			if restart, pending := pendingCrewRestartFor(member, from.ID); pending {
				if err := d.failCrewRestart(memberKey, restart.RequestID, from.ID, "", fmt.Errorf("the day was cleared before it filed its letter")); err != nil {
					d.logf("crew: clear restart: %v", err)
				}
			}
		}
		if err := d.transferCrewBinding(memberKey, from.ID, sc.To); err != nil {
			d.logf("crew: clear binding: %v", err)
		} else {
			if err := d.store.RecordMemberSession(memberKey, sc.To); err != nil {
				d.logf("crew: record cleared member session: %v", err)
			}
			d.life.Go("crew-retired-clear", func() {
				identity, err := d.store.CrewIdentity(memberKey)
				if err != nil {
					d.logf("crew: read cleared member retirement: %v", err)
					return
				}
				if !identity.Retired {
					return
				}
				if _, err := d.crewSleep(requestFromApp(identity.ProfileID), "member:"+memberKey.String()); err != nil {
					d.logf("crew: request sleep after retired clear: %v", err)
				}
			})
		}
	}

	d.sessionInputs().handOverSubmit(from.ID, sc.To)
	terminal, _ := d.evidenceTable().snapshot(from.ID)
	d.startEvidence(sc.To, sessionstate.Evidence{
		Heartbeat:      terminal.Heartbeat,
		ProgramStatus:  terminal.ProgramStatus,
		Process:        terminal.Process,
		ReviewerInLoop: terminal.ReviewerInLoop,
		LastBusyAt:     terminal.LastBusyAt,
		LastMovement:   terminal.LastMovement,
	})
	d.startTranscriptWatcherAtPath(sc.To, from.Agent, from.Directory, d.sessionStartedAt(sc.To), observation.TranscriptPath)
	d.publishFact(FactSessionRegistered, string(sc.To), nil)
	d.publishArrangementChanged(from.ProfileID)

	if !sc.KeepFrom {
		d.recordSessionClose(from.ID, func() (bool, error) { return true, nil })
		d.publishSessionUnregistered(from)
	}
	d.logf("terminal %s moved on from session %s to %s for conversation %s", t, from.ID, sc.To, observation.NativeID)
	return nil
}

// drainTranscriptWatcher stops a session's usage watchers, waits for their last reconcile, and refuses
// new ones until release.
func (d *Daemon) drainTranscriptWatcher(sessionID protocol.SessionID) (release func()) {
	d.watchersMu.Lock()
	if d.usageDraining == nil {
		d.usageDraining = make(map[protocol.SessionID]bool)
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
