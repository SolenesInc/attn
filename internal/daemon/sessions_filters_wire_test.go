package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const sessionsFiltersKey = "sessions.filters"

func TestTheSessionsFiltersAreKeptExactlyAsSentAndRefusedWhenMalformed(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	saved := `{"scope":"closed","range":"custom","customFrom":"2026-08-01","customTo":"2026-08-31","workspaceId":"ws-1","repository":"/Users/victor/projects/attn"}`
	setSetting(t, app, sessionsFiltersKey, saved)

	w.restart()
	app = w.App()
	if got := app.Initial.Settings[sessionsFiltersKey]; got != saved {
		t.Fatalf("filters after a restart = %v, want %s", got, saved)
	}

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
		refused := testworld.Request(app, protocol.SetSettingMessage{
			Cmd: protocol.CmdSetSetting, Key: sessionsFiltersKey, Value: value, RequestID: protocol.Ptr(name),
		}, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == name })
		if protocol.Deref(refused.Success) || !strings.Contains(protocol.Deref(refused.Error), sessionsFiltersKey) {
			t.Errorf("%s: set %s = %+v, want a refusal naming %s", name, value, refused, sessionsFiltersKey)
		}
		if name == "unknown scope" && !strings.Contains(protocol.Deref(refused.Error), "scope") {
			t.Errorf("%s: refusal %q does not name the scope", name, protocol.Deref(refused.Error))
		}
	}
	if got := w.App().Initial.Settings[sessionsFiltersKey]; got != saved {
		t.Errorf("filters after the refused writes = %v, want the saved %s", got, saved)
	}

	setSetting(t, app, sessionsFiltersKey, "")
}
