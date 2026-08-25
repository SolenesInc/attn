package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

type gitStatusMode string

const (
	gitStatusModeFull        gitStatusMode = "full"
	gitStatusModeTrackedOnly gitStatusMode = "tracked_only"
)

type gitStatusOptions struct {
	mode         gitStatusMode
	fullTimeout  time.Duration
	includeStats bool
}

var runGitStatusCommandForDaemon = runGitStatusCommand

type diffStats struct {
	Additions int
	Deletions int
}

func walkUntrackedDir(repoDir, dirPath string) []protocol.GitFileChange {
	var files []protocol.GitFileChange
	fullPath := filepath.Join(repoDir, dirPath)

	var filePaths []string
	filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			relPath, err := filepath.Rel(repoDir, path)
			if err == nil {
				filePaths = append(filePaths, relPath)
			}
		}
		return nil
	})

	if len(filePaths) == 0 {
		return files
	}

	ignoredOutput, err := attngit.OutputWithStdin(attngit.OpStatus, repoDir, strings.NewReader(strings.Join(filePaths, "\n")), "check-ignore", "--stdin")
	if err != nil && len(ignoredOutput) == 0 && strings.Contains(err.Error(), "timed out") {
		return files
	}

	ignoredSet := make(map[string]bool)
	if len(ignoredOutput) > 0 {
		for _, path := range strings.Split(strings.TrimSpace(string(ignoredOutput)), "\n") {
			if path != "" {
				ignoredSet[path] = true
			}
		}
	}

	for _, path := range filePaths {
		if !ignoredSet[path] {
			files = append(files, protocol.GitFileChange{
				Path:   path,
				Status: "untracked",
			})
		}
	}

	return files
}

func parseGitStatusPorcelain(output string, repoDir string) (staged, unstaged, untracked []protocol.GitFileChange) {
	entries := strings.Split(output, "\x00")

	for _, entry := range entries {
		if len(entry) < 3 {
			continue
		}

		indexStatus := entry[0]
		worktreeStatus := entry[1]
		path := strings.TrimSpace(entry[3:])

		if path == "" {
			continue
		}

		if indexStatus == '?' && worktreeStatus == '?' {
			if strings.HasSuffix(path, "/") {
				files := walkUntrackedDir(repoDir, path)
				untracked = append(untracked, files...)
			} else {
				untracked = append(untracked, protocol.GitFileChange{
					Path:   path,
					Status: "untracked",
				})
			}
			continue
		}

		if indexStatus != ' ' && indexStatus != '?' {
			status := statusCodeToString(indexStatus)
			staged = append(staged, protocol.GitFileChange{
				Path:   path,
				Status: status,
			})
		}

		if worktreeStatus != ' ' && worktreeStatus != '?' {
			status := statusCodeToString(worktreeStatus)
			unstaged = append(unstaged, protocol.GitFileChange{
				Path:   path,
				Status: status,
			})
		}
	}

	return staged, unstaged, untracked
}

func statusCodeToString(code byte) string {
	switch code {
	case 'M':
		return "modified"
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default:
		return "modified"
	}
}

func parseGitDiffNumstat(output string) map[string]diffStats {
	result := make(map[string]diffStats)
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}

		// Binary files show "-" for additions/deletions.
		additions, _ := strconv.ParseInt(parts[0], 10, 64)
		deletions, _ := strconv.ParseInt(parts[1], 10, 64)
		path := parts[2]

		result[path] = diffStats{
			Additions: int(additions),
			Deletions: int(deletions),
		}
	}

	return result
}

func getGitStatus(dir string) (*protocol.GitStatusUpdateMessage, error) {
	return getGitStatusWithOptions(dir, gitStatusOptions{
		mode:         gitStatusModeFull,
		includeStats: true,
	})
}

// Callers use gitCoordinator.Status so concurrent requests for one repo/mode share a git process.
func getGitStatusForSubscription(dir string, mode gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
	return getGitStatusWithOptions(dir, gitStatusOptions{
		mode:         mode,
		fullTimeout:  gitStatusFullBudget,
		includeStats: false,
	})
}

func getGitStatusWithOptions(dir string, opts gitStatusOptions) (*protocol.GitStatusUpdateMessage, error) {
	mode := opts.mode
	if mode == "" {
		mode = gitStatusModeFull
	}

	// The default collapses a brand-new untracked directory into one "?? dir/" line, hiding everything inside.
	statusArgs := []string{"status", "--porcelain", "-z", "--untracked-files=all"}
	if mode == gitStatusModeTrackedOnly {
		statusArgs = []string{"status", "--porcelain", "-z", "--untracked-files=no"}
	}

	statusOutput, err := runGitStatusCommandForDaemon(dir, opts.fullTimeout, statusArgs...)
	limited := mode == gitStatusModeTrackedOnly
	var limitedReason *string
	if limited {
		limitedReason = protocol.Ptr("Untracked files hidden because full git status was slow.")
	}
	if err != nil && mode == gitStatusModeFull && isGitStatusTimeout(err) {
		statusArgs = []string{"status", "--porcelain", "-z", "--untracked-files=no"}
		statusOutput, err = runGitStatusCommandForDaemon(dir, opts.fullTimeout, statusArgs...)
		mode = gitStatusModeTrackedOnly
		limited = true
		limitedReason = protocol.Ptr("Untracked files hidden because full git status was slow.")
	}
	if err != nil {
		errorMessage := "Not a git repository"
		if isGitStatusTimeout(err) {
			errorMessage = "Git status timed out"
		}
		return &protocol.GitStatusUpdateMessage{
			Event:     protocol.EventGitStatusUpdate,
			Directory: dir,
			Error:     protocol.Ptr(errorMessage),
		}, nil
	}

	staged, unstaged, untracked := parseGitStatusPorcelain(string(statusOutput), dir)

	if opts.includeStats && len(unstaged) > 0 {
		numstatOutput, _ := attngit.Output(attngit.OpDiff, dir, "diff", "--numstat")
		stats := parseGitDiffNumstat(string(numstatOutput))

		for i := range unstaged {
			if s, ok := stats[unstaged[i].Path]; ok {
				unstaged[i].Additions = protocol.Ptr(s.Additions)
				unstaged[i].Deletions = protocol.Ptr(s.Deletions)
			}
		}
	}

	if opts.includeStats && len(staged) > 0 {
		numstatOutput, _ := attngit.Output(attngit.OpDiff, dir, "diff", "--numstat", "--cached")
		stats := parseGitDiffNumstat(string(numstatOutput))

		for i := range staged {
			if s, ok := stats[staged[i].Path]; ok {
				staged[i].Additions = protocol.Ptr(s.Additions)
				staged[i].Deletions = protocol.Ptr(s.Deletions)
			}
		}
	}

	return &protocol.GitStatusUpdateMessage{
		Event:         protocol.EventGitStatusUpdate,
		Directory:     dir,
		Staged:        staged,
		Unstaged:      unstaged,
		Untracked:     untracked,
		Mode:          protocol.Ptr(string(mode)),
		Limited:       protocol.Ptr(limited),
		LimitedReason: limitedReason,
	}, nil
}

func runGitStatusCommand(dir string, timeout time.Duration, args ...string) ([]byte, error) {
	if timeout > 0 {
		return attngit.OutputWithTimeout(attngit.OpStatus, timeout, dir, args...)
	}
	return attngit.Output(attngit.OpStatus, dir, args...)
}

func isGitStatusTimeout(err error) bool {
	return err != nil && strings.Contains(err.Error(), "timed out")
}

func hashGitStatus(status *protocol.GitStatusUpdateMessage) string {
	stableStatus := *status
	stableStatus.DurationMs = nil
	data, _ := json.Marshal(stableStatus)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8])
}
