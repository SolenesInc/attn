package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestRetiredCommandsAndDelegateFieldsAreRefusedOverTheCLISocket(t *testing.T) {
	w := newWorld(t)
	for _, cmd := range []string{
		"list_dispatches", "submit_dispatch_outcome", "handoff_dispatch", "get_dispatch", "resolve_dispatch_request",
		"send_dispatch_message", "list_dispatch_messages", "read_dispatch_message", "acknowledge_dispatch_message",
		"wake_dispatch_agent", "report_dispatch",
		"automode_models", "automode_pattern_add", "automode_pattern_remove",
	} {
		payload := `{"cmd":"` + cmd + `","source_session_id":"worker-1","request_id":"r1"}`
		if answer := autoModeUnixCall(t, w, payload); answer.Ok || !strings.Contains(protocol.Deref(answer.Error), "unknown command") {
			t.Errorf("retired %s answered %+v, want unknown command", cmd, answer)
		}
	}

	for _, field := range []string{"brief", "ticket_id", "confirm", "placement", "workspace_id", "worktree", "plot", "handover", "preferences_revision", "approval_policy", "sandbox_mode"} {
		payload := `{"cmd":"delegate","request_id":"mixed","assignment":{"kind":"new","brief":"work"},"cwd":"/tmp","` + field + `":null}`
		if answer := autoModeUnixCall(t, w, payload); answer.Ok || !strings.Contains(protocol.Deref(answer.Error), `field "`+field+`" is retired`) {
			t.Errorf("a delegate carrying the retired %s answered %+v, want a refusal naming it", field, answer)
		}
	}
}
