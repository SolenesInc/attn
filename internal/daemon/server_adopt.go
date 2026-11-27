package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type pendingAdoptionKey struct {
	parent       protocol.SessionID
	conversation string
}

func (d *Daemon) adoptServerConversation(a harness.Adoption) func(context.Context) error {
	if d.copiedSession(a.Parent) {
		return func(ctx context.Context) error {
			return d.adoptOnHome(ctx, a)
		}
	}
	if _, err := d.commitLocalAdoption(a); err != nil {
		return refused(err)
	}
	return nil
}

func (d *Daemon) adoptServerConversationHere(ctx context.Context, msg protocol.AdoptServerConversationMessage) (protocol.AdoptServerConversationResult, error) {
	origin, remote := OriginOf(ctx)
	if remote && d.sessionHost(msg.ParentSessionID) != origin.Host {
		return protocol.AdoptServerConversationResult{}, fmt.Errorf("session %s does not run on %s", msg.ParentSessionID, origin.Name)
	}
	sessionID, err := d.commitLocalAdoption(harness.Adoption{
		Parent:       msg.ParentSessionID,
		Conversation: msg.Conversation,
		Title:        protocol.Deref(msg.Title),
		Reopen:       protocol.Deref(msg.Resume),
	})
	if err != nil {
		return protocol.AdoptServerConversationResult{}, err
	}
	if sessionID == "" {
		return protocol.AdoptServerConversationResult{}, nil
	}
	result := protocol.AdoptServerConversationResult{SessionID: protocol.Ptr(sessionID)}
	if remote {
		copyRows, err := d.store.SessionCopy(sessionID)
		if err != nil {
			return protocol.AdoptServerConversationResult{}, err
		}
		result.Copy = copyRows
	}
	return result, nil
}

func (d *Daemon) adoptOnHome(ctx context.Context, a harness.Adoption) error {
	conn, _, err := d.homeConn(d.enrollmentStatus())
	var res protocol.AdoptServerConversationResult
	if err == nil {
		err = conn.Call(ctx, bridgeCall, protocol.AdoptServerConversationMessage{
			Cmd:             "adopt_server_conversation",
			ParentSessionID: a.Parent,
			Conversation:    a.Conversation,
			Title:           protocol.Ptr(a.Title),
			Resume:          protocol.Ptr(a.Reopen),
		}, &res)
	}
	if err != nil {
		if !d.homeAnswered(err) && d.store.Get(a.Parent) != nil {
			d.recordPendingAdoption(a)
			pausepoint.At(pausepoint.OutpostAdoptionDeferred)
			if current := d.homeBridge(); current != nil && current != conn {
				d.retryPendingAdoptions()
			}
		} else {
			d.clearPendingAdoption(a.Parent, a.Conversation, true)
		}
		return err
	}
	d.clearPendingAdoption(a.Parent, a.Conversation, res.SessionID != nil || a.Reopen)
	if len(res.Copy) == 0 {
		return nil
	}
	if err := d.applyOutpostCopy(conn.Context(), conn.Peer().HomeID, res.Copy); err != nil {
		return fmt.Errorf("apply adopted session copy: %w", err)
	}
	return nil
}

func (d *Daemon) recordPendingAdoption(a harness.Adoption) {
	key := pendingAdoptionKey{parent: a.Parent, conversation: a.Conversation}
	d.pendingAdoptionsMu.Lock()
	if d.pendingAdoptions == nil {
		d.pendingAdoptions = make(map[pendingAdoptionKey]harness.Adoption)
	}
	if existing, ok := d.pendingAdoptions[key]; ok {
		a.Reopen = existing.Reopen || a.Reopen
		if a.Title == "" {
			a.Title = existing.Title
		}
	}
	d.pendingAdoptions[key] = a
	d.pendingAdoptionsMu.Unlock()
}

func (d *Daemon) clearPendingAdoption(parent protocol.SessionID, conversation string, force bool) {
	key := pendingAdoptionKey{parent: parent, conversation: conversation}
	d.pendingAdoptionsMu.Lock()
	if existing, ok := d.pendingAdoptions[key]; ok && (force || !existing.Reopen) {
		delete(d.pendingAdoptions, key)
	}
	d.pendingAdoptionsMu.Unlock()
}

func (d *Daemon) retryPendingAdoptions() {
	d.pendingAdoptionsMu.Lock()
	if len(d.pendingAdoptions) == 0 {
		d.pendingAdoptionsMu.Unlock()
		return
	}
	pending := make([]harness.Adoption, 0, len(d.pendingAdoptions))
	for _, a := range d.pendingAdoptions {
		pending = append(pending, a)
	}
	d.pendingAdoptionsMu.Unlock()

	d.life.Go("retryPendingAdoptions", func() {
		ctx, cancel := context.WithTimeout(d.life.Context(), 15*time.Second)
		defer cancel()
		for _, a := range pending {
			if d.store.Get(a.Parent) == nil {
				d.clearPendingAdoption(a.Parent, a.Conversation, true)
				continue
			}
			if err := d.adoptOnHome(ctx, a); err != nil {
				d.logf("retry outpost worker adoption %s: %v", a.Conversation, err)
			}
		}
	})
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
		if outcome.Session != "" && d.hidden(outcome.Session) {
			d.life.Go("showAdoptedWorker", func() {
				d.showAdoptedWorker(outcome.Session, a.Parent)
			})
		}
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
