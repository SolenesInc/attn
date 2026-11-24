package claude

import (
	"testing"
	"time"
)

func TestParseClaudeFinalText(t *testing.T) {
	t.Run("single result object", func(t *testing.T) {
		got := parseClaudeFinalText([]byte(`{"type":"result","subtype":"success","result":"hello"}`))
		if got != "hello" {
			t.Fatalf("got %q, want hello", got)
		}
	})
	t.Run("stream array last result wins", func(t *testing.T) {
		stdout := []byte(`[{"type":"assistant","message":{"content":[{"type":"text","text":"thinking"}]}},{"type":"result","result":"final-answer"}]`)
		if got := parseClaudeFinalText(stdout); got != "final-answer" {
			t.Fatalf("got %q, want final-answer", got)
		}
	})
	t.Run("stream array falls back to assistant text", func(t *testing.T) {
		stdout := []byte(`[{"type":"assistant","message":{"content":[{"type":"text","text":"only-text"}]}}]`)
		if got := parseClaudeFinalText(stdout); got != "only-text" {
			t.Fatalf("got %q, want only-text", got)
		}
	})
}

func TestParseClaudeResultMeta(t *testing.T) {
	t.Run("single result object", func(t *testing.T) {
		meta := parseClaudeResultMeta([]byte(`{"type":"result","result":"{\"verdict\":\"ok\"}","structured_output":{"verdict":"ok"},"total_cost_usd":0.0053,"num_turns":2}`))
		if string(meta.StructuredOutput) != `{"verdict":"ok"}` {
			t.Fatalf("StructuredOutput = %s", meta.StructuredOutput)
		}
		if meta.TotalCostUSD != 0.0053 || meta.NumTurns != 2 {
			t.Fatalf("meta = %+v", meta)
		}
	})
	t.Run("stream array last result wins", func(t *testing.T) {
		stdout := []byte(`[{"type":"system","subtype":"init"},{"type":"assistant","message":{"content":[]}},{"type":"result","structured_output":{"verdict":"ok"},"total_cost_usd":0.5,"num_turns":15}]`)
		meta := parseClaudeResultMeta(stdout)
		if string(meta.StructuredOutput) != `{"verdict":"ok"}` || meta.NumTurns != 15 {
			t.Fatalf("meta = %+v", meta)
		}
	})
	t.Run("no result event yields zero meta", func(t *testing.T) {
		meta := parseClaudeResultMeta([]byte(`[{"type":"system","subtype":"init"}]`))
		if len(meta.StructuredOutput) != 0 || meta.TotalCostUSD != 0 || meta.NumTurns != 0 {
			t.Fatalf("meta = %+v, want zero", meta)
		}
	})
}

func TestClaudeWatcherBehaviorDetectsHaltedTurn(t *testing.T) {
	behavior := &claudeTranscriptWatcherBehavior{}
	got := behavior.HandleLine([]byte(`{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"interruptedMessageId":"msg_01","timestamp":"2026-08-01T22:08:15.284Z"}`))
	if !got.Aborted || got.AbortDetail != "[Request interrupted by user]" {
		t.Fatalf("got %+v, want the halt reported", got)
	}
	if got.AbortAt.UTC().Format(time.RFC3339Nano) != "2026-08-01T22:08:15.284Z" {
		t.Fatalf("abort at %s", got.AbortAt.UTC().Format(time.RFC3339Nano))
	}
	if got := behavior.HandleLine([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working on it"}]}}`)); got.Aborted {
		t.Fatalf("an ordinary line was read as a halt: %+v", got)
	}
	if got := behavior.HandleLine([]byte(`{"type":"custom-title","customTitle":"Fix checkout flow","sessionId":"s1"}`)); got.Title != "Fix checkout flow" || got.Aborted {
		t.Fatalf("custom-title line = %+v, want Title %q", got, "Fix checkout flow")
	}
}
