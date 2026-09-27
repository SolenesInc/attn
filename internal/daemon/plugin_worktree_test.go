package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/git"
)

func initProviderTestRepo(t *testing.T) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	mainDir := filepath.Join(tmpDir, "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatalf("mkdir main repo: %v", err)
	}
	runGitDaemon(t, mainDir, "init")
	runGitDaemon(t, mainDir, "commit", "--allow-empty", "-m", "init")
	return tmpDir, git.NewClient().ResolveMainRepoPath(context.Background(), mainDir)
}
