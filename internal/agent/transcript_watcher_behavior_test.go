package agent

import (
	"testing"
	"time"
)

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
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.behavior.HandleLine([]byte(tc.abort))
			if !got.Aborted || got.AbortDetail != tc.wantDetail {
				t.Fatalf("got %+v, want the halt reported with detail %q", got, tc.wantDetail)
			}
			if got.AbortAt.UTC().Format(time.RFC3339Nano) != tc.wantAt {
				t.Fatalf("abort at %s, want %s", got.AbortAt.UTC().Format(time.RFC3339Nano), tc.wantAt)
			}
			if got := tc.behavior.HandleLine([]byte(tc.ignored)); got.Aborted {
				t.Fatalf("an ordinary line was read as a halt: %+v", got)
			}
			if tc.notAHalt == "" {
				return
			}
			if got := tc.behavior.HandleLine([]byte(tc.notAHalt)); got.Aborted {
				t.Fatalf("a turn the agent ended on its own was read as a user halt: %+v", got)
			}
		})
	}
}
