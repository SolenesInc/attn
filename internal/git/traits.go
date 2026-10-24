package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type RepoTraits struct {
	HasAgentsMD bool
	IsWorktree  bool
	HasRemote   bool
}

func ReadRepoTraits(dir string) (RepoTraits, bool) {
	root, gitDir, ok := locateGitDir(context.Background(), dir)
	if !ok {
		return RepoTraits{}, false
	}
	commonDir := commonGitDir(gitDir)
	_, agentsErr := os.Stat(filepath.Join(root, "AGENTS.md"))
	return RepoTraits{
		HasAgentsMD: agentsErr == nil,
		IsWorktree:  commonDir != gitDir,
		HasRemote:   configDeclaresRemote(commonDir),
	}, true
}

func configDeclaresRemote(gitDir string) bool {
	content, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "[remote ") {
			return true
		}
	}
	return false
}
