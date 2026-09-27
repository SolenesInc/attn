package daemon

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) profileChiefs() map[string]string {
	if d.store == nil {
		return map[string]string{}
	}
	byProfile, err := d.store.ProfileChiefs()
	if err != nil {
		d.logf("read profile chiefs: %v", err)
	}
	return byProfile
}

func (d *Daemon) chiefOfProfile(profileID string) string {
	if profileID == "" {
		return ""
	}
	return d.profileChiefs()[profileID]
}

func (d *Daemon) isChiefOfStaffSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.store == nil {
		return false
	}
	profileID, err := d.store.SessionProfileID(sessionID)
	return err == nil && d.chiefOfProfile(profileID) == sessionID
}

func (d *Daemon) chiefForCaller(callerSessionID string) string {
	profile, err := d.callerProfile(callerSessionID)
	if err != nil {
		return ""
	}
	return d.chiefOfProfile(profile.ID)
}

func (d *Daemon) chiefForClient(client *wsClient) string {
	if profileID := client.selectedProfile(); profileID != "" {
		return d.chiefOfProfile(profileID)
	}
	return d.chiefForCaller("")
}

func (d *Daemon) defaultProfileChief() string {
	if d.store == nil {
		return ""
	}
	profile, err := d.store.OldestProfile()
	if err != nil {
		return ""
	}
	return profile.ChiefSessionID
}

func (d *Daemon) decorateChiefOfStaff(session *protocol.Session, chiefByProfile map[string]string) {
	if session == nil {
		return
	}
	if session.ProfileID != "" && chiefByProfile[session.ProfileID] == session.ID {
		session.ChiefOfStaff = protocol.Ptr(true)
		return
	}
	session.ChiefOfStaff = nil
}

func (d *Daemon) delegatedFromChiefSessionIDs() map[string]bool {
	if d.store == nil {
		return nil
	}
	delegated := d.store.TicketAssigneesOwnedByRole(store.TicketRoleChiefOfStaff)
	if delegated == nil {
		delegated = map[string]bool{}
	}
	for sessionID := range d.gardenDispatchesFromChief() {
		delegated[sessionID] = true
	}
	return delegated
}

func (d *Daemon) decorateDelegatedFromChief(session *protocol.Session, delegatedFromChief map[string]bool) {
	if session == nil {
		return
	}
	if delegatedFromChief[session.ID] {
		session.DelegatedFromChief = protocol.Ptr(true)
		return
	}
	session.DelegatedFromChief = nil
}

func (d *Daemon) sessionExists(sessionID string) bool {
	if d.store != nil && d.store.Get(sessionID) != nil {
		return true
	}
	if d.store != nil && d.store.DelegationSessionReserved(sessionID) {
		return true
	}
	return d.hubManager != nil && d.hubManager.RemoteSession(sessionID) != nil
}

func (d *Daemon) clearChiefOfStaffIfSession(sessionID string) {
	if d.store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if _, err := d.store.ClearProfileChief(sessionID); err != nil {
		d.logf("clear chief of staff role failed for session %s: %v", sessionID, err)
	}
}

func (d *Daemon) nudgeChiefOfStaff(sessionID, attemptKey, prompt string) bool {
	if d.store == nil {
		return false
	}
	if sessionID == "" || d.store.Get(sessionID) == nil {
		return false
	}
	itemID := "chief-inbox/" + strings.TrimSpace(attemptKey)
	if strings.TrimSpace(attemptKey) == "" {
		itemID = "chief-inbox/" + uuid.NewString()
	}
	delivery, _, err := d.store.EnqueueMaintenancePromptOnce(
		itemID, sessionID, "notebook-inbox", "", prompt, time.Now(),
	)
	if err != nil {
		d.logf("chief nudge: queue failed for %s: %v", sessionID, err)
		return false
	}
	err = d.deliverAgentMailboxItem(delivery)
	if err != nil && !errors.Is(err, errAgentMailboxDoorbellOutstanding) && !errors.Is(err, errAgentMailboxDoorbellInFlight) {
		d.logf("chief nudge: doorbell deferred for %s: %v", sessionID, err)
		return false
	}
	return true
}

func (d *Daemon) maybeAssignChiefOnSpawn(sessionID, agent, profileID string, requested bool, existingSession *protocol.Session) bool {
	if !requested || existingSession != nil || d.store == nil {
		return false
	}
	if !d.agentSupportsChiefGuidance(agent) {
		d.logf("create-as-chief: agent %q for session %s has no chief-guidance launch path; ignoring", agent, sessionID)
		return false
	}
	claimed, err := d.store.ClaimProfileChief(profileID, sessionID)
	if err != nil {
		d.logf("create-as-chief: claiming the chief of profile %s for session %s failed: %v", profileID, sessionID, err)
		return false
	}
	if !claimed {
		d.logf("create-as-chief: profile %s already has a chief (%s); ignoring request for session %s", profileID, d.chiefOfProfile(profileID), sessionID)
		return false
	}
	d.logf("create-as-chief: session %s is the chief of profile %s", sessionID, profileID)
	return true
}

func (d *Daemon) handleSetChiefOfStaff(client *wsClient, msg *protocol.SetChiefOfStaffMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		d.sendChiefOfStaffResult(client, sessionID, msg.ChiefOfStaff, "", fmt.Errorf("missing session_id"))
		return
	}

	profileID, err := d.store.SessionProfileID(sessionID)
	if err != nil || profileID == "" {
		d.sendChiefOfStaffResult(client, sessionID, msg.ChiefOfStaff, "", fmt.Errorf("session %s is not an agent of any profile, so it cannot hold a profile's chief role", sessionID))
		return
	}
	previousSessionID := d.chiefOfProfile(profileID)
	roleChanged := previousSessionID != sessionID
	if !msg.ChiefOfStaff {
		roleChanged = previousSessionID == sessionID
	}
	if msg.ChiefOfStaff {
		if !d.sessionExists(sessionID) {
			d.sendChiefOfStaffResult(
				client,
				sessionID,
				true,
				previousSessionID,
				fmt.Errorf("session not found: %s", sessionID),
			)
			return
		}
		if session := d.store.Get(sessionID); session != nil {
			if driver, ok := d.ensurePluginRegistry().driver(string(session.Agent)); ok {
				switch {
				case !driver.Capabilities["launch_instructions"]:
					d.sendChiefOfStaffResult(client, sessionID, true, previousSessionID, fmt.Errorf("agent %q cannot be chief of staff without launch_instructions capability", session.Agent))
					return
				case !driver.Capabilities["resume"]:
					d.sendChiefOfStaffResult(client, sessionID, true, previousSessionID, fmt.Errorf("agent %q cannot apply chief guidance without resume capability", session.Agent))
					return
				}
			}
		}
	}

	prepared := make([]*preparedPluginRoleReload, 0, 2)
	preparedSessions := make(map[string]bool)
	if roleChanged {
		desiredRoles := map[string]bool{sessionID: msg.ChiefOfStaff}
		if msg.ChiefOfStaff && previousSessionID != "" && previousSessionID != sessionID {
			desiredRoles[previousSessionID] = false
		}
		ids := make([]string, 0, len(desiredRoles))
		for id := range desiredRoles {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			reload, pluginSession, err := d.preparePluginRoleReload(id, desiredRoles[id])
			if err != nil {
				for i := len(prepared) - 1; i >= 0; i-- {
					prepared[i].abort()
				}
				d.sendChiefOfStaffResult(client, sessionID, msg.ChiefOfStaff, previousSessionID, fmt.Errorf("prepare chief guidance reload for %s: %w", id, err))
				return
			}
			if pluginSession {
				preparedSessions[id] = true
			}
			if reload != nil {
				prepared = append(prepared, reload)
			}
		}
	}
	abortPrepared := true
	defer func() {
		if !abortPrepared {
			return
		}
		for i := len(prepared) - 1; i >= 0; i-- {
			prepared[i].abort()
		}
	}()

	if msg.ChiefOfStaff {
		if _, _, err := d.store.SetProfileChief(sessionID); err != nil {
			d.sendChiefOfStaffResult(client, sessionID, true, previousSessionID, err)
			return
		}
	} else if _, err := d.store.ClearProfileChief(sessionID); err != nil {
		d.sendChiefOfStaffResult(client, sessionID, false, previousSessionID, err)
		return
	}

	var reloadErr error
	for _, reload := range prepared {
		if err := reload.execute(); err != nil && reloadErr == nil {
			reloadErr = err
		}
	}
	abortPrepared = false

	d.publishFact(FactSessionChiefRoleChanged, sessionID, nil)
	if roleChanged {
		newChiefSessionID := ""
		if msg.ChiefOfStaff {
			newChiefSessionID = sessionID
		}
		d.retargetChiefTicketDelivery(previousSessionID, newChiefSessionID)
		if !preparedSessions[sessionID] {
			go d.reloadSessionAgent(sessionID)
		}
		if msg.ChiefOfStaff && previousSessionID != "" && !preparedSessions[previousSessionID] {
			go d.reloadSessionAgent(previousSessionID)
		}
	}
	d.sendChiefOfStaffResult(client, sessionID, msg.ChiefOfStaff, previousSessionID, reloadErr)
}

func (d *Daemon) retargetChiefTicketDelivery(previousSessionID, newSessionID string) {
	if previousSessionID != "" {
		d.refreshTicketUnread(previousSessionID)
	}
	if newSessionID != "" {
		d.notifyUnreadTicketSession(newSessionID, time.Now())
	}
}

func (d *Daemon) sendChiefOfStaffResult(
	client *wsClient,
	sessionID string,
	chiefOfStaff bool,
	previousSessionID string,
	err error,
) {
	result := protocol.ChiefOfStaffResultMessage{
		Event:        protocol.EventChiefOfStaffResult,
		SessionID:    sessionID,
		ChiefOfStaff: chiefOfStaff,
		Success:      err == nil,
	}
	if previousSessionID != "" {
		result.PreviousSessionID = protocol.Ptr(previousSessionID)
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, result)
}
