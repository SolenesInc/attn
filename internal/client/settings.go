package client

import (
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

type SettingsCaller struct {
	SessionID protocol.SessionID
	Profile   string
}

func (c *Client) Settings(caller SettingsCaller, key string, all bool) (*protocol.SettingsListResult, error) {
	scoped := c.WithGardenProfile(caller.Profile, caller.SessionID)
	if key != "" {
		var entry protocol.SettingEntry
		if err := scoped.RunCommand("settings get", "", command.SettingsGetArgs{Key: key}, &entry); err != nil {
			return nil, err
		}
		return &protocol.SettingsListResult{Entries: []protocol.SettingEntry{entry}}, nil
	}
	var list protocol.SettingsListResult
	if err := scoped.RunCommand("settings list", "", command.SettingsListArgs{All: all}, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

func (c *Client) SetSetting(caller SettingsCaller, key, value string) (*protocol.SettingEntry, error) {
	var entry protocol.SettingEntry
	err := c.WithGardenProfile(caller.Profile, caller.SessionID).RunCommand("settings set", "", command.SettingsSetArgs{Key: key, Value: &value}, &entry)
	if err != nil {
		return nil, err
	}
	return &entry, nil
}
