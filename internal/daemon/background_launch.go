package daemon

import (
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) launchRequester(sessionID, fallback string) string {
	if session := d.store.Get(sessionID); session != nil && session.Label != "" {
		return session.Label
	}
	return fallback
}

func (d *Daemon) showCrewWake(result *protocol.CrewWakeResult, client *wsClient, requestID string) error {
	profile, _, _, err := d.store.ShowSession(result.SessionID)
	if err != nil {
		return err
	}
	changed := client.selectedProfile() != profile.ID
	client.selectProfile(profile.ID)
	d.sendArrangement(client, requestID, nil)
	if changed {
		d.sendGardenProfile(client)
	}
	d.publishArrangementChanged(profile.ID)
	return nil
}

func (d *Daemon) announceBackgroundLaunch(kind, itemID, sessionID, requestedBy string) {
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
	d.publishFact(FactBackgroundLaunch, sessionID, protocol.BackgroundLaunchMessage{Event: protocol.EventBackgroundLaunch, SessionID: sessionID, ProfileID: placement.ProfileID, DesktopID: placement.DesktopID, Name: item.Name, RequestedBy: requestedBy, DesktopLabel: label, Kind: protocol.LaunchDesktopKind(kind)})
}

func (d *Daemon) projectBackgroundLaunch(ev bus.Event) {
	var message protocol.BackgroundLaunchMessage
	if err := ev.Decode(&message); err != nil {
		d.logf("background launch projection: %v", err)
		return
	}
	d.wsHub.BroadcastValue(message)
}
