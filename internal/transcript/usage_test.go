package transcript

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/sessioncost"
)

// Redacted Claude Code 2.1.233 and Codex 0.147.0 captures: shapes and counts kept,
// content replaced. The pi capture is verbatim, plus hand-written guardian entries.
func usageFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "usage", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("usage fixture %s: %v", name, err)
	}
	return path
}

func TestFollowerExtractsClaudeUsageFromCapturedTranscript(t *testing.T) {
	follower, err := NewFollower(usageFixture(t, "claude-2.1.233.jsonl"), "claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Usage) != 3 {
		t.Fatalf("usage = %+v, want initial and revised snapshots plus the next message", batch.Usage)
	}

	fiveMinute := batch.Usage[0]
	if fiveMinute.Key != "claude:msg_redacted_5m" || fiveMinute.Model != "claude-opus-5" {
		t.Fatalf("5m identity = %+v", fiveMinute)
	}
	if fiveMinute.InputTokens != 2 || fiveMinute.OutputTokens != 4 || fiveMinute.CacheWrite5mTokens != 7391 || fiveMinute.CacheWrite1hTokens != 0 || fiveMinute.CacheReadTokens != 4709 {
		t.Fatalf("5m usage = %+v", fiveMinute)
	}
	revised := batch.Usage[1]
	if revised.Key != fiveMinute.Key || revised.OutputTokens != 305 || revised.CacheWrite5mTokens != 7391 {
		t.Fatalf("revised 5m usage = %+v", revised)
	}

	oneHour := batch.Usage[2]
	if oneHour.Key != "claude:msg_redacted_1h_iteration" || oneHour.Model != "claude-opus-5" {
		t.Fatalf("1h identity = %+v", oneHour)
	}
	if oneHour.InputTokens != 2 || oneHour.OutputTokens != 3241 || oneHour.CacheWrite1hTokens != 4111 || oneHour.CacheWrite5mTokens != 0 || oneHour.CacheReadTokens != 262963 {
		t.Fatalf("iteration-backed usage = %+v", oneHour)
	}
}

func TestFollowerExtractsCodexLastUsageWithCachedAndReasoningSubsets(t *testing.T) {
	follower, err := NewFollower(usageFixture(t, "codex-0.147.0.jsonl"), "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Usage) != 2 {
		t.Fatalf("usage = %+v, want two non-null token_count observations", batch.Usage)
	}
	first := batch.Usage[0]
	// The captured record reports 256 reasoning tokens within 563 output tokens:
	// total_tokens is exactly input_tokens + output_tokens, not reasoning on top.
	if first.Model != "gpt-5.5" || first.InputTokens != 8427 || first.CacheReadTokens != 24448 || first.OutputTokens != 563 {
		t.Fatalf("first usage = %+v", first)
	}
	second := batch.Usage[1]
	if second.Model != "gpt-5.5" || second.InputTokens != 6683 || second.CacheReadTokens != 32640 || second.OutputTokens != 297 {
		t.Fatalf("second usage = %+v", second)
	}
	if first.Key == "" || second.Key == "" || first.Key == second.Key {
		t.Fatalf("record keys are not stable and distinct: %q %q", first.Key, second.Key)
	}
}

func TestFollowerRestoresCodexModelBeforeBootstrapOffset(t *testing.T) {
	path := usageFixture(t, "codex-0.147.0.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	needle := []byte(`{"timestamp":"2026-06-13T18:12:54.501Z"`)
	offset := bytes.Index(data, needle)
	if offset < 0 {
		t.Fatal("captured token_count line not found")
	}
	follower, err := NewFollower(path, "codex", int64(offset))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Usage) != 2 || batch.Usage[0].Model != "gpt-5.5" {
		t.Fatalf("bootstrapped usage = %+v", batch.Usage)
	}
}

func TestUsageExtractorEmitsRevisedClaudeSnapshotWithTheSameKey(t *testing.T) {
	extractor := NewUsageExtractor("claude")
	firstLine := []byte(`{"type":"assistant","message":{"id":"msg-progress","model":"model-x","usage":{"input_tokens":10,"output_tokens":0}}}`)
	revisedLine := []byte(`{"type":"assistant","message":{"id":"msg-progress","model":"model-x","usage":{"input_tokens":12,"output_tokens":3}}}`)
	first, ok := extractor.Observe(firstLine, "ignored")
	if !ok {
		t.Fatal("first absolute snapshot was not emitted")
	}
	if _, ok := extractor.Observe(firstLine, "ignored"); ok {
		t.Fatal("identical adjacent Claude snapshot was emitted twice")
	}
	revised, ok := extractor.Observe(revisedLine, "ignored")
	if !ok || revised.Key != first.Key || revised.InputTokens != 12 || revised.OutputTokens != 3 {
		t.Fatalf("revised snapshot = %+v, ok=%v, first=%+v", revised, ok, first)
	}
}

func TestUsageExtractorRejectsImpossibleCodexCacheSubset(t *testing.T) {
	extractor := NewUsageExtractor("codex")
	_, _ = extractor.Observe([]byte(`{"type":"turn_context","payload":{"model":"gpt-test"}}`), "context")
	line := []byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":4,"cached_input_tokens":5,"output_tokens":1}}}}`)
	if usage, ok := extractor.Observe(line, "record"); ok {
		t.Fatalf("impossible usage was accepted: %+v", usage)
	}
}

func TestUsageExtractorPreservesUnclassifiedClaudeCacheWrites(t *testing.T) {
	extractor := NewUsageExtractor("claude")
	line := []byte(`{"type":"assistant","message":{"id":"old-shape","model":"claude-test","usage":{"input_tokens":2,"cache_creation_input_tokens":17,"cache_read_input_tokens":3,"output_tokens":5}}}`)
	usage, ok := extractor.Observe(line, "ignored")
	if !ok || usage.CacheWriteUnclassifiedTokens != 17 || usage.CacheWrite5mTokens != 0 || usage.CacheWrite1hTokens != 0 {
		t.Fatalf("old cache-write shape = %+v, ok=%v", usage, ok)
	}
}

func TestSupportsUsage(t *testing.T) {
	for _, agent := range []string{"claude", " CODEX "} {
		if !SupportsUsage(agent) {
			t.Fatalf("SupportsUsage(%q) = false", agent)
		}
	}
	for _, agent := range []string{"copilot", "shell", "plugin-driver", ""} {
		if SupportsUsage(agent) {
			t.Fatalf("SupportsUsage(%q) = true", agent)
		}
	}
}
func TestFollowerExtractsPiAgentAndGuardianUsage(t *testing.T) {
	follower, err := NewFollower(usageFixture(t, "pi-0.83.0.jsonl"), "pi", 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Usage) != 8 {
		t.Fatalf("usage = %+v, want six assistant messages and two guardian decisions", batch.Usage)
	}

	first := batch.Usage[0]
	if first.Key != "pi:a4e94c7b" || first.Model != "deepseek-v4-flash" || first.Purpose != sessioncost.PurposeAgent {
		t.Fatalf("first assistant identity = %+v", first)
	}
	if first.InputTokens != 5379 || first.OutputTokens != 232 || first.CacheReadTokens != 0 || first.CacheWriteUnclassifiedTokens != 0 {
		t.Fatalf("first assistant tokens = %+v", first)
	}
	if first.ReportedCostUSD != 0.0013365000000000002 {
		t.Fatalf("first assistant reported cost = %v", first.ReportedCostUSD)
	}

	cached := batch.Usage[1]
	if cached.Key != "pi:7ab076d2" || cached.CacheReadTokens != 5504 || cached.InputTokens != 474 {
		t.Fatalf("second assistant usage = %+v", cached)
	}

	for _, usage := range batch.Usage[:6] {
		if usage.Purpose != sessioncost.PurposeAgent {
			t.Fatalf("assistant message priced as %q: %+v", usage.Purpose, usage)
		}
	}

	guardian := batch.Usage[6]
	if guardian.Key != "pi:c1f0a2b7" || guardian.Purpose != sessioncost.PurposeGuardian || guardian.Model != "deepseek-v4-flash" {
		t.Fatalf("guardian identity = %+v", guardian)
	}
	if guardian.InputTokens != 812 || guardian.OutputTokens != 64 || guardian.ReportedCostUSD != 0.00022088 {
		t.Fatalf("guardian usage = %+v", guardian)
	}
	second := batch.Usage[7]
	if second.Key != "pi:e5b41d90" || second.Purpose != sessioncost.PurposeGuardian || second.CacheReadTokens != 768 {
		t.Fatalf("second guardian usage = %+v", second)
	}
}

func TestPiExtractorIgnoresEntriesThatAreNotPricedTraffic(t *testing.T) {
	extractor := NewUsageExtractor("pi")
	lines := map[string]string{
		"user message":       `{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		"tool result":        `{"type":"message","id":"t1","message":{"role":"toolResult","content":[{"type":"text","text":"ok"}]}}`,
		"model change":       `{"type":"model_change","id":"m1","provider":"opencode-go","modelId":"deepseek-v4-flash"}`,
		"other custom entry": `{"type":"custom","id":"x1","customType":"attn-note","data":{"text":"ignored"}}`,
		"assistant so far":   `{"type":"message","id":"a1","message":{"role":"assistant","model":"deepseek-v4-flash"}}`,
		"empty guardian":     `{"type":"custom","id":"g1","customType":"attn-guardian-usage","data":{"model":"deepseek-v4-flash","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cost":{"total":0}}}}`,
	}
	for name, line := range lines {
		if usage, ok := extractor.Observe([]byte(line), "ignored"); ok {
			t.Fatalf("%s produced usage: %+v", name, usage)
		}
	}
}
