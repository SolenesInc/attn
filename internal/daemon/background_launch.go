package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/profiles"
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
	if client != nil {
		changed := client.selectedProfile() != profile.ID
		client.selectProfile(profile.ID)
		d.sendArrangement(client, requestID, nil)
		if changed {
			d.sendGardenProfile(client)
		}
	}
	d.publishArrangementChanged(profile.ID)
	if client == nil {
		d.publishFact(FactSessionShowRequested, result.SessionID, protocol.SessionShowRequestedMessage{Event: protocol.EventSessionShowRequested, SessionID: result.SessionID})
	}
	return nil
}

func (d *Daemon) projectSessionShowRequested(ev bus.Event) {
	var message protocol.SessionShowRequestedMessage
	if err := ev.Decode(&message); err != nil {
		d.logf("session show projection: %v", err)
		return
	}
	d.wsHub.BroadcastValue(message)
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
	_, desktops, err := d.store.ProfileArrangement(placement.ProfileID)
	if err != nil {
		d.logf("background launch placement %s: %v", sessionID, err)
		return
	}
	label := ""
	for _, desktop := range desktops {
		if desktop.ID != placement.DesktopID {
			continue
		}
		label = profiles.DesktopLabel(desktop, desktops)
		if desktop.ShortcutSlot != 0 {
			label = fmt.Sprintf("%d · %s", desktop.ShortcutSlot, label)
		} else {
			label += " (no ⌘ number)"
		}
		break
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
