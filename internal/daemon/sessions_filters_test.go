package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSessionsFiltersSettingRoundTrips(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "filters.sock"))
	t.Cleanup(d.stopEventBus)
	client := &wsClient{send: make(chan outboundMessage, 1)}
	stored := `{"scope":"closed","range":"custom","customFrom":"2026-08-01","customTo":"2026-08-31","workspaceId":"ws-1","repository":"/Users/victor/projects/attn"}`

	d.handleSetSettingWS(client, &protocol.SetSettingMessage{
		Cmd:   protocol.CmdSetSetting,
		Key:   SettingSessionsFilters,
		Value: stored,
	})

	if got := d.store.GetSetting(SettingSessionsFilters); got != stored {
		t.Fatalf("stored filters = %q, want %q", got, stored)
	}
}

func TestSessionsFiltersSettingRefusesUnknownShape(t *testing.T) {
	for name, value := range map[string]string{
		"unknown scope":    `{"scope":"archived","range":"any","customFrom":"","customTo":"","workspaceId":"","repository":""}`,
		"unknown range":    `{"scope":"all","range":"last-week","customFrom":"","customTo":"","workspaceId":"","repository":""}`,
		"unparsed date":    `{"scope":"all","range":"custom","customFrom":"yesterday","customTo":"","workspaceId":"","repository":""}`,
		"unknown field":    `{"scope":"all","range":"any","selectedId":"s1"}`,
		"not an object":    `["closed"]`,
		"a second object":  `{"scope":"all","range":"any"} {"scope":"closed","range":"any"}`,
		"trailing text":    `{"scope":"all","range":"any"} trailing`,
		"a trailing brace": `{"scope":"all","range":"any"}}`,
		"not valid JSON":   `{scope: closed}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSessionsFilters(value); err == nil {
				t.Fatalf("validateSessionsFilters(%q) succeeded", value)
			} else if !strings.Contains(err.Error(), SettingSessionsFilters) {
				t.Fatalf("error %q does not name %q", err, SettingSessionsFilters)
			}
		})
	}
}

func TestInvalidSessionsFiltersPreserveTheSavedOnes(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "filters.sock"))
	t.Cleanup(d.stopEventBus)
	valid := `{"scope":"closed","range":"7d","customFrom":"","customTo":"","workspaceId":"","repository":""}`
	d.store.SetSetting(SettingSessionsFilters, valid)
	client := &wsClient{send: make(chan outboundMessage, 1)}

	d.handleSetSettingWS(client, &protocol.SetSettingMessage{
		Cmd:   protocol.CmdSetSetting,
		Key:   SettingSessionsFilters,
		Value: `{"scope":"archived","range":"7d","customFrom":"","customTo":"","workspaceId":"","repository":""}`,
	})

	if got := d.store.GetSetting(SettingSessionsFilters); got != valid {
		t.Fatalf("saved filters = %q, want preserved %q", got, valid)
	}
	select {
	case outbound := <-client.send:
		var message protocol.SettingsUpdatedMessage
		if err := json.Unmarshal(outbound.payload, &message); err != nil {
			t.Fatalf("decode settings response: %v", err)
		}
		if message.Success == nil || *message.Success {
			t.Fatalf("settings response success = %v, want false", message.Success)
		}
		if message.Error == nil || !strings.Contains(*message.Error, SettingSessionsFilters) || !strings.Contains(*message.Error, "scope") {
			t.Fatalf("settings error = %v, want one naming the key and the scope", message.Error)
		}
	default:
		t.Fatal("invalid setting returned no response")
	}
}

func TestSessionsFiltersSettingAcceptsAbsence(t *testing.T) {
	if err := validateSessionsFilters(""); err != nil {
		t.Fatalf("empty filters were rejected: %v", err)
	}
}
