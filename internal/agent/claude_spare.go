package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
)

func spareUnusable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", harness.ErrSpareUnusable, fmt.Sprintf(format, args...))
}

const claudeSpareReadyRequest = "attn-ready"

func claudeSpareArgs() []string {
	args := []string{"--print"}
	args = append(args, claudeHeadlessIsolationArgs()...)
	return append(args,
		"--strict-mcp-config",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--no-chrome",
		"--disallowedTools", "*",
		"--permission-mode", "dontAsk",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	)
}

// StartSpare starts claude with no model, instructions or input; it waits on
// stdin until Answer gives it all three (F2, F4 of seed s-jhntry). Claude
// answers an mcp_status request once its session has started, with or without
// --bare; the spare is then stopped until Answer.
func (c *Claude) StartSpare(ctx context.Context, executable string) (harness.Spare, error) {
	if !headless.Enabled() {
		return nil, headless.Refusal("claude spare")
	}
	workDir, err := os.MkdirTemp("", "attn-model-call-spare-*")
	if err != nil {
		return nil, fmt.Errorf("create the spare's scratch directory: %w", err)
	}
	spare, err := startClaudeSpare(ctx, executable, workDir)
	if err != nil {
		_ = os.RemoveAll(workDir)
	}
	return spare, err
}

func startClaudeSpare(ctx context.Context, executable, workDir string) (harness.Spare, error) {
	cmd := exec.Command(executable, claudeSpareArgs()...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = workDir
	cmd.Env = append(claudeHeadlessEnv.Environment(), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	var stderr headless.BoundedOutput
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude spare: %w", err)
	}
	spare := &claudeSpare{workDir: workDir, cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: &stderr, exited: make(chan struct{}), started: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(spare.exited)
	}()
	if err := spare.write(map[string]any{"type": "control_request", "request_id": claudeSpareReadyRequest, "request": map[string]any{"subtype": "mcp_status"}}); err != nil {
		spare.Close()
		return nil, fmt.Errorf("ask the claude spare whether it started: %w", err)
	}
	go spare.stopOnceStarted()
	go func() {
		select {
		case <-ctx.Done():
			spare.Close()
		case <-spare.exited:
		}
	}()
	return spare, nil
}

type claudeSpare struct {
	workDir string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *headless.BoundedOutput
	exited  chan struct{}
	started chan struct{}
	used    sync.Once
	closed  sync.Once

	mu      sync.Mutex
	taken   bool
	stopped bool
}

func (s *claudeSpare) stopOnceStarted() {
	defer close(s.started)
	if s.awaitControlSuccess(context.Background()) == nil {
		s.stopUntilTaken()
	}
}

func (s *claudeSpare) stopUntilTaken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.taken && s.running() {
		s.stopped = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGSTOP) == nil
	}
}

func (s *claudeSpare) running() bool {
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

func (s *claudeSpare) take() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taken = true
	if s.stopped && s.running() {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGCONT)
	}
}

func (s *claudeSpare) Close() {
	s.closed.Do(func() {
		_ = s.stdin.Close()
		if s.running() {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	select {
	case <-s.exited:
	case <-time.After(headless.PipeDrainLimit):
	}
	_ = os.RemoveAll(s.workDir)
}

func (s *claudeSpare) Answer(ctx context.Context, request harness.OneShotRequest) (harness.OneShotResult, error) {
	answered := false
	var result harness.OneShotResult
	err := spareUnusable("the spare already answered a call")
	s.used.Do(func() {
		answered = true
		s.take()
		result, err = s.answer(ctx, request)
	})
	if answered {
		s.Close()
	}
	return result, err
}

func (s *claudeSpare) answer(ctx context.Context, request harness.OneShotRequest) (harness.OneShotResult, error) {
	stop := context.AfterFunc(ctx, func() { _ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL) })
	defer stop()
	<-s.started
	controls := []claudeControlRequest{
		{ID: "attn-instructions", Request: map[string]any{"subtype": "initialize", "systemPrompt": request.System}},
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		controls = append(controls, claudeControlRequest{ID: "attn-model", Request: map[string]any{"subtype": "set_model", "model": model}})
	}
	for _, control := range controls {
		if err := s.write(map[string]any{"type": "control_request", "request_id": control.ID, "request": control.Request}); err != nil {
			return harness.OneShotResult{}, spareUnusable("write %s: %v%s", control.Request["subtype"], err, s.diagnostics())
		}
	}
	for range controls {
		if err := s.awaitControlSuccess(ctx); err != nil {
			return harness.OneShotResult{}, err
		}
	}
	message := map[string]any{"type": "user", "message": map[string]any{
		"role": "user", "content": []map[string]string{{"type": "text", "text": request.Input}},
	}}
	if err := s.write(message); err != nil {
		return harness.OneShotResult{}, fmt.Errorf("claude: write the input: %v%s", err, s.diagnostics())
	}
	for {
		line, err := s.stdout.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return harness.OneShotResult{}, ctx.Err()
			}
			return harness.OneShotResult{}, fmt.Errorf("claude ended before it answered%s", s.diagnostics())
		}
		var event struct {
			Type    string          `json:"type"`
			IsError bool            `json:"is_error"`
			Result  json.RawMessage `json:"result"`
			claudeResultMeta
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "result" {
			continue
		}
		text := claudeResultString(event.Result)
		if event.IsError {
			return harness.OneShotResult{}, fmt.Errorf("claude: %s", headless.ClassifyFailure(text))
		}
		return harness.OneShotResult{Text: text, Usage: harness.OneShotUsage{
			ModelTime: time.Duration(event.DurationAPIMs) * time.Millisecond,
			In:        event.Usage.allInputTokens(),
			Out:       event.Usage.OutputTokens,
			CostUSD:   event.TotalCostUSD,
		}}, nil
	}
}

type claudeControlRequest struct {
	ID      string
	Request map[string]any
}

func (s *claudeSpare) write(message any) error {
	line, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(line, '\n'))
	return err
}

func (s *claudeSpare) awaitControlSuccess(ctx context.Context) error {
	for {
		line, err := s.stdout.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return spareUnusable("claude ended before it took the call's instructions%s", s.diagnostics())
		}
		var event struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string `json:"subtype"`
				RequestID string `json:"request_id"`
				Error     string `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "control_response" {
			continue
		}
		if event.Response.Subtype != "success" {
			return spareUnusable("claude refused %s: %s", event.Response.RequestID, event.Response.Error)
		}
		return nil
	}
}

func (s *claudeSpare) diagnostics() string {
	select {
	case <-s.exited:
	case <-time.After(headless.PipeDrainLimit):
		return ""
	}
	if text := strings.TrimSpace(s.stderr.String()); text != "" {
		return ": " + headless.ClassifyFailure(text)
	}
	return ""
}
