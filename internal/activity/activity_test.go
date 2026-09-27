package activity

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/transcript"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestCapKeepsNewestAndReportsTheDrop(t *testing.T) {
	events := make([]transcript.Event, MaxEvents+5)
	for i := range events {
		events[i] = transcript.Event{Kind: transcript.EventKindToolCall, ToolName: "Bash", Text: string(rune('a' + i%26))}
	}
	events[len(events)-1].Text = "LAST"
	window := Window{Events: events}
	window.Report.TotalEvents = len(events)
	window.cap()

	if len(window.Events) != MaxEvents {
		t.Fatalf("events = %d, want %d", len(window.Events), MaxEvents)
	}
	if window.Events[len(window.Events)-1].Text != "LAST" {
		t.Error("cap dropped the newest event; it must drop the oldest")
	}
	if !window.Report.Truncated() {
		t.Fatal("report does not say the window was truncated")
	}
	note := window.Report.String()
	if !strings.Contains(note, "5") || !strings.Contains(note, "max_events=200") {
		t.Errorf("report must name the limit, its value, and the ask; got %q", note)
	}
	if !strings.Contains(window.Render(), "window truncated") {
		t.Error("rendered window must carry the truncation note")
	}
}

func TestClipIsPerKind(t *testing.T) {
	long := strings.Repeat("x", 5000)
	for _, tc := range []struct {
		kind  string
		limit int
	}{
		{transcript.EventKindThinking, ClipThinking},
		{transcript.EventKindAssistant, ClipAssistant},
		{transcript.EventKindToolResult, ClipToolResult},
	} {
		got := clip(transcript.Event{Kind: tc.kind, Text: long})
		if len([]rune(got)) > tc.limit+1 {
			t.Errorf("%s clipped to %d, want <= %d", tc.kind, len(got), tc.limit)
		}
	}
}

func TestCheckCatchesALineThatContradictsABlockedState(t *testing.T) {
	violations := Check("Running the frontend test suite in attn--brisk-toucan", "pending_approval")
	if !hasCheck(violations, "state_consistency") {
		t.Fatalf("a line narrating active work for a blocked session must fail state_consistency; got %v", violations)
	}
	if v := Check("Awaiting approval to delete migrations/0042.sql", "pending_approval"); len(v) != 0 {
		t.Errorf("a line that acknowledges the block must pass; got %v", v)
	}
	if v := Check("Running the frontend test suite in attn--brisk-toucan", "working"); len(v) != 0 {
		t.Errorf("active narration is correct for working; got %v", v)
	}
	for _, line := range []string{
		"Completed activity-bench harness; cost error requires design revision",
		"Halted on a failing migration in internal/store",
		"Stuck on an unresolved import in app/src/App.tsx",
	} {
		if v := Check(line, "idle"); len(v) != 0 {
			t.Errorf("line %q acknowledges the session stopped; got %v", line, v)
		}
	}
}

func TestCheckCatchesFormatFailures(t *testing.T) {
	for _, tc := range []struct {
		name, line, state, want string
	}{
		{"empty", "  ", "working", "nonempty"},
		{"too long", strings.Repeat("x", MaxLineRunes+1), "working", "length"},
		{"trailing period", "Fixing the migration.", "working", "no_trailing_period"},
		{"quoted", `"Fixing the migration"`, "working", "no_quotes"},
		{"preamble", "The agent is fixing the migration", "working", "no_preamble"},
		{"newline", "Fixing\nthe migration", "working", "single_line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !hasCheck(Check(tc.line, tc.state), tc.want) {
				t.Errorf("Check(%q) did not report %s", tc.line, tc.want)
			}
		})
	}
	if hasCheck(Check("Fixing auth: token refresh loops", "working"), "no_preamble") {
		t.Error("a mid-line colon must not read as a preamble")
	}
}

func hasCheck(violations []Violation, name string) bool {
	for _, violation := range violations {
		if violation.Check == name {
			return true
		}
	}
	return false
}
