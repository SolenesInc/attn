package daemon

import (
	"errors"
	"fmt"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
)

var (
	errChiefOfStaffProtected = errors.New("chief of staff is protected from closing; unset the chief role first")
	errCrewRosterUnavailable = errors.New("crew roster is unavailable; try again before closing this session")
)

func (d *Daemon) sessionCloseError(sessionID string) error {
	if d.isChiefOfStaffSession(sessionID) {
		return errChiefOfStaffProtected
	}
	members, _, err := d.readCrewMembers()
	if docstore.IsUndeclaredCollection(err) {
		return nil
	}
	if err != nil {
		d.logf("crew: refusing to close session %s because its crew identity could not be read: %v", sessionID, err)
		return errCrewRosterUnavailable
	}
	for _, member := range members {
		if member.BindingSession == sessionID {
			name := crew.DisplayName(member.ID)
			return fmt.Errorf("%s is protected from closing; put %s to sleep first", name, name)
		}
	}
	return nil
}
