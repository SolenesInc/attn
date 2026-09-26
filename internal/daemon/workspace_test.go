package daemon

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestRollupWorkspaceStatus_PriorityOrdering(t *testing.T) {
	cases := []struct {
		name   string
		states []protocol.SessionState
		want   protocol.WorkspaceStatus
	}{
		{
			name:   "empty yields idle",
			states: nil,
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "single working",
			states: []protocol.SessionState{protocol.SessionStateWorking},
			want:   protocol.WorkspaceStatusWorking,
		},
		{
			name:   "working beats launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateWorking},
			want:   protocol.WorkspaceStatusWorking,
		},
		{
			name:   "working beats waiting_input",
			states: []protocol.SessionState{protocol.SessionStateWaitingInput, protocol.SessionStateWorking},
			want:   protocol.WorkspaceStatusWorking,
		},
		{
			name:   "waiting_input beats pending_approval",
			states: []protocol.SessionState{protocol.SessionStatePendingApproval, protocol.SessionStateWaitingInput},
			want:   protocol.WorkspaceStatusWaitingInput,
		},
		{
			name:   "pending_approval beats idle",
			states: []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStatePendingApproval},
			want:   protocol.WorkspaceStatusPendingApproval,
		},
		{
			name:   "pending_approval beats scheduled",
			states: []protocol.SessionState{protocol.SessionStateScheduled, protocol.SessionStatePendingApproval},
			want:   protocol.WorkspaceStatusPendingApproval,
		},
		{
			name:   "scheduled beats idle",
			states: []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateScheduled},
			want:   protocol.WorkspaceStatusScheduled,
		},
		{
			name:   "scheduled beats launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateScheduled},
			want:   protocol.WorkspaceStatusScheduled,
		},
		{
			name:   "all scheduled yields scheduled",
			states: []protocol.SessionState{protocol.SessionStateScheduled, protocol.SessionStateScheduled},
			want:   protocol.WorkspaceStatusScheduled,
		},
		{
			name:   "idle beats launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateIdle},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "recoverable yields idle",
			states: []protocol.SessionState{protocol.SessionStateRecoverable},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "idle beats recoverable",
			states: []protocol.SessionState{protocol.SessionStateRecoverable, protocol.SessionStateIdle},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "recoverable beats launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateRecoverable},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "unknown beats launching",
			states: []protocol.SessionState{protocol.SessionStateUnknown, protocol.SessionStateLaunching},
			want:   protocol.WorkspaceStatusUnknown,
		},
		{
			name:   "unknown beats idle",
			states: []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateUnknown},
			want:   protocol.WorkspaceStatusUnknown,
		},
		{
			name:   "unknown beats scheduled",
			states: []protocol.SessionState{protocol.SessionStateScheduled, protocol.SessionStateUnknown},
			want:   protocol.WorkspaceStatusUnknown,
		},
		{
			name:   "pending_approval beats unknown",
			states: []protocol.SessionState{protocol.SessionStateUnknown, protocol.SessionStatePendingApproval},
			want:   protocol.WorkspaceStatusPendingApproval,
		},
		{
			name:   "working beats unknown",
			states: []protocol.SessionState{protocol.SessionStateUnknown, protocol.SessionStateWorking},
			want:   protocol.WorkspaceStatusWorking,
		},
		{
			name:   "all session_state_unknown yields unknown",
			states: []protocol.SessionState{protocol.SessionStateUnknown, protocol.SessionStateUnknown},
			want:   protocol.WorkspaceStatusUnknown,
		},
		{
			name:   "all idle yields idle",
			states: []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateIdle},
			want:   protocol.WorkspaceStatusIdle,
		},
		{
			name:   "all launching yields launching",
			states: []protocol.SessionState{protocol.SessionStateLaunching, protocol.SessionStateLaunching},
			want:   protocol.WorkspaceStatusLaunching,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rollupWorkspaceStatus(tc.states)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func newDaemonForTest(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

type broadcastCapture struct {
	mu     sync.Mutex
	events []protocol.WebSocketEvent
}

func (c *broadcastCapture) snapshot() []protocol.WebSocketEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]protocol.WebSocketEvent, len(c.events))
	copy(out, c.events)
	return out
}

func captureBroadcasts(d *Daemon) *broadcastCapture {
	c := &broadcastCapture{}
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event == nil {
			return
		}
		c.mu.Lock()
		c.events = append(c.events, *event)
		c.mu.Unlock()
	}
	return c
}
