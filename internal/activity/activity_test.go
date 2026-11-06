package activity

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestRenderKeepsEveryCharacterWhole(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		budget int
	}{
		{transcript.EventKindThinking, ClipThinking},
		{transcript.EventKindAssistant, ClipAssistant},
		{transcript.EventKindUser, ClipUser},
		{transcript.EventKindToolCall, ClipToolCall},
		{transcript.EventKindToolResult, ClipToolResult},
	} {
		for _, char := range []string{"é", "—", "😀"} {
			for overhang := 1; overhang < len(char); overhang++ {
				kept := strings.Repeat("x", tc.budget-len(char)+overhang)
				text := kept + char + strings.Repeat("y", 50)
				window := Window{Events: []transcript.Event{{Kind: tc.kind, Text: text}}}

				got := window.Render()

				if !utf8.ValidString(got) {
					t.Errorf("%s budget %d, %q overhanging by %d bytes: invalid UTF-8 %q", tc.kind, tc.budget, char, overhang, got)
				}
				if !strings.HasSuffix(got, kept+"…") {
					t.Errorf("%s budget %d, %q overhanging by %d bytes: want the text cut right before the character, got %q", tc.kind, tc.budget, char, overhang, got)
				}
			}
		}
	}
}
