package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestSessionStateFromRecoveredInfo(t *testing.T) {
	tests := []struct {
		name   string
		info   ptybackend.SessionInfo
		want   protocol.SessionState
		wantOK bool
	}{
		{
			name:   "not running is idle",
			info:   ptybackend.SessionInfo{Running: false, State: protocol.StateWorking},
			want:   protocol.SessionStateIdle,
			wantOK: true,
		},
		{
			name:   "waiting input",
			info:   ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentClaude), State: protocol.StateWaitingInput},
			want:   protocol.SessionStateWaitingInput,
			wantOK: true,
		},
		{
			name: "codex waiting input is no opinion",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentCodex), State: protocol.StateWaitingInput},
		},
		{
			name:   "claude pending approval",
			info:   ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentClaude), State: protocol.StatePendingApproval},
			want:   protocol.SessionStatePendingApproval,
			wantOK: true,
		},
		{
			name:   "copilot pending approval",
			info:   ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentCopilot), State: protocol.StatePendingApproval},
			want:   protocol.SessionStatePendingApproval,
			wantOK: true,
		},
		{
			name: "running with the worker's default working is no opinion",
			info: ptybackend.SessionInfo{Running: true, State: protocol.StateWorking},
		},
		{
			name: "running with an empty state is no opinion",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentClaude)},
		},
		{
			name: "explicit idle on a running session is no opinion",
			info: ptybackend.SessionInfo{Running: true, Agent: string(protocol.SessionAgentClaude), State: protocol.StateIdle},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sessionStateFromRecoveredInfo(tt.info)
			if ok != tt.wantOK {
				t.Fatalf("sessionStateFromRecoveredInfo() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("sessionStateFromRecoveredInfo() = %s, want %s", got, tt.want)
			}
		})
	}
}
