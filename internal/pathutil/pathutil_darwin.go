//go:build darwin

package pathutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A GUI app on macOS starts with a minimal PATH, so subprocesses lose tools
// like 'gh' unless this ran first.
func EnsureGUIPath() error {
	currentPath := os.Getenv("PATH")

	if helperPath := runPathHelper(); helperPath != "" {
		currentPath = mergePaths(currentPath, helperPath)
	}

	commonPaths := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
	}
	if home, err := os.UserHomeDir(); err == nil {
		commonPaths = append(commonPaths, filepath.Join(home, ".local", "bin"))
	}

	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			currentPath = mergePaths(currentPath, p)
		}
	}

	return os.Setenv("PATH", currentPath)
}

func runPathHelper() string {
	cmd := exec.Command("/usr/libexec/path_helper", "-s")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return extractPathFromShellOutput(string(output))
}

// extractPathFromShellOutput parses `path_helper -s` output: PATH="..."; export PATH;
func extractPathFromShellOutput(output string) string {
	const prefix = "PATH=\""
	start := strings.Index(output, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(output[start:], "\"")
	if end == -1 {
		return ""
	}
	return output[start : start+end]
}
