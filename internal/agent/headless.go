package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/launchenv"
	"github.com/victorarias/attn/internal/toolhome"
)

const DefaultContextWindowCap = 128000

var headlessContextWindowCap atomic.Int64

func SetHeadlessContextWindowCap(tokens int) {
	if tokens < 0 {
		tokens = 0
	}
	headlessContextWindowCap.Store(int64(tokens))
}

func HeadlessContextWindowCap() int {
	return int(headlessContextWindowCap.Load())
}

const headlessOutputLimit = 1 << 20 // 1 MiB

type boundedHeadlessOutput struct {
	bytes.Buffer
}

func (b *boundedHeadlessOutput) Write(p []byte) (int, error) {
	originalLength := len(p)
	if remaining := headlessOutputLimit - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		if _, err := b.Buffer.Write(p); err != nil {
			return 0, err
		}
	}
	return originalLength, nil
}

const headlessFailureOutputLimit = 4 << 10 // 4 KiB per stream

// The error STRING stays free of child output: it travels into keeper/journal
// surfaces that must not echo workspace content.
func runHeadlessCommand(
	ctx context.Context,
	executable string,
	args []string,
	workDir string,
	provider string,
) (HeadlessTaskResult, []byte, error) {
	// Last line of defense: every caller checks the switch first, so reaching
	// this is a caller that forgot.
	if !headless.Enabled() {
		return HeadlessTaskResult{}, nil, headless.Refusal(provider + " headless task")
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	if dir := strings.TrimSpace(workDir); dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = headlessEnvironment(provider)
	var stdout, stderr boundedHeadlessOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		diagnostics := classifyHeadlessFailure(stdout.String() + "\n" + stderr.String())
		return HeadlessTaskResult{
			Diagnostics:   diagnostics,
			FailureOutput: headlessFailureOutput(stdout.String(), stderr.String()),
		}, stdout.Bytes(), fmt.Errorf("%s: %w", diagnostics, err)
	}
	return HeadlessTaskResult{}, stdout.Bytes(), nil
}

func headlessFailureOutput(stdout, stderr string) string {
	var parts []string
	if s := strings.TrimSpace(stderr); s != "" {
		parts = append(parts, "stderr: "+tailString(s, headlessFailureOutputLimit))
	}
	if s := strings.TrimSpace(stdout); s != "" {
		parts = append(parts, "stdout: "+tailString(s, headlessFailureOutputLimit))
	}
	return strings.Join(parts, "\n")
}

func tailString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return "…(truncated) " + s[len(s)-limit:]
}

func headlessToolNames(toolName string) []string {
	if name := strings.TrimSpace(toolName); name != "" {
		return []string{name}
	}
	return []string{"read_context", "replace_context"}
}

func headlessTempDir(workDir string) string {
	if dir := strings.TrimSpace(workDir); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return os.TempDir()
}

func headlessEnvironment(provider string) []string {
	allowedExact := map[string]bool{
		"HOME":                true,
		"PATH":                true,
		"SHELL":               true,
		"USER":                true,
		"LOGNAME":             true,
		"TMPDIR":              true,
		"TMP":                 true,
		"TEMP":                true,
		"LANG":                true,
		"TERM":                true,
		"COLORTERM":           true,
		"SSL_CERT_FILE":       true,
		"SSL_CERT_DIR":        true,
		"NODE_EXTRA_CA_CERTS": true,
		"HTTP_PROXY":          true,
		"HTTPS_PROXY":         true,
		"ALL_PROXY":           true,
		"NO_PROXY":            true,
		"http_proxy":          true,
		"https_proxy":         true,
		"all_proxy":           true,
		"no_proxy":            true,
	}
	allowedPrefixes := []string{"LC_"}
	switch provider {
	case "codex":
		for _, name := range []string{"CODEX_HOME", "CODEX_ACCESS_TOKEN", "CODEX_API_KEY"} {
			allowedExact[name] = true
		}
		allowedPrefixes = append(allowedPrefixes, "OPENAI_", "AZURE_OPENAI_", "AWS_", "GOOGLE_")
	case "claude":
		allowedPrefixes = append(allowedPrefixes, "ANTHROPIC_", "CLAUDE_CODE_USE_", "AWS_", "GOOGLE_", "AZURE_")
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		allowed := allowedExact[name]
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(name, prefix) {
				allowed = true
				break
			}
		}
		if allowed {
			env = append(env, entry)
		}
	}
	if provider == "codex" && strings.TrimSpace(os.Getenv("CODEX_HOME")) == "" {
		if homeDir, err := toolhome.Dir(); err == nil && strings.TrimSpace(homeDir) != "" {
			env = append(env, "CODEX_HOME="+filepath.Join(homeDir, ".codex"))
		}
	}
	if provider == "claude" {
		env = append(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
		// Injected, not inherited: the allowlist above drops CLAUDE_CODE_* and the
		// daemon scrubs this var from its own environment.
		if window := HeadlessContextWindowCap(); window > 0 {
			env = append(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+strconv.Itoa(window))
		}
	}
	return launchenv.WithActiveAttnFirst(env, launchenv.ActiveAttnExecutable())
}

func environmentContains(env []string, name string) bool {
	prefix := name + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func classifyHeadlessFailure(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "authentication_failed"),
		strings.Contains(lower, "not logged in"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "api key"):
		return "headless agent authentication failed"
	case strings.Contains(lower, "model") &&
		(strings.Contains(lower, "not found") ||
			strings.Contains(lower, "invalid") ||
			strings.Contains(lower, "unavailable") ||
			strings.Contains(lower, "unsupported")):
		return "headless agent model is invalid or unavailable"
	case strings.Contains(lower, "mcp"),
		strings.Contains(lower, "tool server"),
		strings.Contains(lower, "server failed"):
		return "headless agent MCP tool server failed"
	default:
		return "headless agent process failed"
	}
}
