package daemon_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheSharedPTYHostSettingRefusesWhatItCannotHonour(t *testing.T) {
	w := newWorld(t)
	app := w.App()

	for _, tc := range []struct{ name, key, value string }{
		{name: "not a boolean", key: "pty_shared_host_enabled", value: "maybe"},
		{name: "the derived status", key: "pty_shared_host_active", value: "true"},
		{name: "a backend without a shared host", key: "pty_shared_host_enabled", value: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestID := uuid.NewString()
			answer := testworld.Request(app, protocol.SetSettingMessage{
				Cmd: protocol.CmdSetSetting, Key: tc.key, Value: tc.value, RequestID: protocol.Ptr(requestID),
			}, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
			if protocol.Deref(answer.Success) || protocol.Deref(answer.Error) == "" {
				t.Errorf("set %s=%s answered success=%t, want a refusal with its reason", tc.key, tc.value, protocol.Deref(answer.Success))
			}
			if enabled, active := answer.Settings["pty_shared_host_enabled"], answer.Settings["pty_shared_host_active"]; enabled != "false" || active != "false" {
				t.Errorf("after the refusal the shared host reads enabled=%s active=%s, want both false", enabled, active)
			}
		})
	}
}
