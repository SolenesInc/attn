package agent

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHasCopilotTranscriptPendingApproval(t *testing.T) {
	now := time.Now()
	stalled := now.Add(-(copilotToolStartGraceTime + 10*time.Millisecond))
	cases := []struct {
		name     string
		pending  map[string]copilotPendingTool
		turnOpen bool
		want     bool
	}{
		{"a stalled bash beside a fast view", map[string]copilotPendingTool{"view-fast": {name: "view", startedAt: now.Add(-10 * time.Second)}, "bash-stalled": {name: "bash", startedAt: stalled}}, true, true},
		{"a stalled create", map[string]copilotPendingTool{"create-stalled": {name: "create", startedAt: stalled}}, true, true},
		{"a bash still inside its grace window", map[string]copilotPendingTool{"bash-recent": {name: "bash", startedAt: now.Add(-(copilotToolStartGraceTime - 50*time.Millisecond))}}, true, false},
		{"a stalled bash after the turn closed", map[string]copilotPendingTool{"bash-stalled": {name: "bash", startedAt: stalled}}, false, false},
	}
	for _, tc := range cases {
		if got := hasCopilotTranscriptPendingApproval(tc.pending, now, tc.turnOpen); got != tc.want {
			t.Errorf("%s: pending approval = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestShouldPromoteTranscriptPending(t *testing.T) {
	for state, want := range map[protocol.SessionState]bool{
		protocol.SessionStateWorking:         false,
		protocol.SessionStatePendingApproval: false,
		protocol.SessionStateIdle:            true,
		protocol.SessionStateWaitingInput:    true,
		protocol.SessionStateUnknown:         true,
		protocol.SessionStateLaunching:       true,
	} {
		if got := shouldPromoteTranscriptPending(state); got != want {
			t.Errorf("promote from %s = %v, want %v", state, got, want)
		}
	}
}

func TestWatcherBehaviorsDetectAHaltedTurn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		behavior   TranscriptWatcherBehavior
		abort      string
		wantDetail string
		wantAt     string
		ignored    string
		notAHalt   string
	}{
		{
			name:       "claude",
			behavior:   &claudeTranscriptWatcherBehavior{},
			abort:      `{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"interruptedMessageId":"msg_01","timestamp":"2026-08-01T22:08:15.284Z"}`,
			wantDetail: "[Request interrupted by user]",
			wantAt:     "2026-08-01T22:08:15.284Z",
			ignored:    `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working on it"}]}}`,
		},
		{
			name:       "codex",
			behavior:   &codexTranscriptWatcherBehavior{},
			abort:      `{"type":"event_msg","timestamp":"2026-08-01T21:58:33.937Z","payload":{"type":"turn_aborted","reason":"interrupted"}}`,
			wantDetail: "interrupted",
			wantAt:     "2026-08-01T21:58:33.937Z",
			ignored:    `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"019f"}}`,
			notAHalt:   `{"type":"event_msg","payload":{"type":"turn_aborted","reason":"replaced"}}`,
		},
		{
			name:       "copilot",
			behavior:   newCopilotBehavior(),
			abort:      `{"type":"abort","timestamp":"2026-08-02T08:52:00.344Z","data":{"reason":"user_initiated"}}`,
			wantDetail: "user_initiated",
			wantAt:     "2026-08-02T08:52:00.344Z",
			ignored:    `{"type":"assistant.message","data":{"content":"working on it"}}`,
			notAHalt:   `{"type":"abort","data":{"reason":"tool_failure"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.behavior.HandleLine([]byte(tc.abort), time.Now(), protocol.SessionStateWorking)
			if !got.Aborted || got.AbortDetail != tc.wantDetail {
				t.Fatalf("got %+v, want the halt reported with detail %q", got, tc.wantDetail)
			}
			if got.AbortAt.UTC().Format(time.RFC3339Nano) != tc.wantAt {
				t.Fatalf("abort at %s, want %s", got.AbortAt.UTC().Format(time.RFC3339Nano), tc.wantAt)
			}
			if got.State != "" {
				t.Fatalf("state %q, want none: the behavior reports the fact, the resolver decides", got.State)
			}
			if got := tc.behavior.HandleLine([]byte(tc.ignored), time.Now(), protocol.SessionStateWorking); got.Aborted {
				t.Fatalf("an ordinary line was read as a halt: %+v", got)
			}
			if tc.notAHalt == "" {
				return
			}
			if got := tc.behavior.HandleLine([]byte(tc.notAHalt), time.Now(), protocol.SessionStateWorking); got.Aborted {
				t.Fatalf("a turn the agent ended on its own was read as a user halt: %+v", got)
			}
		})
	}
}

func newCopilotBehavior() TranscriptWatcherBehavior {
	b := &copilotTranscriptWatcherBehavior{}
	b.Reset()
	return b
}

func TestCopilotAbortClosesTheTurnBracket(t *testing.T) {
	for _, abort := range []string{
		`{"type":"abort","data":{"reason":"user_initiated"}}`,
		`{"type":"abort","data":{"reason":"tool_failure"}}`,
	} {
		t.Run(abort, func(t *testing.T) {
			b := &copilotTranscriptWatcherBehavior{}
			b.Reset()

			now := time.Now()
			b.HandleLine([]byte(`{"type":"assistant.turn_start","data":{"turnId":"0"}}`), now, protocol.SessionStateWorking)
			b.HandleLine([]byte(`{"type":"tool.execution_start","data":{"toolCallId":"call_1","toolName":"bash"}}`), now, protocol.SessionStateWorking)
			if tick := b.Tick(now.Add(2*time.Second), protocol.SessionStateWorking); !tick.BlockClassification {
				t.Fatal("an open copilot turn should block classification")
			}

			got := b.HandleLine([]byte(abort), now.Add(3*time.Second), protocol.SessionStateWorking)

			if !got.Aborted && !got.BracketClosed {
				t.Fatalf("got %+v, want the evidence bracket closed: nothing else closes it", got)
			}

			tick := b.Tick(now.Add(4*time.Second), protocol.SessionStateWorking)
			if tick.BlockClassification {
				t.Fatal("the aborted turn is still pinning the session working")
			}
			if tick.State != "" {
				t.Fatalf("state %q, want none after an abort", tick.State)
			}
		})
	}
}
