package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/launchenv"
)

const DefaultContextWindowCap = 128000

var contextWindowCap atomic.Int64

func SetContextWindowCap(tokens int) {
	if tokens < 0 {
		tokens = 0
	}
	contextWindowCap.Store(int64(tokens))
}

func ContextWindowCap() int {
	return int(contextWindowCap.Load())
}

const outputLimit = 1 << 20

type BoundedOutput struct {
	bytes.Buffer
}

func (b *BoundedOutput) Write(p []byte) (int, error) {
	originalLength := len(p)
	if remaining := outputLimit - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		if _, err := b.Buffer.Write(p); err != nil {
			return 0, err
		}
	}
	return originalLength, nil
}

const failureOutputLimit = 4 << 10

// A cancelled real Claude run returned 50 ms after its kill. Receipt: seed s-jvn7nq, production-launcher-receipts.txt (2026-11-01).
const PipeDrainLimit = 5 * time.Second

func RunCommand(
	ctx context.Context,
	executable string,
	args []string,
	prompt string,
	workDir string,
	provider string,
	env Env,
	reportedFailure func(stdout []byte) string,
) (TaskResult, []byte, error) {
	if !Enabled() {
		return TaskResult{}, nil, Refusal(provider + " headless task")
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = PipeDrainLimit
	if dir := strings.TrimSpace(workDir); dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env.Environment()
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr BoundedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		diagnostics := ClassifyFailure(reportedFailure(stdout.Bytes()) + "\n" + stderr.String())
		return TaskResult{
			Diagnostics:   diagnostics,
			FailureOutput: failureOutput(stdout.String(), stderr.String()),
		}, stdout.Bytes(), fmt.Errorf("%s: %w", diagnostics, err)
	}
	return TaskResult{}, stdout.Bytes(), nil
}

func failureOutput(stdout, stderr string) string {
	var parts []string
	if s := strings.TrimSpace(stderr); s != "" {
		parts = append(parts, "stderr: "+tailString(s, failureOutputLimit))
	}
	if s := strings.TrimSpace(stdout); s != "" {
		parts = append(parts, "stdout: "+tailString(s, failureOutputLimit))
	}
	return strings.Join(parts, "\n")
}

func tailString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return "…(truncated) " + s[len(s)-limit:]
}

func WriteFile(pattern string, content []byte) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", func() {}, err
	}
	path, err := filepath.Abs(file.Name())
	if err != nil {
		path = file.Name()
	}
	remove := func() { _ = os.Remove(path) }
	if _, err := file.Write(content); err != nil {
		file.Close()
		remove()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		remove()
		return "", func() {}, err
	}
	return path, remove, nil
}

func WriteSystemPrompt(request TaskRequest) (string, func(), error) {
	system := strings.TrimSpace(request.SystemPrompt)
	if system == "" {
		return "", func() {}, nil
	}
	path, remove, err := WriteFile("attn-instructions-*.md", []byte(system))
	if err != nil {
		return "", func() {}, fmt.Errorf("write the call's instructions: %w", err)
	}
	return path, remove, nil
}

// Env is the part of attn's environment a provider's headless run keeps, beyond the base every run keeps.
type Env struct {
	Names    []string
	Prefixes []string
	// Extra returns variables to add after filtering, given the filtered environment.
	Extra func(filtered []string) []string
}

var baseNames = []string{
	"HOME", "PATH", "SHELL", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP", "LANG", "TERM", "COLORTERM",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy",
}

func (e Env) Environment() []string {
	allowedExact := make(map[string]bool, len(baseNames)+len(e.Names))
	for _, name := range append(append([]string{}, baseNames...), e.Names...) {
		allowedExact[name] = true
	}
	allowedPrefixes := append([]string{"LC_"}, e.Prefixes...)
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
	if e.Extra != nil {
		env = append(env, e.Extra(env)...)
	}
	return launchenv.WithActiveAttnFirst(env, launchenv.ActiveAttnExecutable())
}

func ClassifyFailure(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "input_too_large"):
		return "headless agent input is longer than the harness accepts"
	case strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "connection_failed"):
		return "headless agent could not reach its model provider"
	case strings.Contains(lower, "authentication_failed"),
		strings.Contains(lower, "not logged in"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "api key"):
		return "headless agent authentication failed"
	case strings.Contains(lower, "model") &&
		(strings.Contains(lower, "not found") ||
			strings.Contains(lower, "invalid") ||
			strings.Contains(lower, "unavailable") ||
			strings.Contains(lower, "unsupported") ||
			strings.Contains(lower, "not supported") ||
			strings.Contains(lower, "not exist")):
		return "headless agent model is invalid or unavailable"
	case strings.Contains(lower, "mcp"),
		strings.Contains(lower, "tool server"),
		strings.Contains(lower, "server failed"):
		return "headless agent MCP tool server failed"
	default:
		return "headless agent process failed"
	}
}

type TaskRequest struct {
	Executable      string
	Model           string
	ReasoningEffort string
	Prompt          string
	WorkDir         string

	AllowedTools []string

	DisableTools bool

	ExtraWritableRoots []string

	MaxTurns     int
	MaxBudgetUSD string
	OutputSchema json.RawMessage

	SystemPrompt string
}

type TaskResult struct {
	Diagnostics      string
	FailureOutput    string
	Text             string
	StructuredOutput json.RawMessage
	TotalCostUSD     float64
	NumTurns         int
	ModelTime        time.Duration
	InputTokens      int
	OutputTokens     int
}

type TaskProvider interface {
	RunHeadlessTask(ctx context.Context, request TaskRequest) (TaskResult, error)
}

// RunOneShot answers req with the harness CLI at executable, its tools off, in a scratch folder when req.Dir is "".
func RunOneShot(ctx context.Context, provider TaskProvider, executable string, req harness.OneShotRequest) (harness.OneShotResult, error) {
	dir := req.Dir
	if dir == "" {
		scratch, err := os.MkdirTemp("", "attn-one-shot-*")
		if err != nil {
			return harness.OneShotResult{}, fmt.Errorf("create the call's scratch directory: %w", err)
		}
		defer os.RemoveAll(scratch)
		dir = scratch
	}
	run, err := provider.RunHeadlessTask(ctx, TaskRequest{
		Executable:      executable,
		Model:           req.Model,
		ReasoningEffort: req.Effort,
		SystemPrompt:    req.System,
		Prompt:          req.Input,
		WorkDir:         dir,
		DisableTools:    true,
		MaxTurns:        req.Bound.MaxTurns,
		MaxBudgetUSD:    req.Bound.MaxCostUSD,
		OutputSchema:    req.Schema,
	})
	return harness.OneShotResult{
		Text:          run.Text,
		Structured:    run.StructuredOutput,
		Diagnostics:   run.Diagnostics,
		FailureOutput: run.FailureOutput,
		Usage: harness.OneShotUsage{
			In: run.InputTokens, Out: run.OutputTokens, CostUSD: run.TotalCostUSD, ModelTime: run.ModelTime, Turns: run.NumTurns,
		},
	}, err
}
