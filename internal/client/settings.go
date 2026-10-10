package client

import (
	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) Settings(session protocol.SessionID, profile, key string, all bool) (*protocol.SettingsListResult, error) {
	resp, err := c.send(protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings, SourceSessionID: protocol.Ptr(session), ProfileID: protocol.Ptr(profile), Key: protocol.Ptr(key), All: protocol.Ptr(all)})
	if err != nil {
		return nil, err
	}
	if key != "" && resp.Setting != nil {
		return &protocol.SettingsListResult{Entries: []protocol.SettingEntry{*resp.Setting}}, nil
	}
	return resp.SettingsList, nil
}

func (c *Client) SetSetting(session protocol.SessionID, profile, key, value string) (*protocol.SettingEntry, error) {
	resp, err := c.send(protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, SourceSessionID: protocol.Ptr(session), ProfileID: protocol.Ptr(profile), Key: key, Value: value})
	if err != nil {
		return nil, err
	}
	return resp.Setting, nil
}
