package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFileDiffBetweenPinnedRefsIgnoresTheWorkingTree(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	v1 := commitFile(t, repo, "src/file.ts", "v1")
	v2 := commitFile(t, repo, "src/file.ts", "v2")
	if err := os.WriteFile(filepath.Join(repo, "src/file.ts"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		name, base, head, original, modified string
	}{
		{"between two revisions", v1, v2, "v1", "v2"},
		{"where the file is absent", before, before, "", ""},
	} {
		requestID := "diff " + row.name
		result := testworld.Request(app, protocol.GetFileDiffMessage{
			Cmd: protocol.CmdGetFileDiff, Directory: repo, Path: "src/file.ts",
			BaseRef: protocol.Ptr(row.base), HeadRef: protocol.Ptr(row.head), RequestID: protocol.Ptr(requestID),
		}, protocol.EventFileDiffResult, func(r protocol.FileDiffResultMessage) bool { return protocol.Deref(r.RequestID) == requestID })
		if !result.Success || result.Original != row.original || result.Modified != row.modified {
			t.Errorf("the diff %s = %q -> %q (%s); want %q -> %q", row.name, result.Original, result.Modified, protocol.Deref(result.Error), row.original, row.modified)
		}
	}
}
