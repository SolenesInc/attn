package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) profileChiefs() map[string]protocol.SessionID {
	if d.store == nil {
		return map[string]protocol.SessionID{}
	}
	return d.chiefMemberSessions()
}

func (d *Daemon) legacyProfileChiefs() map[string]protocol.SessionID {
	if d.store == nil {
		return map[string]protocol.SessionID{}
	}
	byProfile, err := d.store.ProfileChiefs()
	if err != nil {
		d.logf("read profile chiefs: %v", err)
	}
	return byProfile
}

func (d *Daemon) chiefOfProfile(profileID string) protocol.SessionID {
	if profileID == "" {
		return ""
	}
	return d.profileChiefs()[profileID]
}

func (d *Daemon) isChiefOfStaffSession(sessionID protocol.SessionID) bool {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" || d.store == nil {
		return false
	}
	profileID, err := d.store.SessionProfileID(sessionID)
	return err == nil && d.chiefOfProfile(profileID) == sessionID
}

func (d *Daemon) profileForClient(client *wsClient) string {
	if profileID := client.selectedProfile(); profileID != "" {
		return profileID
	}
	profile, err := d.callerProfile("")
	if err != nil {
		return ""
	}
	return profile.ID
}

func (d *Daemon) decorateChiefOfStaff(session *protocol.Session, chiefByProfile map[string]protocol.SessionID) {
	if session == nil {
		return
	}
	if session.ProfileID != "" && chiefByProfile[session.ProfileID] == session.ID {
		session.ChiefOfStaff = protocol.Ptr(true)
		return
	}
	session.ChiefOfStaff = nil
}

func (d *Daemon) delegatedFromChiefSessionIDs() map[protocol.SessionID]bool {
	if d.store == nil {
		return nil
	}
	delegated := map[protocol.SessionID]bool{}
	for sessionID := range d.gardenDispatchesFromChief() {
		delegated[sessionID] = true
	}
	return delegated
}

func (d *Daemon) decorateDelegatedFromChief(session *protocol.Session, delegatedFromChief map[protocol.SessionID]bool) {
	if session == nil {
		return
	}
	if delegatedFromChief[session.ID] {
		session.DelegatedFromChief = protocol.Ptr(true)
		return
	}
	session.DelegatedFromChief = nil
}

func (d *Daemon) sessionExists(sessionID protocol.SessionID) bool {
	if d.store != nil && d.store.Get(sessionID) != nil {
		return true
	}
	return d.store != nil && d.store.DelegationSessionReserved(sessionID)
}

func (d *Daemon) sessionActive(sessionID protocol.SessionID) bool {
	return d.partyHolds(who.Session(sessionID))
}
