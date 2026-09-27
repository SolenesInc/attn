package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func actionNames(actions []protocol.SessionReopenAction) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		names = append(names, string(action))
	}
	return names
}

func wantReopenVerdict(
	t *testing.T, verdict *sessionReopenVerdict, reopenable bool, actions []protocol.SessionReopenAction,
) {
	t.Helper()
	if verdict.Reopenable != reopenable {
		t.Errorf("reopenable = %v, want %v (reason %q)", verdict.Reopenable, reopenable, verdict.Reason)
	}
	if !slices.Equal(actionNames(verdict.Actions), actionNames(actions)) {
		t.Errorf("actions = %v, want %v", actionNames(verdict.Actions), actionNames(actions))
	}
	if !reopenable && strings.TrimSpace(verdict.Reason) == "" {
		t.Error("a verdict that refuses a reopen carries no reason; an agent cannot act on that")
	}
}

func TestReopenVerdictSendsARemoteSessionToItsOwnDaemon(t *testing.T) {
	endpoints := []protocol.EndpointInfo{
		{ID: "outpost-7", Name: "big-linux", Status: "connected"},
		{ID: "outpost-8", Name: "sleepy-linux", Status: "disconnected"},
	}
	cases := map[string]struct {
		endpointID string
		wantTail   string
	}{
		"reachable":   {endpointID: "outpost-7", wantTail: "reopen it there"},
		"unreachable": {endpointID: "outpost-8", wantTail: "retry when it is"},
		"forgotten":   {endpointID: "outpost-nobody-configured", wantTail: "retry when it is"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			verdict := &sessionReopenVerdict{
				SessionID: "remote-one",
				Execution: garden.Dispatch{HostKind: garden.HostRemote, EndpointID: tc.endpointID},
			}
			if decideReopenHost(verdict, endpoints) {
				t.Fatal("a remote session went on being decided on this daemon")
			}
			wantReopenVerdict(t, verdict, false, nil)
			if !strings.Contains(verdict.Reason, tc.endpointID) &&
				!strings.Contains(verdict.Reason, "big-linux") &&
				!strings.Contains(verdict.Reason, "sleepy-linux") {
				t.Errorf("reason = %q, want the host named", verdict.Reason)
			}
			if !strings.Contains(verdict.Reason, tc.wantTail) {
				t.Errorf("reason = %q, want it to end with %q", verdict.Reason, tc.wantTail)
			}
		})
	}
}
