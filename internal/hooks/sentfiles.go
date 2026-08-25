package hooks

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

func SentFiles(toolName string, toolInput json.RawMessage, cwd string) []string {
	if toolName != "SendUserFile" {
		return nil
	}
	var input struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(toolInput, &input); err != nil {
		return nil
	}

	var sent []string
	seen := map[string]bool{}
	for _, path := range input.Files {
		path = strings.TrimSpace(path)
		if path == "" {
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
		sent = append(sent, path)
	}
	return sent
}
