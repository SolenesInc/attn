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
