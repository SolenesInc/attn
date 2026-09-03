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

const profileRoleChiefOfStaff = "chief_of_staff"

func (d *Daemon) chiefOfStaffSessionID() string {
	if d.store == nil {
		return ""
	}
	return strings.TrimSpace(d.store.GetProfileRole(profileRoleChiefOfStaff))
}

func (d *Daemon) isChiefOfStaffSession(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	return d.chiefOfStaffSessionID() == sessionID
}

func (d *Daemon) decorateChiefOfStaffWithSessionID(session *protocol.Session, chiefOfStaffSessionID string) {
	if session == nil {
		return
	}
	if session.ID == chiefOfStaffSessionID {
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

// Set only when true and cleared otherwise, so it round-trips as an omitted
// boolean.
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
	return d.hubManager != nil && d.hubManager.RemoteSession(sessionID) != nil
}

func (d *Daemon) clearChiefOfStaffIfSession(sessionID string) {
	if d.store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if err := d.store.ClearProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
		d.logf("clear chief of staff role failed for session %s: %v", sessionID, err)
	}
}

func (d *Daemon) nudgeChiefOfStaff(attemptKey, prompt string) bool {
	if d.store == nil {
		return false
	}
	sessionID := d.chiefOfStaffSessionID()
	if sessionID == "" {
		return false
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return false
	}
	if d.chiefOfStaffSessionID() != sessionID {
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

// The role must be set BEFORE ptyBackend.Spawn: the agent's notebook-guide query can fire
// before the session row exists, and it is what pulls the chief guidance in.
func (d *Daemon) maybeAssignChiefOnSpawn(sessionID, agent string, requested bool, existingSession *protocol.Session) bool {
	if !requested || existingSession != nil || d.store == nil {
		return false
	}
	if !d.agentSupportsChiefGuidance(agent) {
		d.logf("create-as-chief: agent %q for session %s has no chief-guidance launch path; ignoring", agent, sessionID)
		return false
	}
	if current := d.chiefOfStaffSessionID(); current != "" {
		d.logf("create-as-chief: a chief (%s) already exists; ignoring request for session %s", current, sessionID)
		return false
	}
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
		d.logf("create-as-chief: set chief role failed for session %s: %v", sessionID, err)
		return false
	}
	d.logf("create-as-chief: session %s assigned chief role at launch", sessionID)
	return true
}

func (d *Daemon) handleSetChiefOfStaff(client *wsClient, msg *protocol.SetChiefOfStaffMessage) {
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		d.sendChiefOfStaffResult(client, sessionID, msg.ChiefOfStaff, "", fmt.Errorf("missing session_id"))
		return
	}

	previousSessionID := d.chiefOfStaffSessionID()
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
		if err := d.store.SetProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
			d.sendChiefOfStaffResult(client, sessionID, true, previousSessionID, err)
			return
		}
	} else if err := d.store.ClearProfileRole(profileRoleChiefOfStaff, sessionID); err != nil {
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
	// ChiefGuidance is injected only at agent-launch, so a live promotion re-runs it. The
	// reload is destructive, so fire it ONLY on a real role change.
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
