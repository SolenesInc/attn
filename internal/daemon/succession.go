package daemon

import (
	"context"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// opened opens a session in terminal t for a conversation no session holds; it takes only the terminal,
// and from closes into the ledger whole. The caller holds from's lifecycle lock.
func (d *Daemon) opened(t harness.TerminalID, from *protocol.Session, observation agentConversationObservation) error {
	launch, _ := d.store.LaunchIntent(from.ID)
	launch.ChiefOfStaff = false
	to := protocol.SessionID(uuid.NewString())
	return d.succeed(t, from, store.Succession{
		To:     to,
		Launch: launch,
		Close:  store.SessionClose{Reason: string("cleared; its terminal moved on to " + to)},
	}, observation)
}

// shows puts owner, which holds the conversation t reports and runs in no live terminal, in t: a closed
// owner reopens in place, an open one leaves its dead panes. The caller holds both lifecycle locks.
func (d *Daemon) shows(t harness.TerminalID, from *protocol.Session, owner protocol.SessionID, observation agentConversationObservation) error {
	return d.succeed(t, from, store.Succession{
		To:     owner,
		Close:  store.SessionClose{Reason: string("resumed; its terminal moved on to " + owner)},
		KeepTo: d.store.Get(owner) != nil && d.linkOf(owner) != nil,
	}, observation)
}

func (d *Daemon) succeed(t harness.TerminalID, from *protocol.Session, sc store.Succession, observation agentConversationObservation) error {
	unlockEnds := d.lockTerminalEnds(from.ID)
	live := d.liveTerminals(context.Background())
	sc.KeepFrom = d.othersRun(from.ID, t) || d.keepsWhenLeft(from.ID)
	sc.Live = make(map[string]bool, len(live))
	for id := range live {
		sc.Live[string(id)] = true
	}
	if !sc.KeepFrom {
		if _, err := d.captureGardenSessionSnapshot(from); err != nil {
			d.logf("garden: preserving execution %s: %v", from.ID, err)
		}
	}
	sc.From = from.ID
	sc.Conversation = store.SessionConversation{NativeID: observation.NativeID, TranscriptPath: observation.TranscriptPath}
	_, closedAsks, err := d.store.CommitSuccession(sc, t)
	unlockEnds()
	if err != nil {
		return err
	}
	d.publishFact(FactSessionRegistered, string(sc.To), nil)
	d.publishArrangementChanged(from.ProfileID)
	d.codexShared().terminalMoved(t, from.ProfileID)
	d.mirrorLabel(sc.To)

	if !sc.KeepFrom {
		d.recordSessionClose(from.ID, func() (store.SessionClosure, error) {
			return store.SessionClosure{Recorded: true, Asks: closedAsks}, nil
		})
		d.publishSessionUnregistered(from)
	} else {
		if d.hidden(from.ID) {
			d.forgetProgramStatus(from.ID)
		}
		d.publishFact(FactSessionReregistered, string(from.ID), nil)
	}
	d.logf("terminal %s moved on from session %s to %s for conversation %s", t, from.ID, sc.To, observation.NativeID)
	return nil
}
