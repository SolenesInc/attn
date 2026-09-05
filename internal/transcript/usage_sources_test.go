package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeUsageSourcesStayInsideTheNativeSubagentDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "conversation.jsonl")
	if err := os.WriteFile(root, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root[:len(root)-len(".jsonl")], "subagents")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "agent-a.jsonl")
	for path, body := range map[string]string{
		child:                                   "{}\n",
		filepath.Join(dir, "notes.txt"):         "{}\n",
		filepath.Join(dir, "nested", "x.jsonl"): "{}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sources, err := NewClaudeUsageSourceResolver(root).Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || !sources[0].Root || sources[1].Path != child {
		t.Fatalf("Claude sources = %+v", sources)
	}
}

func TestCodexUsageSourcesFollowOnlyThreadSpawnDescendants(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "09", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.jsonl")
	writeSourceRecord(t, root, codexSourceMeta("root", `"cli"`))
	writeSourceRecord(t, filepath.Join(dir, "child.jsonl"), codexSourceMeta("child", codexSourceParent("root")))
	writeSourceRecord(t, filepath.Join(dir, "grandchild.jsonl"), codexSourceMeta("grandchild", codexSourceParent("child")))
	writeSourceRecord(t, filepath.Join(dir, "guardian.jsonl"), codexSourceMeta("guardian", `{"subagent":{"other":"guardian"}}`))
	writeSourceRecord(t, filepath.Join(dir, "unrelated.jsonl"), codexSourceMeta("unrelated", codexSourceParent("someone-else")))

	sources, err := NewCodexUsageSourceResolver(root).Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 3 || sources[0].Path != root || sources[1].ID != "child" || sources[2].ID != "grandchild" {
		t.Fatalf("Codex sources = %+v", sources)
	}
}

func TestCodexUsageSourceRetriesAPartialMetadataRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "09", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	writeSourceRecord(t, root, codexSourceMeta("root", `"cli"`))
	if err := os.WriteFile(child, []byte(codexSourceMeta("child", codexSourceParent("root"))), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewCodexUsageSourceResolver(root)
	sources, err := resolver.Discover()
	if err != nil || len(sources) != 1 {
		t.Fatalf("partial discovery = %+v, %v", sources, err)
	}
	file, err := os.OpenFile(child, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("\n")
	_ = file.Close()
	sources, err = resolver.Discover()
	if err != nil || len(sources) != 2 || sources[1].ID != "child" {
		t.Fatalf("completed discovery = %+v, %v", sources, err)
	}
}

func writeSourceRecord(t *testing.T, path, line string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func codexSourceMeta(id, source string) string {
	return `{"type":"session_meta","payload":{"id":"` + id + `","source":` + source + `}}`
}

func codexSourceParent(parent string) string {
	return `{"subagent":{"thread_spawn":{"parent_thread_id":"` + parent + `"}}}`
}
