package codex

import (
	"strings"
	"testing"
	"time"
)

func TestCodexParseFinalTextFromStdoutFallback(t *testing.T) {
	stdout := []byte(strings.Join([]string{
		`{"type":"thread.started","thread_id":"t"}`,
		`{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"first"}}`,
		`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"FINAL"}}`,
		`{"type":"turn.completed","usage":{}}`,
	}, "\n"))
	if got := parseCodexFinalText(stdout); got != "FINAL" {
		t.Fatalf("parseCodexFinalText = %q, want FINAL", got)
	}
}

func TestCodexWatcherBehaviorDetectsHaltedTurn(t *testing.T) {
	behavior := &codexTranscriptWatcherBehavior{}
	got := behavior.HandleLine([]byte(`{"type":"event_msg","timestamp":"2026-08-01T21:58:33.937Z","payload":{"type":"turn_aborted","reason":"interrupted"}}`))
	if !got.Aborted || got.AbortDetail != "interrupted" {
		t.Fatalf("got %+v, want the halt reported", got)
	}
	if got.AbortAt.UTC().Format(time.RFC3339Nano) != "2026-08-01T21:58:33.937Z" {
		t.Fatalf("abort at %s", got.AbortAt.UTC().Format(time.RFC3339Nano))
	}
	if got := behavior.HandleLine([]byte(`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"019f"}}`)); got.Aborted {
		t.Fatalf("an ordinary line was read as a halt: %+v", got)
	}
	if got := behavior.HandleLine([]byte(`{"type":"event_msg","payload":{"type":"turn_aborted","reason":"replaced"}}`)); got.Aborted {
		t.Fatalf("a turn the agent ended on its own was read as a user halt: %+v", got)
	}
}
