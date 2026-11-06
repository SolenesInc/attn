package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

func (d *Daemon) settingEntry(spec *setting.Setting, key, value string) protocol.SettingEntry {
	entry := protocol.SettingEntry{Key: key, Scope: protocol.SettingScopeDaemon, Description: spec.Description, Value: &value}
	if spec.Scope == setting.PerProfile {
		entry.Scope = protocol.SettingScopeProfile
	}
	if spec.ReadOnly() {
		entry.ReadOnly = protocol.Ptr(true)
	} else if spec.Class == setting.Home && !d.enrollmentStatus().IsHome() {
		entry.SetAtHome = protocol.Ptr(true)
	}
	if strings.Contains(key, "<") {
		entry.Family = protocol.Ptr(true)
		entry.Value = nil
	}
	return entry
}

func (d *Daemon) settingValue(spec *setting.Setting, key string, computed map[string]interface{}, profileID string) string {
	if spec.ReadOnly() {
		value, _ := computed[key].(string)
		return value
	}
	if spec.Scope == setting.PerProfile {
		return d.store.ProfileSetting(profileID, key)
	}
	concrete := *spec
	concrete.Key = key
	return d.setting(&concrete)
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
	var spec *setting.Setting
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
	if key != "" && spec.Scope == setting.PerProfile && profileErr != nil {
		return nil, nil, profileErr
	}
	savedAtHome := func(spec *setting.Setting) bool {
		return spec.Scope == setting.PerProfile && !spec.ReadOnly() && d.enrollmentStatus().IsOutpost()
	}
	if key != "" && savedAtHome(spec) {
		return nil, nil, fmt.Errorf("%s is saved at the home for each profile; read it there with attn settings get %s, or read %s for this machine's copy", key, key, setting.NotebookRootEffective)
	}
	notebookProfile := profile.ID
	if d.enrollmentStatus().IsOutpost() && msg.SourceSessionID != nil {
		notebookProfile, _ = d.sessionNotebookProfile(*msg.SourceSessionID)
	}
	values := d.settingsSnapshot(notebookProfile)
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
	for _, spec := range setting.All() {
		if spec.ReadOnly() && !protocol.Deref(msg.All) || spec.Scope == setting.PerProfile && profileErr != nil || savedAtHome(spec) {
			continue
		}
		value := d.settingValue(spec, spec.Key, values, profile.ID)
		result.Entries = append(result.Entries, d.settingEntry(spec, spec.Key, value))
		if !strings.Contains(spec.Key, "<") {
			continue
		}
		for concrete := range values {
			matched, ok := lookupSetting(concrete)
			if ok && matched.Key == spec.Key && concrete != spec.Key {
				value := d.settingValue(spec, concrete, values, profile.ID)
				result.Entries = append(result.Entries, d.settingEntry(spec, concrete, value))
			}
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Key < result.Entries[j].Key })
	return result, nil, nil
}

func (d *Daemon) handleGetSettings(conn net.Conn, msg *protocol.GetSettingsMessage) {
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
