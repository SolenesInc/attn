package daemon

import (
	"encoding/json"
	"net"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func protocolLaunchItem(item store.LaunchDesktopItem) protocol.LaunchDesktopItem {
	setting := protocol.LaunchDesktopSetting{Label: protocol.Ptr(item.Setting.Label), Mode: protocol.LaunchDesktopMode(item.Setting.Mode)}
	if item.Setting.DesktopID != "" {
		setting.DesktopID = protocol.Ptr(item.Setting.DesktopID)
	}
	if item.Setting.Fallback {
		setting.Fallback = protocol.Ptr(true)
	}
	return protocol.LaunchDesktopItem{Kind: protocol.LaunchDesktopKind(item.Kind), ItemID: item.ID, Name: item.Name, ProfileID: item.ProfileID, Setting: setting, Confirmed: item.Confirmed}
}

func (d *Daemon) launchDesktopResult(action, requestID, kind, id string, setting *protocol.LaunchDesktopSetting, ref *string) protocol.LaunchDesktopResultMessage {
	result := protocol.LaunchDesktopResultMessage{Event: protocol.EventLaunchDesktopResult, Action: action, RequestID: requestID}
	err := d.requireHome("launch desktops")
	if err == nil && ref != nil {
		var item store.LaunchDesktopItem
		item, err = d.store.LaunchDesktopItem(kind, id)
		if err == nil {
			var chosen store.LaunchDesktopSetting
			chosen, err = d.launchDesktopFromRef(item.ProfileID, kind, *ref)
			setting = &protocol.LaunchDesktopSetting{Mode: protocol.LaunchDesktopMode(chosen.Mode), DesktopID: protocol.Ptr(chosen.DesktopID)}
		}
	}
	if err == nil && setting != nil {
		_, err = d.store.SetLaunchDesktop(kind, id, store.LaunchDesktopSetting{Mode: string(setting.Mode), DesktopID: protocol.Deref(setting.DesktopID)})
	}
	if err == nil {
		item, desktops, readErr := d.store.LaunchDesktopChoices(kind, id)
		err = readErr
		if err == nil {
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
	d.sendToClient(client, d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, &msg.Setting, msg.DesktopRef))
}

func (d *Daemon) launchDesktopFromRef(profileID, kind, ref string) (store.LaunchDesktopSetting, error) {
	if ref == "current" {
		return store.LaunchDesktopSetting{Mode: "current"}, nil
	}
	if ref == "dedicated" && kind == "automation" {
		return store.LaunchDesktopSetting{Mode: "dedicated"}, nil
	}
	profile, err := d.liveLaunchProfile(profileID)
	if err != nil {
		return store.LaunchDesktopSetting{}, err
	}
	desktop, err := d.resolveDesktopRef(profile, ref)
	if err != nil {
		return store.LaunchDesktopSetting{}, err
	}
	return store.LaunchDesktopSetting{Mode: "desktop", DesktopID: desktop.ID}, nil
}

func (d *Daemon) handleLaunchDesktopCommand(conn net.Conn, cmd string, message any) {
	var result protocol.LaunchDesktopResultMessage
	switch cmd {
	case protocol.CmdLaunchDesktopGet:
		msg := message.(*protocol.LaunchDesktopGetMessage)
		result = d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, nil, nil)
	case protocol.CmdLaunchDesktopSet:
		msg := message.(*protocol.LaunchDesktopSetMessage)
		result = d.launchDesktopResult(msg.Cmd, msg.RequestID, string(msg.Kind), msg.ItemID, &msg.Setting, msg.DesktopRef)
	}
	_ = json.NewEncoder(conn).Encode(result)
}
