package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/settingkeys"
)

func (d *Daemon) settingEntry(spec settingSpec, key, value string) protocol.SettingEntry {
	entry := protocol.SettingEntry{Key: key, Scope: protocol.SettingScopeDaemon, Description: spec.description, Value: &value}
	if spec.readOnly || spec.class == settingkeys.Home && !d.enrollmentStatus().IsHome() {
		entry.ReadOnly = protocol.Ptr(true)
	}
	if strings.Contains(key, "<") {
		entry.Family = protocol.Ptr(true)
		entry.Value = nil
	}
	return entry
}

func (d *Daemon) settingValue(spec settingSpec, key string, computed map[string]interface{}) string {
	if spec.readOnly {
		value, _ := computed[key].(string)
		return value
	}
	if spec.class == settingkeys.Machine {
		return d.machineSetting(MachineSetting(key))
	}
	return d.homeSetting(HomeSetting(key))
}

func (d *Daemon) settingsList(msg *protocol.GetSettingsMessage) (*protocol.SettingsListResult, *protocol.SettingEntry, error) {
	key := protocol.Deref(msg.Key)
	if key != "" {
		_, ok := lookupSetting(key)
		if !ok || strings.Contains(key, "<") {
			return nil, nil, fmt.Errorf("unknown setting: %s", key)
		}
	}
	values := d.daemonSettingsSnapshot()
	if key != "" {
		spec, _ := lookupSetting(key)
		value := d.settingValue(spec, key, values)
		entry := d.settingEntry(spec, key, value)
		return nil, &entry, nil
	}
	result := &protocol.SettingsListResult{Entries: []protocol.SettingEntry{}}
	for _, spec := range settingSpecs {
		if spec.readOnly && !protocol.Deref(msg.All) {
			continue
		}
		value := d.settingValue(spec, spec.key, values)
		result.Entries = append(result.Entries, d.settingEntry(spec, spec.key, value))
		if !strings.Contains(spec.key, "<") {
			continue
		}
		for concrete := range values {
			matched, ok := lookupSetting(concrete)
			if ok && matched.key == spec.key && concrete != spec.key {
				value := d.settingValue(spec, concrete, values)
				result.Entries = append(result.Entries, d.settingEntry(spec, concrete, value))
			}
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Key < result.Entries[j].Key })
	return result, nil, nil
}

func (d *Daemon) handleGetSettings(conn net.Conn, msg *protocol.GetSettingsMessage) {
	d.refreshTailscaleServeState()
	list, entry, err := d.settingsList(msg)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, SettingsList: list, Setting: entry})
}

func (d *Daemon) handleSetSetting(conn net.Conn, msg *protocol.SetSettingMessage) {
	entry, err := d.setSetting(msg)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, Setting: entry})
}
