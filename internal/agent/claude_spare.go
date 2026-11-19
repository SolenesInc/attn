package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/headless"
)

var _ SpareStarter = (*Claude)(nil)

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
// stdin until Answer gives it all three (F2, F4 of seed s-jhntry).
func (c *Claude) StartSpare(ctx context.Context, executable, workDir string) (Spare, error) {
	if !headless.Enabled() {
		return nil, headless.Refusal("claude spare")
	}
	cmd := exec.Command(executable, claudeSpareArgs()...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = workDir
	cmd.Env = headlessEnvironment("claude")
	var stderr boundedHeadlessOutput
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
	spare := &claudeSpare{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: &stderr, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(spare.exited)
	}()
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
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *boundedHeadlessOutput
	exited chan struct{}
	used   sync.Once
	closed sync.Once
}

func (s *claudeSpare) Close() {
	s.closed.Do(func() {
		_ = s.stdin.Close()
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	})
	select {
	case <-s.exited:
	case <-time.After(headlessPipeDrainLimit):
	}
}

func (s *claudeSpare) Answer(ctx context.Context, request ModelCallRequest) (ModelCallResult, error) {
	answered := false
	var result ModelCallResult
	err := spareUnusable("the spare already answered a call")
	s.used.Do(func() {
		answered = true
		result, err = s.answer(ctx, request)
	})
	if answered {
		s.Close()
	}
	return result, err
}

func (s *claudeSpare) answer(ctx context.Context, request ModelCallRequest) (ModelCallResult, error) {
	stop := context.AfterFunc(ctx, func() { _ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL) })
	defer stop()
	controls := []claudeControlRequest{
		{ID: "attn-instructions", Request: map[string]any{"subtype": "initialize", "systemPrompt": request.SystemPrompt}},
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		controls = append(controls, claudeControlRequest{ID: "attn-model", Request: map[string]any{"subtype": "set_model", "model": model}})
	}
	for _, control := range controls {
		if err := s.write(map[string]any{"type": "control_request", "request_id": control.ID, "request": control.Request}); err != nil {
			return ModelCallResult{}, spareUnusable("write %s: %v%s", control.Request["subtype"], err, s.diagnostics())
		}
	}
	for range controls {
		if err := s.awaitControlSuccess(ctx); err != nil {
			return ModelCallResult{}, err
		}
	}
	message := map[string]any{"type": "user", "message": map[string]any{
		"role": "user", "content": []map[string]string{{"type": "text", "text": request.Input}},
	}}
	if err := s.write(message); err != nil {
		return ModelCallResult{}, fmt.Errorf("claude: write the input: %v%s", err, s.diagnostics())
	}
	for {
		line, err := s.stdout.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ModelCallResult{}, ctx.Err()
			}
			return ModelCallResult{}, fmt.Errorf("claude ended before it answered%s", s.diagnostics())
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
			return ModelCallResult{}, fmt.Errorf("claude: %s", classifyHeadlessFailure(text))
		}
		return modelCallAnswer(text, ModelCallResult{
			ModelTime:    time.Duration(event.DurationAPIMs) * time.Millisecond,
			InputTokens:  event.Usage.InputTokens,
			OutputTokens: event.Usage.OutputTokens,
			CostUSD:      event.TotalCostUSD,
		})
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
	case <-time.After(headlessPipeDrainLimit):
		return ""
	}
	if text := strings.TrimSpace(s.stderr.String()); text != "" {
		return ": " + classifyHeadlessFailure(text)
	}
	return ""
}
