package protocol

import (
	"strings"
	"testing"
)

func TestParseMessageRejectsRetiredDispatchCommands(t *testing.T) {
	retired := []string{
		"list_dispatches",
		"submit_dispatch_outcome",
		"handoff_dispatch",
		"get_dispatch",
		"resolve_dispatch_request",
		"send_dispatch_message",
		"list_dispatch_messages",
		"read_dispatch_message",
		"acknowledge_dispatch_message",
		"wake_dispatch_agent",
		"report_dispatch",
	}
	for _, cmd := range retired {
		_, _, err := ParseMessage([]byte(`{"cmd":"` + cmd + `","source_session_id":"worker-1"}`))
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("retired %q error = %v, want unknown command", cmd, err)
		}
	}
}

func TestParseMessageRejectsRetiredAutoModeCommands(t *testing.T) {
	for _, input := range []string{
		`{"cmd":"automode_model_set","models":["a/one"],"request_id":"r1"}`,
		`{"cmd":"automode_models","request_id":"r1"}`,
		`{"cmd":"automode_pattern_add","list":"allow","pattern":"git status*","request_id":"r1"}`,
		`{"cmd":"automode_pattern_remove","list":"allow","pattern":"git status*","request_id":"r1"}`,
	} {
		if _, _, err := ParseMessage([]byte(input)); err == nil ||
			!strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%s error = %v, want unknown command", input, err)
		}
	}
}
