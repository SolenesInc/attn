package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) launchRequester(sessionID protocol.SessionID, fallback string) string {
	if session := d.store.Get(sessionID); session != nil && session.Label != "" {
		return session.Label
	}
	return fallback
}

func (d *Daemon) showCrewStart(result *protocol.CrewStartResult, client *wsClient, requestID string) error {
	profile, _, _, err := d.store.ShowSession(result.SessionID)
	if err != nil {
		return err
	}
	changed := client.selectedProfile() != profile.ID
	client.selectProfile(profile.ID)
	d.sendArrangement(client, requestID, nil)
	if changed {
		d.sendProfileSnapshots(client)
	}
	d.publishArrangementChanged(profile.ID)
	return nil
}

type backgroundLaunch struct {
	DesktopID    string                     `json:"desktop_id"`
	DesktopLabel string                     `json:"desktop_label"`
	Event        string                     `json:"event"`
	Kind         protocol.LaunchDesktopKind `json:"kind"`
	Name         string                     `json:"name"`
	ProfileID    string                     `json:"profile_id"`
	RequestedBy  string                     `json:"requested_by"`
	SessionID    protocol.SessionID         `json:"session_id"`
}

func (d *Daemon) announceBackgroundLaunch(kind string, itemID string, sessionID protocol.SessionID, requestedBy string) {
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil || !placed {
		return
	}
	item, err := d.store.LaunchDesktopItem(kind, itemID)
	if err != nil {
		d.logf("background launch %s: %v", sessionID, err)
		return
	}
	label, err := d.store.LaunchDesktopLabel(placement.DesktopID)
	if err != nil {
		d.logf("background launch placement %s: %v", sessionID, err)
		return
	}
	d.publishFact(FactBackgroundLaunch, string(sessionID), backgroundLaunch{Event: "background_launch", SessionID: sessionID, ProfileID: placement.ProfileID, DesktopID: placement.DesktopID, Name: item.Name, RequestedBy: requestedBy, DesktopLabel: label, Kind: protocol.LaunchDesktopKind(kind)})
}
