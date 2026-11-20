package fakeagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

const (
	methodSpare = "spare"
	// FakeModelMs is the model time the stand-in spare reports for every answer.
	FakeModelMs = 1234
)

// Spare is a stand-in claude started ahead of a model call, waiting on stdin.
type Spare struct {
	Argv   []string
	exited <-chan struct{}
	t      testing.TB
}

func (s *Spare) Exited() bool {
	select {
	case <-s.exited:
		return true
	default:
		return false
	}
}

// AwaitExited waits until the spare's process has exited.
func (s *Spare) AwaitExited() {
	s.t.Helper()
	select {
	case <-s.exited:
	case <-time.After(HangGuard):
		s.t.Fatalf("the spare claude still runs %s after it should have exited", HangGuard)
	}
}

func (k *Kit) Spare() *Spare {
	k.t.Helper()
	select {
	case spare := <-k.spares:
		return spare
	case <-time.After(HangGuard):
		k.t.Fatalf("the daemon started no spare claude within %s%s", HangGuard, k.failureSummary())
		return nil
	}
}

func (k *Kit) receiveSpare(f *fake, params json.RawMessage) (any, error) {
	var started launchedAs
	if err := json.Unmarshal(params, &started); err != nil {
		return nil, err
	}
	k.spares <- &Spare{Argv: started.Argv, exited: f.peer.done, t: k.t}
	return struct{}{}, nil
}

// claudeSpare serves a claude started with stream-json input and no prompt:
// it takes its instructions and model from control requests, then answers the
// one user message through the test as a headless task.
func claudeSpare(cfg config) int {
	control, err := dialControl(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake claude spare: %v\n", err)
		return 1
	}
	defer control.close()
	dir, _ := os.Getwd()
	started := launchedAs{Argv: os.Args, Env: os.Environ(), Dir: dir}
	control.start()
	if err := control.call(context.Background(), methodSpare, started, &struct{}{}); err != nil {
		fmt.Fprintf(os.Stderr, "fake claude spare: %v\n", err)
		return 1
	}
	if err := reportSessionStarted(); err != nil {
		fmt.Fprintf(os.Stderr, "fake claude spare: %v\n", err)
		return 1
	}
	asked := headlessTask{Harness: Claude, launchedAs: started}
	instructed := false
	input := bufio.NewReader(os.Stdin)
	for {
		line, err := input.ReadBytes('\n')
		if err != nil {
			return 0
		}
		var message struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype      string `json:"subtype"`
				SystemPrompt string `json:"systemPrompt"`
				Model        string `json:"model"`
			} `json:"request"`
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &message); err != nil {
			fmt.Fprintf(os.Stderr, "fake claude spare: %q is not JSON\n", line)
			return 1
		}
		switch message.Type {
		case "control_request":
			switch message.Request.Subtype {
			case "initialize":
				instructed = true
				asked.Prompt = message.Request.SystemPrompt
			case "set_model":
				asked.Model = message.Request.Model
			}
			if err := printJSONLines(map[string]any{"type": "control_response", "response": map[string]any{
				"subtype": "success", "request_id": message.RequestID,
			}}); err != nil {
				return 1
			}
		case "user":
			if !instructed {
				asked.Refusal = "a spare claude got its input before its instructions"
			}
			for _, block := range message.Message.Content {
				asked.Prompt = joinSystemPrompt(asked.Prompt, block.Text)
			}
			var answer headlessAnswer
			if err := control.call(context.Background(), methodHeadless, asked, &answer); err != nil {
				fmt.Fprintf(os.Stderr, "fake claude spare: %v\n", err)
				return 1
			}
			result := claudeResult(answer.Failure != "", answer.Text+answer.Failure)
			result["duration_api_ms"] = FakeModelMs
			result["usage"] = map[string]int{"input_tokens": 661, "output_tokens": 12}
			if err := printJSONLines(claudeInit(asked.Model), result); err != nil {
				return 1
			}
			return 0
		}
	}
}

func reportSessionStarted() error {
	path := os.Getenv("ATTN_MODEL_CALL_SPARE_STARTED")
	if path == "" || !slices.Contains(os.Args, "--plugin-dir") {
		return nil
	}
	return os.WriteFile(path, []byte("started\n"), 0o600)
}
