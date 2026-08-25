package daemon

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var updateWireGoldens = flag.Bool("update", false, "update wire-trace golden files")

var (
	wireTimestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
	wireUUIDPattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func wireRecorder(d *Daemon) *WireTrace {
	trace := &WireTrace{}
	d.wsHub.wireTap = trace.record
	return trace
}

func normalizeWirePayload(payload []byte, paths map[string]string) string {
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return fmt.Sprintf("<unparseable: %s>", string(payload))
	}
	normalized := normalizeWireValue("", decoded, paths)
	// HTML escaping off: <tmp> in a golden is unreadable in a diff.
	var buf strings.Builder
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(normalized); err != nil {
		return fmt.Sprintf("<unrenderable: %v>", err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func normalizeWireValue(key string, v any, paths map[string]string) any {
	if isEnvironmentProbedKey(key) {
		return "<probed>"
	}
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, child := range typed {
			out[k] = normalizeWireValue(k, child, paths)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = normalizeWireValue(key, child, paths)
		}
		return out
	case string:
		return normalizeWireString(typed, paths)
	case float64:
		if strings.HasSuffix(key, "_ms") || strings.HasSuffix(key, "_seconds") {
			return "<duration>"
		}
		return typed
	default:
		return v
	}
}

// Agent availability is a PATH lookup, so pinning it would make the golden a statement
// about the host. Matched by suffix so a new driver's key does not depend on the runner.
func isEnvironmentProbedKey(key string) bool {
	return strings.HasSuffix(key, "_available")
}

func normalizeWireString(s string, paths map[string]string) string {
	if wireTimestampPattern.MatchString(s) {
		return "<timestamp>"
	}
	if wireUUIDPattern.MatchString(s) {
		return "<uuid>"
	}
	// Longest first, so a nested temp dir is not half-replaced by its parent.
	for _, path := range sortedPathsLongestFirst(paths) {
		s = strings.ReplaceAll(s, path, paths[path])
	}
	if wireHomeDir != "" {
		s = strings.ReplaceAll(s, wireHomeDir, "<home>")
	}
	return s
}

// Paths the daemon derives from the host's home — the notebook root, say — would pin
// the golden to whoever ran it, and tests must not redirect HOME.
var wireHomeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return strings.TrimRight(home, string(filepath.Separator))
}()

func sortedPathsLongestFirst(paths map[string]string) []string {
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

func renderWireTrace(trace *WireTrace, paths map[string]string) string {
	payloads := trace.Payloads()
	names := trace.EventNames()
	var b strings.Builder
	for i, payload := range payloads {
		fmt.Fprintf(&b, "--- %02d %s\n", i+1, names[i])
		b.WriteString(normalizeWirePayload(payload, paths))
		b.WriteString("\n")
	}
	if len(payloads) == 0 {
		b.WriteString("(no wire traffic)\n")
	}
	return b.String()
}

func assertWireGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "wire", name+".golden")
	if *updateWireGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (regenerate with -update): %v", path, err)
	}
	if got == string(want) {
		return
	}
	t.Errorf("wire trace differs from %s — clients would receive different bytes.\n%s",
		path, firstWireDiff(string(want), got))
}

func firstWireDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	limit := len(wantLines)
	if len(gotLines) > limit {
		limit = len(gotLines)
	}
	at := func(lines []string, i int) string {
		if i < len(lines) {
			return lines[i]
		}
		return "<end of trace>"
	}
	for i := 0; i < limit; i++ {
		if at(wantLines, i) == at(gotLines, i) {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "first difference at line %d:\n", i+1)
		for j := max(0, i-3); j < min(limit, i+4); j++ {
			marker := "  "
			if j == i {
				marker = "> "
			}
			fmt.Fprintf(&b, "%sgolden: %s\n%s  live: %s\n", marker, at(wantLines, j), marker, at(gotLines, j))
		}
		return b.String()
	}
	return "traces differ only in trailing whitespace"
}
