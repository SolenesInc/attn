package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/settingkeys"
)

func (d *Daemon) settingEntry(spec settingSpec, key, value string) protocol.SettingEntry {
	scope := protocol.SettingScopeDaemon
	if spec.scope == profileScope && d.enrollmentStatus().IsHome() {
		scope = protocol.SettingScopeProfile
	}
	entry := protocol.SettingEntry{Key: key, Scope: scope, Description: spec.description, Value: &value}
	if spec.readOnly || spec.class == settingkeys.Home && !d.enrollmentStatus().IsHome() {
		entry.ReadOnly = protocol.Ptr(true)
	}
	if strings.Contains(key, "<") {
		entry.Family = protocol.Ptr(true)
		entry.Value = nil
	}
	return entry
}

func (d *Daemon) settingValue(profileID string, spec settingSpec, key string, computed map[string]interface{}) string {
	if spec.readOnly {
		value, _ := computed[key].(string)
		return value
	}
	if spec.scope == profileScope && d.enrollmentStatus().IsHome() {
		return d.profileSetting(profileID, MachineSetting(key))
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
	profile := profiles.Profile{}
	if !d.enrollmentStatus().IsHome() && protocol.Deref(msg.ProfileID) != "" {
		id, synced, err := d.syncedNotebookProfile(protocol.Deref(msg.ProfileID))
		if err != nil {
			return nil, nil, err
		}
		profile = profiles.Profile{ID: id, Name: synced.Name}
	}
	needProfile := d.enrollmentStatus().IsHome() && (key == "" || protocol.Deref(msg.ProfileID) != "")
	if key != "" {
		spec, _ := lookupSetting(key)
		needProfile = needProfile || spec.scope == profileScope && d.enrollmentStatus().IsHome()
	}
	if needProfile {
		bindings, err := d.bindings()
		if err != nil {
			return nil, nil, err
		}
		request, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, bindings)
		if err != nil {
			if requested := protocol.Deref(msg.ProfileID); requested != "" {
				err = fmt.Errorf("settings --profile %q: %w", requested, err)
			}
			return nil, nil, err
		}
		profile, err = d.store.LiveProfile(request.ProfileID())
		if err != nil {
			return nil, nil, err
		}
	}
	values := d.settingsSnapshot(profile.ID)
	if key != "" {
		spec, _ := lookupSetting(key)
		value := d.settingValue(profile.ID, spec, key, values)
		entry := d.settingEntry(spec, key, value)
		return nil, &entry, nil
	}
	result := &protocol.SettingsListResult{ProfileID: profile.ID, ProfileName: profile.Name, Entries: []protocol.SettingEntry{}}
	for _, spec := range settingSpecs {
		if spec.readOnly && !protocol.Deref(msg.All) || spec.scope == profileScope && profile.ID == "" && d.enrollmentStatus().IsHome() {
			continue
		}
		value := d.settingValue(profile.ID, spec, spec.key, values)
		result.Entries = append(result.Entries, d.settingEntry(spec, spec.key, value))
		if !strings.Contains(spec.key, "<") {
			continue
		}
		for concrete := range values {
			matched, ok := lookupSetting(concrete)
			if ok && matched.key == spec.key && concrete != spec.key {
				value := d.settingValue(profile.ID, spec, concrete, values)
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

func (d *Daemon) profileSettingsOverlay(profileID string) map[string]interface{} {
	settings := make(map[string]interface{})
	state, reason := d.notebookSyncSettings(profileID)
	settings[string(settingNotebookSyncState)], settings[string(settingNotebookSyncLastError)] = state, reason
	if profileID == "" {
		return settings
	}
	for k, v := range d.store.ProfileSettings(profileID) {
		settings[k] = v
	}
	if profile, err := d.store.LiveProfile(profileID); err == nil {
		if root, err := d.defaultNotebookRoot(profile.Name, d.profileSetting(profile.ID, SettingNotebookRoot)); err == nil {
			settings[string(SettingNotebookRootDefault)] = root
		}
	}
	if root, err := d.notebookRoot(profileID); err == nil {
		settings[string(SettingNotebookRootEffective)] = root
	}
	return settings
}

func (d *Daemon) settingsSnapshot(profileID string) map[string]interface{} {
	settings := d.daemonSettingsSnapshot()
	for k, v := range d.profileSettingsOverlay(profileID) {
		settings[k] = v
	}
	return settings
}
