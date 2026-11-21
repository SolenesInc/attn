package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) settingsCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"settings list": command.Typed(func(_ context.Context, c command.Caller, args command.SettingsListArgs) (any, error) {
			list, _, err := d.settingsList(settingsQuery(c, "", args.All))
			return list, err
		}),
		"settings get": command.Typed(func(_ context.Context, c command.Caller, args command.SettingsGetArgs) (any, error) {
			_, entry, err := d.settingsList(settingsQuery(c, args.Key, false))
			return entry, err
		}),
		"settings set": command.Typed(func(_ context.Context, c command.Caller, args command.SettingsSetArgs) (any, error) {
			session, profile := settingsCaller(c)
			return d.setSetting(&protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, SourceSessionID: session, ProfileID: profile, Key: args.Key, Value: protocol.Deref(args.Value)})
		}),
	}
}

func settingsCaller(c command.Caller) (*protocol.SessionID, *string) {
	var session *protocol.SessionID
	var profile *string
	if c.Session != "" {
		session = protocol.Ptr(c.Session)
	}
	if c.Profile != "" {
		profile = protocol.Ptr(c.Profile)
	}
	return session, profile
}

func settingsQuery(c command.Caller, key string, all bool) *protocol.GetSettingsMessage {
	session, profile := settingsCaller(c)
	return &protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings, SourceSessionID: session, ProfileID: profile, Key: protocol.Ptr(key), All: protocol.Ptr(all)}
}
