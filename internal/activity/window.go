// Design and measurements: docs/plans/2026-08-07-session-activity.md
package activity

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/transcript"
)

// Clip budgets per event kind, in characters. Thinking is the longest content type
// (measured: p95 3,520 chars, max 20,443), so it gets the largest budget.
const (
	ClipThinking   = 600
	ClipAssistant  = 400
	ClipUser       = 300
	ClipToolCall   = 200
	ClipToolResult = 150
)

// Tripwires set past the observed maximum. Measured across 617 active 3-minute
// windows: 64 events and 12,408 rendered chars at the maximum.
const (
	MaxEvents = 200
	MaxChars  = 32000
)

type Window struct {
	Events     []transcript.Event
	NextCursor string
	Report     Report
}

type Report struct {
	DroppedOld  int
	TotalEvents int
	HitEventCap bool
	HitCharCap  bool
}

func (r Report) Truncated() bool { return r.DroppedOld > 0 }

func (r Report) String() string {
	if !r.Truncated() {
		return ""
	}
	switch {
	case r.HitEventCap:
		return fmt.Sprintf("dropped %d of %d events (max_events=%d)", r.DroppedOld, r.TotalEvents, MaxEvents)
	case r.HitCharCap:
		return fmt.Sprintf("dropped %d of %d events (max_chars=%d)", r.DroppedOld, r.TotalEvents, MaxChars)
	default:
		return fmt.Sprintf("dropped %d of %d events", r.DroppedOld, r.TotalEvents)
	}
}

func (w Window) Empty() bool { return len(w.Events) == 0 }

// MaxPages bounds how far Read walks to reach the end of a delta. A tripwire:
// the largest measured delta across a working day is well under one page.
const MaxPages = 50

var ErrDeltaTooLarge = fmt.Errorf("activity: delta exceeds %d pages of %d events", MaxPages, MaxEvents)

func Read(path, agent, cursor string) (Window, error) {
	window := Window{}
	at := cursor
	for page := 0; ; page++ {
		if page >= MaxPages {
			return Window{}, ErrDeltaTooLarge
		}
		read, err := transcript.ReadEventPage(path, agent, at, MaxEvents+1)
		if err != nil {
			return Window{}, err
		}
		window.Report.TotalEvents += len(read.Events)
		window.NextCursor = read.NextCursor
		window.Events = tail(append(window.Events, read.Events...), MaxEvents+1)
		if read.AtEnd {
			break
		}
		at = read.NextCursor
	}
	window.cap()
	return window, nil
}

func tail(events []transcript.Event, n int) []transcript.Event {
	if len(events) <= n {
		return events
	}
	return append(events[:0], events[len(events)-n:]...)
}

// A full scan measured 1.37s on the largest live transcript.
func SeedCursor(path string) (string, error) {
	return transcript.HeadCursor(path)
}

func (w *Window) cap() {
	if w.Report.TotalEvents > MaxEvents {
		w.Report.DroppedOld += w.Report.TotalEvents - MaxEvents
		w.Report.HitEventCap = true
	}
	if len(w.Events) > MaxEvents {
		w.Events = w.Events[len(w.Events)-MaxEvents:]
	}
	total := 0
	keepFrom := len(w.Events)
	for i := len(w.Events) - 1; i >= 0; i-- {
		size := len(clip(w.Events[i])) + 24
		if total+size > MaxChars {
			break
		}
		total += size
		keepFrom = i
	}
	if keepFrom > 0 {
		w.Report.DroppedOld += keepFrom
		w.Report.HitCharCap = true
		w.Events = w.Events[keepFrom:]
	}
}

func clipFor(kind string) int {
	switch kind {
	case transcript.EventKindThinking:
		return ClipThinking
	case transcript.EventKindAssistant:
		return ClipAssistant
	case transcript.EventKindUser:
		return ClipUser
	case transcript.EventKindToolCall:
		return ClipToolCall
	default:
		return ClipToolResult
	}
}

func clip(event transcript.Event) string {
	text := strings.Join(strings.Fields(event.Text), " ")
	budget := clipFor(event.Kind)
	if len(text) <= budget {
		return text
	}
	return text[:budget] + "…"
}

func (w Window) Render() string {
	var b strings.Builder
	for _, event := range w.Events {
		text := clip(event)
		if text == "" {
			continue
		}
		switch event.Kind {
		case transcript.EventKindToolCall:
			fmt.Fprintf(&b, "tool_call %s: %s\n", event.ToolName, text)
		case transcript.EventKindToolResult:
			if event.IsError {
				fmt.Fprintf(&b, "tool_result ERROR: %s\n", text)
				continue
			}
			fmt.Fprintf(&b, "tool_result: %s\n", text)
		default:
			fmt.Fprintf(&b, "%s: %s\n", event.Kind, text)
		}
	}
	if note := w.Report.String(); note != "" {
		fmt.Fprintf(&b, "\n[window truncated: %s]\n", note)
	}
	return strings.TrimRight(b.String(), "\n")
}
