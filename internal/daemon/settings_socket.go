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
	entry := protocol.SettingEntry{Key: key, Scope: protocol.SettingScopeDaemon, Description: spec.description, Value: &value}
	if spec.scope == profileScope {
		entry.Scope = protocol.SettingScopeProfile
	}
	if spec.readOnly || spec.class == settingkeys.Home && !d.enrollmentStatus().IsHome() {
		entry.ReadOnly = protocol.Ptr(true)
	}
	if strings.Contains(key, "<") {
		entry.Family = protocol.Ptr(true)
		entry.Value = nil
	}
	return entry
}

func (d *Daemon) settingValue(spec settingSpec, key string, computed map[string]interface{}, profileID string) string {
	if spec.readOnly {
		value, _ := computed[key].(string)
		return value
	}
	if spec.scope == profileScope {
		return d.store.ProfileSetting(profileID, key)
	}
	if spec.class == settingkeys.Machine {
		return d.machineSetting(MachineSetting(key))
	}
	return d.homeSetting(HomeSetting(key))
}

func (d *Daemon) settingsProfile(sessionID *protocol.SessionID, requested *string) (profiles.Profile, error) {
	profile, err := d.resolveGardenProfile(protocol.Deref(sessionID), protocol.Deref(requested), "")
	if err != nil && strings.TrimSpace(protocol.Deref(requested)) != "" {
		return profiles.Profile{}, fmt.Errorf("settings --profile %q: %w", protocol.Deref(requested), err)
	}
	return profile, err
}

func (d *Daemon) settingsList(msg *protocol.GetSettingsMessage) (*protocol.SettingsListResult, *protocol.SettingEntry, error) {
	key := protocol.Deref(msg.Key)
	var spec settingSpec
	if key != "" {
		var ok bool
		spec, ok = lookupSetting(key)
		if !ok || strings.Contains(key, "<") {
			return nil, nil, fmt.Errorf("unknown setting: %s", key)
		}
	}
	var profile profiles.Profile
	var profileErr error
	if d.enrollmentStatus().IsHome() {
		profile, profileErr = d.settingsProfile(msg.SourceSessionID, msg.ProfileID)
	}
	if key != "" && spec.scope == profileScope && profileErr != nil {
		return nil, nil, profileErr
	}
	values := d.settingsSnapshot(profile.ID)
	if key != "" {
		entry := d.settingEntry(spec, key, d.settingValue(spec, key, values, profile.ID))
		return nil, &entry, nil
	}
	result := &protocol.SettingsListResult{Entries: []protocol.SettingEntry{}}
	if profileErr != nil {
		result.ProfileUnresolved = protocol.Ptr(profileErr.Error())
	} else if profile.ID != "" {
		result.Profile = protocol.Ptr(profile.Name)
	}
	for _, spec := range settingSpecs {
		if spec.readOnly && !protocol.Deref(msg.All) || spec.scope == profileScope && profileErr != nil {
			continue
		}
		value := d.settingValue(spec, spec.key, values, profile.ID)
		result.Entries = append(result.Entries, d.settingEntry(spec, spec.key, value))
		if !strings.Contains(spec.key, "<") {
			continue
		}
		for concrete := range values {
			matched, ok := lookupSetting(concrete)
			if ok && matched.key == spec.key && concrete != spec.key {
				value := d.settingValue(spec, concrete, values, profile.ID)
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
