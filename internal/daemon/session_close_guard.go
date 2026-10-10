package daemon

import (
	"errors"
	"fmt"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

var (
	errChiefOfStaffProtected = errors.New("chief of staff is protected from closing; unset the chief role first")
	errCrewRosterUnavailable = errors.New("crew roster is unavailable; try again before closing this session")
)

func (d *Daemon) sessionCloseError(sessionID protocol.SessionID) error {
	if d.isChiefOfStaffSession(sessionID) {
		return errChiefOfStaffProtected
	}
	b, err := d.bindings()
	if docstore.IsUndeclaredCollection(err) {
		return nil
	}
	if err != nil {
		d.logf("crew: refusing to close session %s because its crew identity could not be read: %v", sessionID, err)
		return errCrewRosterUnavailable
	}
	if err := b.CheckSession(sessionID); err != nil {
		return err
	}
	party, _ := b.PartyOf(sessionID)
	if key, member := party.Member(); member {
		name := d.memberName(key)
		return fmt.Errorf("%s is protected from closing; put %s to sleep first", name, name)
	}
	return nil
}
