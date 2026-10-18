package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) mayActOnSession(callerID protocol.SessionID, target *protocol.Session, action string) error {
	if callerID == "" || callerID == target.ID {
		return nil
	}
	b, err := d.bindings()
	if err != nil {
		return err
	}
	r, err := d.requestFromSession(callerID, b)
	if err != nil {
		return err
	}
	if r.ProfileID() != target.ProfileID {
		return fmt.Errorf("unknown live session %s", target.ID)
	}
	if d.requestedByChief(r) {
		return nil
	}
	dispatchers := d.gardenDispatchersBySession()
	seen := map[protocol.SessionID]bool{}
	for id := target.ID; !seen[id]; {
		seen[id] = true
		party, _ := dispatchers[id].Party()
		dispatcher, ok := b.SessionOf(party)
		if !ok {
			break
		}
		ancestor := d.store.Get(dispatcher)
		if ancestor == nil || ancestor.ProfileID != r.ProfileID() {
			break
		}
		if dispatcher == callerID {
			return nil
		}
		id = dispatcher
	}
	return fmt.Errorf("delegation-chain rule: a session may %s itself or an agent in its delegation chain; the profile's Chief may act on any agent. %s (%s) is outside %s's chain", action, target.ID, target.Label, callerID)
}
