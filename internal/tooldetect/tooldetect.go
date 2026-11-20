package tooldetect

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

type toolInput map[string]json.RawMessage

func (in toolInput) text(key string) string {
	var value string
	_ = json.Unmarshal(in[key], &value)
	return value
}

func MarkdownEdits(name string, raw json.RawMessage, cwd string) []string {
	var input toolInput
	if json.Unmarshal(raw, &input) != nil {
		return nil
	}
	var paths []string
	switch name {
	case "Edit", "Write", "MultiEdit", "NotebookEdit", "edit", "write":
		paths = []string{input.text("file_path"), input.text("notebook_path"), input.text("path")}
	case "apply_patch":
		for _, key := range []string{"patch", "command", "input"} {
			if patch := input.text(key); strings.TrimSpace(patch) != "" {
				paths = patchTargets(patch)
				break
			}
		}
	}
	var markdown []string
	for _, path := range paths {
		switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
		case ".md", ".markdown":
			markdown = append(markdown, path)
		}
	}
	return resolveToolPaths(markdown, cwd)
}

func patchTargets(patch string) []string {
	var targets []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "*** Add File: "); ok {
			targets = append(targets, strings.TrimSpace(rest))
		} else if rest, ok := strings.CutPrefix(line, "*** Update File: "); ok {
			targets = append(targets, strings.TrimSpace(rest))
		} else if rest, ok := strings.CutPrefix(line, "*** Move to: "); ok {
			targets = append(targets[:max(len(targets)-1, 0)], strings.TrimSpace(rest))
		}
	}
	return targets
}

func resolveToolPaths(paths []string, cwd string) []string {
	seen := map[string]bool{}
	var resolved []string
	for _, path := range paths {
		path = strings.TrimSpace(path)
		switch {
		case path == "":
			continue
		case filepath.IsAbs(path):
			path = filepath.Clean(path)
		case cwd == "":
			continue
		default:
			path = filepath.Join(cwd, path)
		}
		if !seen[path] {
			seen[path] = true
			resolved = append(resolved, path)
		}
	}
	return resolved
}

func PullRequestsCreated(name string, raw json.RawMessage, output string) []string {
	if !isShellTool(name) || !hasGHPRCreate(shellCommand(raw)) {
		return nil
	}
	seen := map[string]bool{}
	var urls []string
	for from := 0; ; {
		found := strings.Index(output[from:], "https://")
		if found < 0 {
			return urls
		}
		start := from + found
		end, ok := pullRequestURLEnd(output, start)
		if !ok {
			from = start + 1
			continue
		}
		if url := output[start:end]; !seen[url] {
			seen[url] = true
			urls = append(urls, url)
		}
		from = end
	}
}

func isShellTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "bash" || name == "shell" || name == "exec_command" || strings.HasSuffix(name, ".exec_command")
}

func shellCommand(raw json.RawMessage) string {
	var input toolInput
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd"} {
		var text string
		if json.Unmarshal(input[key], &text) == nil {
			return text
		}
		var parts []string
		if json.Unmarshal(input[key], &parts) == nil {
			return strings.Join(parts, " ")
		}
	}
	return ""
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isSpaceByte(b byte) bool {
	return b == '\t' || b == '\n' || b == 0x0c || b == '\r' || b == ' '
}

func hasGHPRCreate(command string) bool {
	word := func(at int, w string) (int, bool) {
		return at + len(w), strings.HasPrefix(command[at:], w)
	}
	spaces := func(at int) (int, bool) {
		start := at
		for at < len(command) && isSpaceByte(command[at]) {
			at++
		}
		return at, at > start
	}
	for start := range command {
		if start > 0 && isWordByte(command[start-1]) {
			continue
		}
		at, ok := word(start, "gh")
		for _, next := range []string{"pr", "create"} {
			if ok {
				at, ok = spaces(at)
			}
			if ok {
				at, ok = word(at, next)
			}
		}
		if ok && (at == len(command) || !isWordByte(command[at])) {
			return true
		}
	}
	return false
}

func pullRequestURLEnd(text string, start int) (int, bool) {
	run := func(at int, allowed func(byte) bool) int {
		n := 0
		for at+n < len(text) && allowed(text[at+n]) {
			n++
		}
		return n
	}
	literal := func(at int, want string) (int, bool) {
		return at + len(want), strings.HasPrefix(text[min(at, len(text)):], want)
	}
	alnum := func(b byte) bool { return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
	host := func(b byte) bool { return alnum(b) || b == '.' || b == '-' }
	segment := func(b byte) bool { return alnum(b) || b == '.' || b == '_' || b == '-' }
	at := start + len("https://")
	if at >= len(text) || !alnum(text[at]) {
		return 0, false
	}
	at, ok := literal(at+1+run(at+1, host), "/")
	for range 2 {
		if !ok {
			return 0, false
		}
		length := run(at, segment)
		if length == 0 {
			return 0, false
		}
		at, ok = literal(at+length, "/")
	}
	if ok {
		at, ok = literal(at, "pull/")
	}
	digits := run(at, func(b byte) bool { return b >= '0' && b <= '9' })
	return at + digits, ok && digits > 0
}
