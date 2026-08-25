package hooks

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Coverage is deliberately partial: a file rewritten through the shell (`sed -i`, a
// heredoc) arrives as a Bash call with no attributable path; deletions are skipped.
func MarkdownEdits(toolName string, toolInput json.RawMessage, cwd string) []string {
	var paths []string
	switch toolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		var input struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		}
		if err := json.Unmarshal(toolInput, &input); err != nil {
			return nil
		}
		paths = []string{input.FilePath, input.NotebookPath}
	case "apply_patch":
		var input struct {
			Command string `json:"command"`
			Input   string `json:"input"`
		}
		if err := json.Unmarshal(toolInput, &input); err != nil {
			return nil
		}
		patch := input.Command
		if strings.TrimSpace(patch) == "" {
			patch = input.Input
		}
		paths = applyPatchTargets(patch)
	default:
		return nil
	}

	var edited []string
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || !isMarkdownPath(path) {
			continue
		}
		if !filepath.IsAbs(path) {
			if cwd == "" {
				continue
			}
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		edited = append(edited, path)
	}
	return edited
}

// A rename is reported at its destination, which is the file that now exists.
func applyPatchTargets(patch string) []string {
	var targets []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "*** Add File: "), strings.HasPrefix(line, "*** Update File: "):
			targets = append(targets, strings.TrimSpace(line[strings.Index(line, ": ")+2:]))
		case strings.HasPrefix(line, "*** Move to: "):
			// A move follows the "Update File" header for its source, which no
			// longer exists once the patch applies.
			if len(targets) > 0 {
				targets = targets[:len(targets)-1]
			}
			targets = append(targets, strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: ")))
		}
	}
	return targets
}

func isMarkdownPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}
