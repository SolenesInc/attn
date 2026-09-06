package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSettingAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name, value         string
		closeStore, success bool
	}{
		{name: "persisted", value: "128000", success: true},
		{name: "validation", value: "1"},
		{name: "storage", value: "128000", closeStore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "daemon.sock"))
			t.Cleanup(func() { _ = d.store.Close() })
			if tc.closeStore {
				_ = d.store.Close()
			}
			client := newWorkspaceProtocolTestClient()
			requestID := "save-" + tc.name
			d.handleSetSettingWS(client, &protocol.SetSettingMessage{
				Cmd: protocol.CmdSetSetting, Key: "default_context_window_cap_codex", Value: tc.value, RequestID: &requestID,
			})
			var response protocol.SettingsUpdatedMessage
			if err := json.Unmarshal((<-client.send).payload, &response); err != nil {
				t.Fatal(err)
			}
			if protocol.Deref(response.RequestID) != requestID || protocol.Deref(response.Success) != tc.success {
				t.Fatalf("acknowledgement = %+v", response)
			}
			if tc.success && d.store.GetSetting("default_context_window_cap_codex") != tc.value {
				t.Fatal("acknowledged before persistence")
			}
			if !tc.success && protocol.Deref(response.Error) == "" {
				t.Fatal("failure omitted its reason")
			}
		})
	}
}
