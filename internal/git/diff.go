package git

import (
	"sort"
	"strconv"
	"strings"
)

type DiffFileInfo struct {
	Path           string `json:"path"`
	Status         string `json:"status"` // "added", "modified", "deleted", "renamed"
	OldPath        string `json:"old_path,omitempty"`
	Additions      int    `json:"additions,omitempty"`
	Deletions      int    `json:"deletions,omitempty"`
	HasUncommitted bool   `json:"has_uncommitted,omitempty"`
}

func GetBranchDiffFiles(repoDir, baseRef string) ([]DiffFileInfo, error) {
	fileMap := make(map[string]*DiffFileInfo)

	statusOut, err := runGitOutput(OpDiff, repoDir, "diff", "--name-status", baseRef+"...HEAD")
	if err != nil {
		// A missing baseRef or merge-base is fine; fall back to uncommitted changes.
		statusOut = []byte{}
	}

	if len(statusOut) > 0 {
		lines := strings.Split(strings.TrimSpace(string(statusOut)), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 2 {
				continue
			}

			statusCode := parts[0]
			path := parts[len(parts)-1]

			info := &DiffFileInfo{
				Path:   path,
				Status: parseGitStatus(statusCode),
			}

			if strings.HasPrefix(statusCode, "R") && len(parts) >= 3 {
				info.OldPath = parts[1]
			}

			fileMap[path] = info
		}
	}

	numstatOut, _ := runGitOutput(OpDiff, repoDir, "diff", "--numstat", baseRef+"...HEAD") // Ignore errors, stats are optional

	if len(numstatOut) > 0 {
		lines := strings.Split(strings.TrimSpace(string(numstatOut)), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 3 {
				continue
			}

			path := parts[2]
			if strings.Contains(path, " => ") {
				path = extractRenamePath(path)
			}

			if info, ok := fileMap[path]; ok {
				info.Additions, _ = strconv.Atoi(parts[0])
				info.Deletions, _ = strconv.Atoi(parts[1])
			}
		}
	}

	// --untracked-files=all expands a brand-new untracked directory into its files;
	// without it git collapses the folder into one "?? dir/" entry, hiding every file.
	porcelainOut, _ := runGitOutput(OpStatus, repoDir, "status", "--porcelain", "--untracked-files=all")

	uncommittedFiles := make(map[string]bool)
	if len(porcelainOut) > 0 {
		// Don't TrimSpace the whole output: the leading space is part of the status code.
		lines := strings.Split(strings.TrimRight(string(porcelainOut), "\n"), "\n")
		for _, line := range lines {
			if len(line) < 4 { // Need at least "XY " + 1 char path
				continue
			}
			// Format: "XY path" where X=staged, Y=unstaged, followed by single space
			statusXY := line[:2]
			path := line[3:] // Don't TrimSpace - path may have intentional spaces

			// Handle renames in porcelain: "R  old -> new"
			if strings.Contains(path, " -> ") {
				parts := strings.Split(path, " -> ")
				if len(parts) == 2 {
					path = parts[1]
				}
			}

			uncommittedFiles[path] = true

			if info, ok := fileMap[path]; ok {
				info.HasUncommitted = true
			} else {
				info := &DiffFileInfo{
					Path:           path,
					Status:         parseGitPorcelainStatus(statusXY),
					HasUncommitted: true,
				}
				fileMap[path] = info
			}
		}
	}

	if len(uncommittedFiles) > 0 {
		unstatsOut, _ := runGitOutput(OpDiff, repoDir, "diff", "--numstat")

		if len(unstatsOut) > 0 {
			lines := strings.Split(strings.TrimSpace(string(unstatsOut)), "\n")
			for _, line := range lines {
				if line == "" {
					continue
				}
				parts := strings.Split(line, "\t")
				if len(parts) < 3 {
					continue
				}
				path := parts[2]
				if info, ok := fileMap[path]; ok && info.Additions == 0 && info.Deletions == 0 {
					info.Additions, _ = strconv.Atoi(parts[0])
					info.Deletions, _ = strconv.Atoi(parts[1])
				}
			}
		}

		stagedStatsOut, _ := runGitOutput(OpDiff, repoDir, "diff", "--numstat", "--cached")

		if len(stagedStatsOut) > 0 {
			lines := strings.Split(strings.TrimSpace(string(stagedStatsOut)), "\n")
			for _, line := range lines {
				if line == "" {
					continue
				}
				parts := strings.Split(line, "\t")
				if len(parts) < 3 {
					continue
				}
				path := parts[2]
				if info, ok := fileMap[path]; ok {
					adds, _ := strconv.Atoi(parts[0])
					dels, _ := strconv.Atoi(parts[1])
					info.Additions += adds
					info.Deletions += dels
				}
			}
		}
	}

	result := make([]DiffFileInfo, 0, len(fileMap))
	for _, info := range fileMap {
		result = append(result, *info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})

	return result, nil
}

func parseGitStatus(code string) string {
	if len(code) > 0 {
		switch code[0] {
		case 'A':
			return "added"
		case 'M':
			return "modified"
		case 'D':
			return "deleted"
		case 'R':
			return "renamed"
		case 'C':
			return "copied"
		case 'T':
			return "typechange"
		}
	}
	return "modified"
}

func parseGitPorcelainStatus(xy string) string {
	if len(xy) < 2 {
		return "modified"
	}
	x, y := xy[0], xy[1]

	if x == '?' && y == '?' {
		return "untracked"
	}
	if x == 'A' || y == 'A' {
		return "added"
	}
	if x == 'D' || y == 'D' {
		return "deleted"
	}
	if x == 'R' || y == 'R' {
		return "renamed"
	}
	return "modified"
}

// Handles git numstat rename shapes: "old.go => new.go", "{old => new}/file.go",
// and "dir/{old.go => new.go}".
func extractRenamePath(path string) string {
	if !strings.Contains(path, "{") {
		parts := strings.Split(path, " => ")
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
		return path
	}

	start := strings.Index(path, "{")
	end := strings.Index(path, "}")
	if start >= 0 && end > start {
		braceContent := path[start+1 : end]
		parts := strings.Split(braceContent, " => ")
		if len(parts) == 2 {
			prefix := path[:start]
			suffix := path[end+1:]
			return prefix + strings.TrimSpace(parts[1]) + suffix
		}
	}

	return path
}
