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
	setting := protocol.LaunchDesktopSetting{Label: protocol.Ptr(item.Setting.Label), Mode: protocol.LaunchDesktopMode(item.Setting.Mode), DestinationID: protocol.Ptr(item.Setting.DestinationID), DesktopName: protocol.Ptr(item.Setting.DesktopName), Pending: protocol.Ptr(item.Setting.Pending), OwnerKind: protocol.Ptr(protocol.LaunchDesktopKind(item.Setting.OwnerKind)), OwnerID: protocol.Ptr(item.Setting.OwnerID)}
	if item.Setting.DesktopID != "" {
		setting.DesktopID = protocol.Ptr(item.Setting.DesktopID)
	}
	return protocol.LaunchDesktopItem{Kind: protocol.LaunchDesktopKind(item.Kind), ItemID: item.ID, Name: item.Name, ProfileID: item.ProfileID, Setting: setting, Confirmed: item.Confirmed}
}

func (d *Daemon) launchDesktopResult(action, requestID, kind, id string, setting *protocol.LaunchDesktopSetting, ref *string) protocol.LaunchDesktopResultMessage {
	if kind == "automation" && setting != nil {
		d.automationMu.Lock()
		defer d.automationMu.Unlock()
	}
	result := protocol.LaunchDesktopResultMessage{Event: protocol.EventLaunchDesktopResult, Action: action, RequestID: requestID}
	err := d.requireHome("launch desktops")
	if err == nil && id == "" && setting == nil {
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
	if err == nil && ref != nil {
		var item store.LaunchDesktopItem
		item, err = d.store.LaunchDesktopItem(kind, id)
		if err == nil {
			var chosen store.LaunchDesktopSetting
			chosen, err = d.namedLaunchDesktopFromRef(item.ProfileID, kind, *ref, setting.DesktopName)
			setting = &protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopMode(chosen.Mode), DesktopID: protocol.Ptr(chosen.DesktopID), DesktopName: setting.DesktopName}
		}
	}
	if err == nil && setting != nil {
		_, err = d.store.SetLaunchDesktop(kind, id, storeLaunchSetting(*setting))
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
	if err == nil && setting != nil {
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
	d.sendToClient(client, d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, nil, nil))
}
func (d *Daemon) handleLaunchDesktopSet(client *wsClient, msg *protocol.LaunchDesktopSetMessage) {
	if msg.DesktopName != nil {
		msg.Setting.DesktopName = msg.DesktopName
	}
	d.sendToClient(client, d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, &msg.Setting, msg.DesktopRef))
}

func storeLaunchSetting(setting protocol.LaunchDesktopSetting) store.LaunchDesktopSetting {
	return store.LaunchDesktopSetting{Mode: string(setting.Mode), DesktopID: protocol.Deref(setting.DesktopID), DestinationID: protocol.Deref(setting.DestinationID), DesktopName: protocol.Deref(setting.DesktopName)}
}

func (d *Daemon) launchDesktopFromRef(profileID, kind, ref string) (store.LaunchDesktopSetting, error) {
	if ref == "own" {
		return store.LaunchDesktopSetting{Mode: "own"}, nil
	}
	profile, err := d.liveLaunchProfile(profileID)
	if err != nil {
		return store.LaunchDesktopSetting{}, err
	}
	desktop, err := d.resolveDesktopRef(profile, ref)
	if err == nil {
		return store.LaunchDesktopSetting{Mode: "desktop", DesktopID: desktop.ID}, nil
	}
	if len(ref) == 1 && ref[0] >= '5' && ref[0] <= '9' {
		return store.LaunchDesktopSetting{Mode: "own", DesktopID: profiles.NumberedDesktopID(profileID, int(ref[0]-'0'))}, nil
	}
	return store.LaunchDesktopSetting{}, err
}

func (d *Daemon) handleLaunchDesktopCommand(conn net.Conn, cmd string, message any) {
	var result protocol.LaunchDesktopResultMessage
	switch cmd {
	case protocol.CmdLaunchDesktopGet:
		msg := message.(*protocol.LaunchDesktopGetMessage)
		result = d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, nil, nil)
	case protocol.CmdLaunchDesktopSet:
		msg := message.(*protocol.LaunchDesktopSetMessage)
		if msg.DesktopName != nil {
			msg.Setting.DesktopName = msg.DesktopName
		}
		result = d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, &msg.Setting, msg.DesktopRef)
	}
	_ = json.NewEncoder(conn).Encode(result)
}

func (d *Daemon) namedLaunchDesktopFromRef(profileID, kind, ref string, name *string) (store.LaunchDesktopSetting, error) {
	setting, err := d.launchDesktopFromRef(profileID, kind, ref)
	if err != nil {
		return setting, err
	}
	if name != nil {
		if setting.Mode != "own" {
			return setting, fmt.Errorf("--desktop-name needs --launch-desktop own or an empty slot 5–9")
		}
		setting.DesktopName = *name
	}
	return setting, nil
}
