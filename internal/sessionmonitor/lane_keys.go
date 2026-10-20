package sessionmonitor

import (
	"bytes"
	"time"

	"github.com/victorarias/attn/internal/harness"
)

func (e *Entry) noteUserInputLocked(terminal harness.TerminalID, source string, data []byte) bool {
	if !IsComposerKeystroke(source, data) {
		return false
	}
	now := time.Now()
	e.registry.host.NoteKey(terminal, now)
	e.activityAt = now
	return true
}

func (e *Entry) NoteActivity() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	e.activityAt = time.Now()
	e.mu.Unlock()
	return true
}

func quietRemaining(last time.Time, within time.Duration) time.Duration {
	if last.IsZero() {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func isUserKeystrokeSource(source string) bool {
	switch source {
	case "agent", "automation", "attach_replay", "pointer", "response":
		return false
	default:
		return true
	}
}

func IsComposerKeystroke(source string, data []byte) bool {
	return isUserKeystrokeSource(source) && userInputEditsComposer(data)
}

func userInputEditsComposer(data []byte) bool {
	rest := data
	for len(rest) > 0 {
		switch {
		case bytes.HasPrefix(rest, []byte("\x1b[<")):
			end := bytes.IndexAny(rest, "Mm")
			if end < 0 {
				return true
			}
			rest = rest[end+1:]
		case bytes.HasPrefix(rest, []byte("\x1b[I")), bytes.HasPrefix(rest, []byte("\x1b[O")):
			rest = rest[3:]
		default:
			return true
		}
	}
	return false
}
