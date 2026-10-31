package client

import (
	"github.com/victorarias/attn/internal/protocol"
)

type SettingsCaller struct {
	SessionID protocol.SessionID
	Profile   string
}

func (caller SettingsCaller) fields() (*protocol.SessionID, *string) {
	var sessionID *protocol.SessionID
	var profile *string
	if caller.SessionID != "" {
		sessionID = protocol.Ptr(caller.SessionID)
	}
	if caller.Profile != "" {
		profile = protocol.Ptr(caller.Profile)
	}
	return sessionID, profile
}

func (c *Client) Settings(caller SettingsCaller, key string, all bool) (*protocol.SettingsListResult, error) {
	sessionID, profile := caller.fields()
	resp, err := c.send(protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings, SourceSessionID: sessionID, ProfileID: profile, Key: protocol.Ptr(key), All: protocol.Ptr(all)})
	if err != nil {
		return nil, err
	}
	if key != "" && resp.Setting != nil {
		return &protocol.SettingsListResult{Entries: []protocol.SettingEntry{*resp.Setting}}, nil
	}
	return resp.SettingsList, nil
}

func (c *Client) DropOutpostCopy() error {
	_, err := c.send(protocol.DropOutpostCopyMessage{Cmd: protocol.CmdDropOutpostCopy})
	return err
}

func (c *Client) SetSetting(caller SettingsCaller, key, value string) (*protocol.SettingEntry, error) {
	sessionID, profile := caller.fields()
	resp, err := c.send(protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, SourceSessionID: sessionID, ProfileID: profile, Key: key, Value: value})
	if err != nil {
		return nil, err
	}
	return resp.Setting, nil
}
