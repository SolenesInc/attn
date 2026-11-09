package daemon

import (
	"fmt"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) reviveMembersInterruptedSession(memberID string) bool {
	latest, err := d.store.MemberLatestSession(memberID)
	if err != nil {
		d.logf("crew: reading %s's latest session: %v", memberID, err)
		return false
	}
	session := d.store.Get(latest)
	if session == nil || session.State != protocol.SessionStateRecoverable {
		return false
	}
	d.awaitHarness(string(session.Agent))
	if !d.agentAvailable(string(session.Agent)) {
		return false
	}
	if err := d.reviveSession(latest, 80, 24); err != nil {
		d.logf("crew: reviving %s's interrupted session %s failed: %v", memberID, latest, err)
		return false
	}
	return true
}

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
		return fmt.Errorf("%s is in session %s; session %s comes back only while %s is between sessions",
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
