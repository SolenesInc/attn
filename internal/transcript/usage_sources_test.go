package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/sessioncost"
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

func TestCodexUsageSourcesKeepTheirIdentityAcrossArchiveLocations(t *testing.T) {
	for _, rootArchived := range []bool{false, true} {
		for _, childArchived := range []bool{false, true} {
			for _, boundArchived := range []bool{false, true} {
				t.Run(fmt.Sprintf("root=%t/child=%t/binding=%t", rootArchived, childArchived, boundArchived), func(t *testing.T) {
					home := t.TempDir()
					live := filepath.Join(home, "sessions", "2026", "10", "02")
					archived := filepath.Join(home, "archived_sessions")
					for _, dir := range []string{live, archived} {
						if err := os.MkdirAll(dir, 0o755); err != nil {
							t.Fatal(err)
						}
					}
					rootName := "rollout-2026-10-02T12-00-00-root.jsonl"
					rootDir, childDir := live, live
					if rootArchived {
						rootDir = archived
					}
					if childArchived {
						childDir = archived
					}
					root := filepath.Join(rootDir, rootName)
					child := filepath.Join(childDir, "rollout-2026-10-02T12-00-00-child.jsonl")
					writeSourceRecord(t, root, codexSourceMeta("root", `"cli"`))
					writeSourceRecord(t, child, codexSourceMeta("child", codexSourceParent("root")))
					binding := filepath.Join(live, rootName)
					if boundArchived {
						binding = filepath.Join(archived, rootName)
					}
					sources, err := NewCodexUsageSourceResolver(binding).Discover()
					if err != nil || len(sources) != 2 || sources[0].ID != filepath.Join(live, rootName) || sources[0].Path != root || sources[1].ID != "child" || sources[1].Path != child {
						t.Fatalf("sources = %+v (%v), want stable live root identity and native child in either location", sources, err)
					}
				})
			}
		}
	}
}

func TestCodexUsageSourcesFollowSessionDescendantsAndGuardians(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "09", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.jsonl")
	writeSourceRecord(t, root, codexSourceMeta("root", `"cli"`))
	writeSourceRecord(t, filepath.Join(dir, "child.jsonl"), codexSourceMeta("child", codexSourceParent("root")))
	writeSourceRecord(t, filepath.Join(dir, "grandchild.jsonl"), codexSourceMeta("grandchild", codexSourceParent("child")))
	writeSourceRecord(t, filepath.Join(dir, "guardian.jsonl"), codexGuardianMeta("guardian", "root"))
	writeSourceRecord(t, filepath.Join(dir, "other-guardian.jsonl"), codexGuardianMeta("other-guardian", "someone-else"))
	writeSourceRecord(t, filepath.Join(dir, "unrelated.jsonl"), codexSourceMeta("unrelated", codexSourceParent("someone-else")))

	sources, err := NewCodexUsageSourceResolver(root).Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 4 || sources[0].Path != root || sources[1].ID != "child" ||
		sources[2].ID != "grandchild" || sources[3].ID != "guardian" || sources[3].Purpose != sessioncost.PurposeGuardian {
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

func TestCodexUsageSourcesDiscoverArchivedGrandchildrenAfterPartialParentCompletes(t *testing.T) {
	for _, archivedParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("parentArchived=%t", archivedParent), func(t *testing.T) {
			home := t.TempDir()
			live := filepath.Join(home, "sessions", "2026", "10", "02")
			archive := filepath.Join(home, "archived_sessions")
			for _, dir := range []string{live, archive} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			root := filepath.Join(live, "root.jsonl")
			parent := filepath.Join(live, "parent.jsonl")
			if archivedParent {
				parent = filepath.Join(archive, "parent.jsonl")
			}
			grandchild := filepath.Join(archive, "grandchild.jsonl")
			writeSourceRecord(t, root, codexSourceMeta("root", `"cli"`))
			if err := os.WriteFile(parent, []byte(codexSourceMeta("parent", codexSourceParent("root"))), 0o600); err != nil {
				t.Fatal(err)
			}
			writeSourceRecord(t, grandchild, codexSourceMeta("grandchild", codexSourceParent("parent")))
			resolver := NewCodexUsageSourceResolver(root)
			sources, err := resolver.Discover()
			if err != nil || len(sources) != 1 {
				t.Fatalf("partial lineage = %+v, %v", sources, err)
			}
			file, err := os.OpenFile(parent, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.WriteString("\n")
			_ = file.Close()
			if err != nil {
				t.Fatal(err)
			}
			sources, err = resolver.Discover()
			if err != nil || len(sources) != 3 || sources[1].ID != "parent" || sources[2].ID != "grandchild" {
				t.Fatalf("complete lineage = %+v, %v", sources, err)
			}
		})
	}
}

func TestCodexUsageSourcesFollowArchiveMembershipChanges(t *testing.T) {
	home := t.TempDir()
	live := filepath.Join(home, "sessions", "2026", "10", "02")
	archive := filepath.Join(home, "archived_sessions")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(live, "root.jsonl")
	writeSourceRecord(t, root, codexSourceMeta("root", `"cli"`))
	resolver := NewCodexUsageSourceResolver(root)
	if sources, err := resolver.Discover(); err != nil || len(sources) != 1 {
		t.Fatalf("initial = %+v, %v", sources, err)
	}
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(archive, "child.jsonl")
	writeSourceRecord(t, child, codexSourceMeta("child", codexSourceParent("root")))
	for _, path := range []string{child, filepath.Join(live, "child.jsonl"), child} {
		if path != child {
			if err := os.Rename(child, path); err != nil {
				t.Fatal(err)
			}
		} else if _, err := os.Stat(child); os.IsNotExist(err) {
			if err := os.Rename(filepath.Join(live, "child.jsonl"), child); err != nil {
				t.Fatal(err)
			}
		}
		sources, err := resolver.Discover()
		if err != nil || len(sources) != 2 || sources[1].ID != "child" || sources[1].Path != path {
			t.Fatalf("moved = %+v, %v", sources, err)
		}
	}
	writeSourceRecord(t, filepath.Join(archive, "new-child.jsonl"), codexSourceMeta("new-child", codexSourceParent("root")))
	if sources, err := resolver.Discover(); err != nil || len(sources) != 3 {
		t.Fatalf("new archive child = %+v, %v", sources, err)
	}
}

func TestCodexUsageSourcesRetryAnArchiveReadFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the filesystem permission failure")
	}
	home := t.TempDir()
	live := filepath.Join(home, "sessions", "2026", "10", "02")
	archive := filepath.Join(home, "archived_sessions")
	for _, dir := range []string{live, archive} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(live, "root.jsonl")
	child := filepath.Join(archive, "child.jsonl")
	writeSourceRecord(t, root, codexSourceMeta("root", `"cli"`))
	writeSourceRecord(t, child, codexSourceMeta("child", codexSourceParent("root")))
	if err := os.Chmod(child, 0); err != nil {
		t.Fatal(err)
	}
	resolver := NewCodexUsageSourceResolver(root)
	if _, err := resolver.Discover(); !os.IsPermission(err) {
		t.Fatalf("unreadable archive error = %v", err)
	}
	if err := os.Chmod(child, 0o600); err != nil {
		t.Fatal(err)
	}
	if sources, err := resolver.Discover(); err != nil || len(sources) != 2 || sources[1].ID != "child" {
		t.Fatalf("recovered read = %+v, %v", sources, err)
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

func codexGuardianMeta(id, parent string) string {
	return `{"type":"session_meta","payload":{"id":"` + id + `","parent_thread_id":"` + parent +
		`","source":{"subagent":{"other":"guardian"}}}}`
}

func codexSourceParent(parent string) string {
	return `{"subagent":{"thread_spawn":{"parent_thread_id":"` + parent + `"}}}`
}
