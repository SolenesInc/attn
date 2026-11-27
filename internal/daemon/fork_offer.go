package daemon

import (
	"errors"
	"fmt"
	"sync"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

type forkOffers struct {
	mu       sync.Mutex
	original map[protocol.SessionID]protocol.SessionID
}

func (o *forkOffers) offer(fork, original protocol.SessionID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.original == nil {
		o.original = make(map[protocol.SessionID]protocol.SessionID)
	}
	o.original[fork] = original
}

func (o *forkOffers) of(fork protocol.SessionID) (protocol.SessionID, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	original, ok := o.original[fork]
	return original, ok
}

func (o *forkOffers) end(fork protocol.SessionID) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.original[fork]
	delete(o.original, fork)
	return ok
}

var errForkOriginalGone = errors.New("the session it was forked from is gone")

func (d *Daemon) decorateSessionForkOffer(clone *protocol.Session) {
	clone.ForkOffer = nil
	if original, ok := d.forkOffers.of(clone.ID); ok {
		clone.ForkOffer = protocol.Ptr(original)
	}
}

func (d *Daemon) endForkOffer(fork protocol.SessionID) {
	if d.forkOffers.end(fork) {
		d.publishFact(FactSessionReregistered, string(fork), nil)
	}
}

func (d *Daemon) handleForkOfferDismiss(msg *protocol.ForkOfferDismissMessage) {
	d.endForkOffer(protocol.TrimID(msg.SessionID))
}

func (d *Daemon) handleForkOfferOpen(client *wsClient, msg *protocol.ForkOfferOpenMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		forkID := protocol.TrimID(msg.SessionID)
		fork := d.store.Get(forkID)
		if fork == nil {
			return profileActionOutcome{}, profiles.Errorf(profiles.CodeNotFound, "session %s is not open", forkID)
		}
		if profile := d.profileForClient(client); fork.ProfileID != profile {
			return profileActionOutcome{}, profiles.Errorf(profiles.CodeCrossProfile, "session %s belongs to profile %q, not profile %q", forkID, fork.ProfileID, profile)
		}
		original, offered := d.forkOffers.of(forkID)
		if !offered {
			return profileActionOutcome{}, nil
		}
		ended := func() { d.endForkOffer(forkID) }
		beside := d.placementBeside(forkID)
		if beside != nil {
			beside.focus = true
		}
		if d.store.Get(original) != nil {
			return d.showSession(client, original, beside, ended)
		}
		if d.store.SessionLedgerEntry(original) == nil {
			ended()
			return profileActionOutcome{}, fmt.Errorf("open the original of %s: %w", forkID, errForkOriginalGone)
		}
		return profileActionOutcome{finish: func() (profileActionOutcome, error) {
			if _, err := d.reopenSession(original, protocol.SessionReopenActionReopen, beside); err != nil {
				return profileActionOutcome{}, fmt.Errorf("reopen %s, which %s was forked from: %w", original, forkID, err)
			}
			return d.showSession(client, original, nil, ended)
		}}, nil
	})
}
