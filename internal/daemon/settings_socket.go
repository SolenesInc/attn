package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) settingsProfile(session protocol.SessionID, requested string) (profiles.Profile, error) {
	if protocol.TrimID(session) != "" {
		profile, err := d.callerProfile(session)
		if err != nil {
			return profiles.Profile{}, err
		}
		if requested != "" {
			target, err := d.liveProfileNamed(requested)
			if err != nil {
				return profiles.Profile{}, err
			}
			if target.ID != profile.ID {
				return profiles.Profile{}, fmt.Errorf("this session belongs to profile %q; --profile only chooses a profile outside an attn session", profile.Name)
			}
		}
		return profile, nil
	}
	if requested != "" {
		return d.liveProfileNamed(requested)
	}
	return d.resolveGardenProfile("", "", "")
}

func settingEntry(spec settingSpec, key, value string) protocol.SettingEntry {
	scope := protocol.SettingScopeDaemon
	if spec.scope == profileScope {
		scope = protocol.SettingScopeProfile
	}
	entry := protocol.SettingEntry{Key: key, Scope: scope, Description: spec.description, Value: &value}
	if spec.readOnly {
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
	if spec.scope == profileScope {
		return d.profileSetting(profileID, profileSettingKey(key))
	}
	return d.daemonSetting(daemonSettingKey(key))
}

func (d *Daemon) settingsList(msg *protocol.GetSettingsMessage) (*protocol.SettingsListResult, *protocol.SettingEntry, error) {
	key := protocol.Deref(msg.Key)
	if key != "" {
		spec, ok := lookupSetting(key)
		if !ok || strings.Contains(key, "<") {
			return nil, nil, fmt.Errorf("unknown setting: %s", key)
		}
		if spec.scope == profileScope {
			if err := d.requireHome("profile settings"); err != nil {
				return nil, nil, err
			}
		}
	}
	profile := profiles.Profile{}
	needProfile := key == "" || protocol.Deref(msg.ProfileID) != ""
	if key != "" {
		spec, _ := lookupSetting(key)
		needProfile = needProfile || spec.scope == profileScope
	}
	if needProfile {
		if err := d.requireHome("profile settings"); err != nil {
			return nil, nil, err
		}
		var err error
		profile, err = d.settingsProfile(protocol.Deref(msg.SourceSessionID), protocol.Deref(msg.ProfileID))
		if err != nil {
			return nil, nil, err
		}
	}
	values := d.settingsSnapshot(profile.ID)
	if key != "" {
		spec, _ := lookupSetting(key)
		value := d.settingValue(profile.ID, spec, key, values)
		entry := settingEntry(spec, key, value)
		return nil, &entry, nil
	}
	result := &protocol.SettingsListResult{ProfileID: profile.ID, ProfileName: profile.Name, Entries: []protocol.SettingEntry{}}
	for _, spec := range settingSpecs {
		if spec.readOnly && !protocol.Deref(msg.All) || spec.scope == profileScope && profile.ID == "" {
			continue
		}
		value := d.settingValue(profile.ID, spec, spec.key, values)
		result.Entries = append(result.Entries, settingEntry(spec, spec.key, value))
		if !strings.Contains(spec.key, "<") {
			continue
		}
		for concrete := range values {
			matched, ok := lookupSetting(concrete)
			if ok && matched.key == spec.key && concrete != spec.key {
				value := d.settingValue(profile.ID, spec, concrete, values)
				result.Entries = append(result.Entries, settingEntry(spec, concrete, value))
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
	if d.requireHome("profile settings") != nil || profileID == "" {
		return settings
	}
	for k, v := range d.store.ProfileSettings(profileID) {
		settings[k] = v
	}
	if profile, err := d.store.LiveProfile(profileID); err == nil {
		if root, err := d.defaultNotebookRoot(profile.Name, d.profileSetting(profile.ID, settingNotebookRoot)); err == nil {
			settings[string(settingNotebookRootDefault)] = root
		}
	}
	if root, err := d.notebookRoot(profileID); err == nil {
		settings[string(settingNotebookRootEffective)] = root
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
