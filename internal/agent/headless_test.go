package agent

import (
	"strings"
	"testing"
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
