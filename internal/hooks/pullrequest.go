package hooks

import (
	"encoding/json"
	"regexp"
	"strings"
)

var ghPRCreatePattern = regexp.MustCompile(`\bgh\s+pr\s+create\b`)

var pullRequestURLPattern = regexp.MustCompile(`https://[A-Za-z0-9][A-Za-z0-9.-]*/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/pull/[0-9]+`)

// PullRequestCreated returns the URLs a `gh pr create` printed, in order.
func PullRequestCreated(toolName string, toolInput, toolResponse json.RawMessage) []string {
	if !isShellTool(toolName) {
		return nil
	}
	if !ghPRCreatePattern.MatchString(shellCommand(toolInput)) {
		return nil
	}

	// Every harness shapes tool_response differently (an object with stdout, a bare
	// string, a list of blocks), and a pull request URL survives JSON encoding intact.
	var urls []string
	seen := map[string]bool{}
	for _, url := range pullRequestURLPattern.FindAllString(string(toolResponse), -1) {
		if seen[url] {
			continue
		}
		seen[url] = true
		urls = append(urls, url)
	}
	return urls
}

func isShellTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "bash" || name == "shell" || name == "exec_command" || strings.HasSuffix(name, ".exec_command")
}

func shellCommand(toolInput json.RawMessage) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(toolInput, &object) != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd"} {
		raw, ok := object[key]
		if !ok {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
		// Codex's exec form is an argv list: `["bash", "-lc", "gh pr create …"]`.
		var argv []string
		if json.Unmarshal(raw, &argv) == nil {
			return strings.Join(argv, " ")
		}
	}
	return ""
}
