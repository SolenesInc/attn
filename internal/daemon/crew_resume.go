package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) resumeAsMember(sessionID protocol.SessionID, resume func() error) error {
	if d.store == nil {
		return resume()
	}
	key, err := d.store.SessionMemberKey(sessionID)
	if err != nil {
		return fmt.Errorf("read the crew member of session %s: %w", shortSessionID(sessionID), err)
	}
	if key == "" {
		return resume()
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	members, _, err := d.readCrewMembers()
	if err != nil {
		return err
	}
	member, ok := crew.Keyed(key, members)
	if !ok {
		return resume()
	}
	if member.Retired {
		return retiredMemberError(member)
	}
	awakeIn, _, err := d.crewAwakeSession(member)
	if err != nil {
		return err
	}
	if awakeIn != "" && awakeIn != sessionID {
		return fmt.Errorf("%s is awake in session %s; session %s comes back only while %s is asleep",
			member.Name, shortSessionID(awakeIn), shortSessionID(sessionID), member.Name)
	}
	if _, err := d.claimCrewBinding(member.ID, sessionID); err != nil {
		return err
	}
	if err := resume(); err != nil {
		if awakeIn == "" {
			d.releaseCrewBindingIfSession(sessionID)
		}
		return err
	}
	d.takeCrewExitedSession(member.ID)
	d.recordMemberSession(member.ID, sessionID)
	return nil
}
