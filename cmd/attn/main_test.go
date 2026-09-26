package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseDirectLaunchArgs_ResumePickerWithFlagAfterResume(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--resume", "--yolo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !parsed.resumePicker {
		t.Fatalf("expected resume picker to be enabled")
	}
	if parsed.resumeID != "" {
		t.Fatalf("expected empty resume id, got %q", parsed.resumeID)
	}
	if !parsed.yoloMode {
		t.Fatalf("expected yolo flag to be preserved")
	}
}

func TestParseDirectLaunchArgs_MemberNamesTheSession(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--member", "trellis"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.member != "trellis" {
		t.Fatalf("member = %q, want trellis", parsed.member)
	}
	if parsed.label != "Trellis" {
		t.Fatalf("label = %q, want the member's name", parsed.label)
	}
}

func TestParseDirectLaunchArgs_LabelOverridesTheMemberName(t *testing.T) {
	parsed, err := parseDirectLaunchArgs([]string{"--member", "trellis", "-s", "crew slice 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.member != "trellis" || parsed.label != "crew slice 1" {
		t.Fatalf("member/label = %q/%q, want trellis/crew slice 1", parsed.member, parsed.label)
	}
}

func TestStopFacts(t *testing.T) {
	cases := []struct {
		name         string
		payload      string
		wantStatuses []string
		wantNames    []string
		wantCrons    int
	}{
		{
			name:         "workflow running (parent yields mid-run)",
			payload:      `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[{"id":"wv9p74ip7","type":"workflow","status":"running","name":"hello-parallel"}],"session_crons":[]}`,
			wantStatuses: []string{"running"},
		},
		{
			name:         "workflow plus background shells running",
			payload:      `{"background_tasks":[{"type":"workflow","status":"running"},{"type":"shell","status":"running"},{"type":"shell","status":"running"}]}`,
			wantStatuses: []string{"running", "running", "running"},
		},
		{
			name:    "empty background_tasks (workflow finished)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[]}`,
		},
		{
			name:    "fields absent (e.g. another agent)",
			payload: `{"hook_event_name":"Stop","stop_hook_active":false}`,
		},
		{
			name:         "task present but not running is still reported",
			payload:      `{"background_tasks":[{"type":"workflow","status":"completed"}]}`,
			wantStatuses: []string{"completed"},
		},
		{
			name:      "recurring cron pending",
			payload:   `{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo persist-probe"}]}`,
			wantCrons: 1,
		},
		{
			name:      "one-shot reminder pending",
			payload:   `{"hook_event_name":"Stop","stop_hook_active":false,"session_crons":[{"id":"5e9a0f21","schedule":"18 14 * * *","recurring":false,"prompt":"echo oneshot-fired"}]}`,
			wantCrons: 1,
		},
		{
			name:      "recurring plus one-shot pending",
			payload:   `{"session_crons":[{"id":"43f0809f","schedule":"*/30 * * * *","recurring":true,"prompt":"echo recurring-probe"},{"id":"2b1dec68","schedule":"15 9 20 6 *","recurring":false,"prompt":"echo oneshot-probe"}]}`,
			wantCrons: 2,
		},
		{
			name:         "background running and cron pending are reported together",
			payload:      `{"background_tasks":[{"type":"shell","status":"running"}],"session_crons":[{"id":"d0055050","schedule":"*/30 * * * *","recurring":true,"prompt":"echo x"}]}`,
			wantStatuses: []string{"running"},
			wantCrons:    1,
		},
		{
			name:         "a task's description travels as its name (captured from Claude Code 2.1.257)",
			payload:      `{"background_tasks":[{"id":"bzd8fe67e","type":"shell","status":"running","description":"Sleep for 90 seconds in background","command":"sleep 90"},{"id":"a2c193c118d82726c","type":"subagent","status":"running","description":"Run sleep 75 then report completion","agent_type":"general-purpose"}],"session_crons":[]}`,
			wantStatuses: []string{"running", "running"},
			wantNames:    []string{"Sleep for 90 seconds in background", "Run sleep 75 then report completion"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var input hookInput
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			facts := stopFacts(input)
			var statuses, names []string
			for _, task := range facts.BackgroundTasks {
				statuses = append(statuses, task.Status)
				names = append(names, protocol.Deref(task.Name))
			}
			if !slices.Equal(statuses, tc.wantStatuses) {
				t.Fatalf("background task statuses = %q, want %q", statuses, tc.wantStatuses)
			}
			if tc.wantNames != nil && !slices.Equal(names, tc.wantNames) {
				t.Fatalf("background task names = %q, want %q", names, tc.wantNames)
			}
			if facts.PendingSessionCrons != tc.wantCrons {
				t.Fatalf("PendingSessionCrons = %d, want %d", facts.PendingSessionCrons, tc.wantCrons)
			}
		})
	}
}
