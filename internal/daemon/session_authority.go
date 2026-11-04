package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) mayActOnSession(callerID protocol.SessionID, target *protocol.Session, action string) error {
	if callerID == "" || callerID == target.ID {
		return nil
	}
	caller := d.store.Get(callerID)
	if caller == nil || caller.ProfileID != target.ProfileID {
		return fmt.Errorf("unknown live session %s", target.ID)
	}
	if d.chiefOfProfile(target.ProfileID) == callerID || d.inDelegationChain(callerID, caller.ProfileID, target.ID) {
		return nil
	}
	return fmt.Errorf("delegation-chain rule: a session may %s itself or an agent in its delegation chain; the profile's chief may act on any agent. %s (%s) is outside %s's chain", action, target.ID, target.Label, callerID)
}

func (d *Daemon) inDelegationChain(callerID protocol.SessionID, profileID string, targetID protocol.SessionID) bool {
	dispatchers := d.gardenDispatchersBySession()
	seen := map[protocol.SessionID]bool{}
	for id := targetID; !seen[id]; {
		seen[id] = true
		dispatcher, ok := d.sessionOfActor(dispatchers[id])
		if !ok {
			return false
		}
		ancestor := d.store.Get(dispatcher)
		if ancestor == nil || ancestor.ProfileID != profileID {
			return false
		}
		if dispatcher == callerID {
			return true
		}
		id = dispatcher
	}
	return false
}
