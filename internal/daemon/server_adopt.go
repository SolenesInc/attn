package daemon

import (
	"context"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) adoptServerConversation(a harness.Adoption) func(context.Context) error {
	if d.copiedSession(a.Parent) {
		return nil
	}
	if _, err := d.commitLocalAdoption(a); err != nil {
		return refused(err)
	}
	return nil
}

func (d *Daemon) commitLocalAdoption(a harness.Adoption) (protocol.SessionID, error) {
	outcome, err := d.store.CommitAdoption(store.Adoption{
		ID:           protocol.SessionID(uuid.NewString()),
		Parent:       a.Parent,
		Conversation: a.Conversation,
		Title:        a.Title,
		Reopen:       a.Reopen,
		CanReopen:    d.canAutoReopen,
	})
	if err != nil {
		return "", err
	}
	if !outcome.Created && !outcome.Reopened {
		return outcome.Session, nil
	}
	d.startSessionMonitor(outcome.Session)
	d.publishFact(FactSessionRegistered, string(outcome.Session), nil)
	d.life.Go("showAdoptedWorker", func() {
		d.showAdoptedWorker(outcome.Session, a.Parent)
	})
	return outcome.Session, nil
}

func (d *Daemon) canAutoReopen(sessionID protocol.SessionID) bool {
	return !d.sessionTeardownInFlight(sessionID) && !d.sessionReleaseInFlight(sessionID)
}

func (d *Daemon) showAdoptedWorker(sessionID, parentID protocol.SessionID) {
	if !d.hidden(sessionID) {
		return
	}
	spawn, policy, err := d.spawnShowing(sessionID, 80, 24)
	if err != nil {
		d.logf("show adopted worker %s: %v", sessionID, err)
		return
	}
	if beside := d.placementBeside(parentID); beside != nil {
		policy.launchPlacement = beside
	}
	policy.userStarted = false
	policy.onlyIfHidden = true
	if rejection := d.runSpawnPipeline(spawn, policy); rejection != nil {
		d.logf("show adopted worker %s: %v", sessionID, rejection.reason())
	}
}
