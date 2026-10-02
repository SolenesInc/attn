package daemon

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type launchDesktopWrite struct {
	ref     *string
	name    *string
	setting *protocol.LaunchDesktopSetting
}

func protocolLaunchItem(item store.LaunchDesktopItem) protocol.LaunchDesktopItem {
	setting := protocol.LaunchDesktopSetting{Label: protocol.Ptr(item.Label)}
	if item.DesktopID != "" {
		setting.DesktopID = protocol.Ptr(item.DesktopID)
	}
	return protocol.LaunchDesktopItem{Kind: protocol.LaunchDesktopKind(item.Kind), ItemID: item.ID, Name: item.Name, ProfileID: item.ProfileID, Setting: setting, Confirmed: item.Confirmed}
}

func (d *Daemon) launchDesktopResult(action, requestID, kind, id string, setting *protocol.LaunchDesktopSetting, ref, name *string) protocol.LaunchDesktopResultMessage {
	write := setting != nil || ref != nil
	if kind == "automation" && write {
		d.automationMu.Lock()
		defer d.automationMu.Unlock()
	}
	result := protocol.LaunchDesktopResultMessage{Event: protocol.EventLaunchDesktopResult, Action: action, RequestID: requestID}
	err := d.requireHome("launch desktops")
	if err == nil && id == "" && !write {
		profiles, readErr := d.store.ListProfiles(false)
		if readErr != nil {
			result.Error = protocol.Ptr(readErr.Error())
			return result
		}
		for _, profile := range profiles {
			items, readErr := d.store.LaunchDesktopItems(profile.ID)
			if readErr != nil {
				result.Error = protocol.Ptr(readErr.Error())
				return result
			}
			for _, item := range items {
				result.Items = append(result.Items, protocolLaunchItem(item))
			}
			_, desktops, readErr := d.store.ProfileArrangement(profile.ID)
			if readErr != nil {
				result.Error = protocol.Ptr(readErr.Error())
				return result
			}
			for _, desktop := range desktops {
				wire, encodeErr := protocolDesktop(desktop)
				if encodeErr != nil {
					result.Error = protocol.Ptr(encodeErr.Error())
					return result
				}
				result.Desktops = append(result.Desktops, wire)
			}
		}
		result.Success = true
		return result
	}
	chosen := storeLaunchSetting(setting)
	if err == nil && ref != nil {
		var item store.LaunchDesktopItem
		item, err = d.store.LaunchDesktopItem(kind, id)
		if err == nil {
			var fromRef store.LaunchDesktopSetting
			fromRef, err = d.launchDesktopFromRef(item.ProfileID, item.Name, *ref, name)
			chosen = &fromRef
		}
	}
	if err == nil && chosen != nil {
		err = d.store.SetLaunchDesktop(kind, id, *chosen)
	}
	if err == nil {
		item, desktops, readErr := d.store.LaunchDesktopChoices(kind, id)
		err = readErr
		if err == nil {
			items, readErr := d.store.LaunchDesktopItems(item.ProfileID)
			if readErr != nil {
				result.Error = protocol.Ptr(readErr.Error())
				return result
			}
			for _, candidate := range items {
				result.Items = append(result.Items, protocolLaunchItem(candidate))
			}
			wire := protocolLaunchItem(item)
			result.Item = &wire
			result.Desktops = []protocol.Desktop{}
			for _, desktop := range desktops {
				wire, encodeErr := protocolDesktop(desktop)
				if encodeErr != nil {
					err = encodeErr
					break
				}
				result.Desktops = append(result.Desktops, wire)
			}
		}
	}
	result.Success = err == nil
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	if err == nil && chosen != nil {
		d.publishArrangementChanged(result.Item.ProfileID)
		d.publishMigrationChanged(result.Item.ProfileID)
		if kind == "crew" {
			d.publishFact(FactCrewUpdated, id, nil)
		} else {
			d.broadcastAutomationsChanged(id)
		}
	}
	return result
}

func (d *Daemon) handleLaunchDesktopGet(client *wsClient, msg *protocol.LaunchDesktopGetMessage) {
	d.sendToClient(client, d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, nil, nil, nil))
}
func (d *Daemon) handleLaunchDesktopSet(client *wsClient, msg *protocol.LaunchDesktopSetMessage) {
	d.sendToClient(client, d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, msg.Setting, msg.DesktopRef, msg.DesktopName))
}

// storeLaunchSetting reads a setting sent back without a desktop, a review suggestion, as no choice.
func storeLaunchSetting(setting *protocol.LaunchDesktopSetting) *store.LaunchDesktopSetting {
	if setting == nil || (protocol.Deref(setting.DesktopID) == "" && protocol.Deref(setting.DesktopName) == "") {
		return nil
	}
	return &store.LaunchDesktopSetting{DesktopID: protocol.Deref(setting.DesktopID), DesktopName: protocol.Deref(setting.DesktopName)}
}

// launchDesktopFromRef reads the CLI's --launch-desktop: "own" or an empty slot 5–9 is a new desktop
// named --desktop-name, else the item's name; anything else names an existing desktop.
func (d *Daemon) launchDesktopFromRef(profileID, itemName, ref string, name *string) (store.LaunchDesktopSetting, error) {
	newName := itemName
	if name != nil {
		newName = *name
	}
	if ref == "own" {
		return store.LaunchDesktopSetting{DesktopName: newName}, nil
	}
	profile, err := d.liveLaunchProfile(profileID)
	if err != nil {
		return store.LaunchDesktopSetting{}, err
	}
	desktop, err := d.resolveDesktopRef(profile, ref)
	if err == nil && name != nil {
		return store.LaunchDesktopSetting{}, fmt.Errorf("--desktop-name needs --launch-desktop own or an empty slot 5–9")
	}
	if err == nil {
		return store.LaunchDesktopSetting{DesktopID: desktop.ID}, nil
	}
	if len(ref) == 1 && ref[0] >= '5' && ref[0] <= '9' {
		return store.LaunchDesktopSetting{DesktopID: profiles.NumberedDesktopID(profileID, int(ref[0]-'0')), DesktopName: newName}, nil
	}
	return store.LaunchDesktopSetting{}, err
}

func (d *Daemon) handleLaunchDesktopCommand(conn net.Conn, cmd string, message any) {
	var result protocol.LaunchDesktopResultMessage
	switch cmd {
	case protocol.CmdLaunchDesktopGet:
		msg := message.(*protocol.LaunchDesktopGetMessage)
		result = d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, nil, nil, nil)
	case protocol.CmdLaunchDesktopSet:
		msg := message.(*protocol.LaunchDesktopSetMessage)
		result = d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, msg.Setting, msg.DesktopRef, msg.DesktopName)
	}
	_ = json.NewEncoder(conn).Encode(result)
}
