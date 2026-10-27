package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type RepoTraits struct {
	HasAgentsMD bool
	HasClaudeMD bool
	IsWorktree  bool
	HasRemote   bool
}

func ReadRepoTraits(dir string) (RepoTraits, bool) {
	root, gitDir, ok := locateGitDir(context.Background(), dir)
	if !ok {
		return RepoTraits{}, false
	}
	commonDir := commonGitDir(gitDir)
	return RepoTraits{
		HasAgentsMD: fileExists(filepath.Join(root, "AGENTS.md")),
		HasClaudeMD: fileExists(filepath.Join(root, "CLAUDE.md")),
		IsWorktree:  commonDir != gitDir,
		HasRemote:   configDeclaresRemote(commonDir),
	}, true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
