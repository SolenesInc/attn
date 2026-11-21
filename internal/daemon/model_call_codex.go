package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/codexshared"
)

var errSharedCodexUnusable = errors.New("the shared Codex app-server could not take the call")

func (r *codexShared) runningSocket(profile string) string {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s == nil || s.client() == nil {
		return ""
	}
	return s.socket
}

func codexUserMCPServers() ([]string, error) {
	raw, err := os.ReadFile(codexConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config struct {
		MCPServers map[string]toml.Primitive `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, fmt.Errorf("read the MCP servers in %s: %w", codexConfigPath(), err)
	}
	names := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func sharedCodexUnusable(err error) error {
	return fmt.Errorf("%w: %w", errSharedCodexUnusable, err)
}

func answerModelCallOnSharedCodex(ctx context.Context, socket string, request agentdriver.ModelCallRequest) (agentdriver.ModelCallResult, error) {
	servers, err := codexUserMCPServers()
	if err != nil {
		return agentdriver.ModelCallResult{}, sharedCodexUnusable(err)
	}
	finished := make(chan struct{})
	defer close(finished)
	events := make(chan codexshared.Message, 16)
	client, err := codexshared.Connect(ctx, socket, codexshared.ClientInfo{Name: "attn_model_call", Version: "1"}, func(m codexshared.Message) {
		switch m.Method {
		case "item/completed", "thread/tokenUsage/updated", "turn/completed", "error":
			select {
			case events <- m:
			case <-finished:
			}
		}
	})
	if err != nil {
		return agentdriver.ModelCallResult{}, sharedCodexUnusable(err)
	}
	defer client.Close()

	start := map[string]any{
		"model": request.Model, "ephemeral": true, "sandbox": "read-only", "approvalPolicy": "never",
		"cwd": request.WorkDir, "config": agentdriver.CodexToolFreeConfig(servers),
	}
	if system := strings.TrimSpace(request.SystemPrompt); system != "" {
		start["baseInstructions"] = system
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := sharedCodexCall(ctx, client, "thread/start", start, &thread); err != nil {
		return agentdriver.ModelCallResult{}, sharedCodexUnusable(err)
	}
	turn := map[string]any{"threadId": thread.Thread.ID, "input": []map[string]string{{"type": "text", "text": request.Input}}}
	if effort := strings.TrimSpace(request.ReasoningEffort); effort != "" {
		turn["effort"] = effort
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := sharedCodexCall(ctx, client, "turn/start", turn, &started); err != nil {
		return agentdriver.ModelCallResult{}, sharedCodexUnusable(err)
	}
	return awaitSharedCodexTurn(ctx, client, events, thread.Thread.ID, started.Turn.ID)
}

func sharedCodexCall(ctx context.Context, client *codexshared.Client, method string, params, out any) error {
	raw, err := client.Call(ctx, method, params)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func awaitSharedCodexTurn(ctx context.Context, client *codexshared.Client, events <-chan codexshared.Message, thread, turn string) (agentdriver.ModelCallResult, error) {
	var result agentdriver.ModelCallResult
	var text, failure string
	for {
		select {
		case <-ctx.Done():
			interrupt, cancel := context.WithTimeout(context.Background(), codexServerCallLimit)
			_, _ = client.Call(interrupt, "turn/interrupt", map[string]string{"threadId": thread, "turnId": turn})
			cancel()
			return agentdriver.ModelCallResult{}, ctx.Err()
		case <-client.Done():
			return agentdriver.ModelCallResult{}, errors.New("the shared Codex app-server closed the call's connection")
		case m := <-events:
			var params struct {
				ThreadID string `json:"threadId"`
				Item     struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
				TokenUsage struct {
					Last struct {
						InputTokens  int `json:"inputTokens"`
						OutputTokens int `json:"outputTokens"`
					} `json:"last"`
				} `json:"tokenUsage"`
				Turn struct {
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
				WillRetry bool `json:"willRetry"`
			}
			if json.Unmarshal(m.Params, &params) != nil || params.ThreadID != thread {
				continue
			}
			switch m.Method {
			case "item/completed":
				if params.Item.Type == "agentMessage" {
					text = params.Item.Text
				}
			case "thread/tokenUsage/updated":
				result.InputTokens, result.OutputTokens = params.TokenUsage.Last.InputTokens, params.TokenUsage.Last.OutputTokens
			case "error":
				if !params.WillRetry {
					failure = params.Error.Message
				}
			case "turn/completed":
				if params.Turn.Status != "completed" {
					if params.Turn.Error != nil && params.Turn.Error.Message != "" {
						failure = params.Turn.Error.Message
					}
					return agentdriver.ModelCallResult{}, fmt.Errorf("the Codex turn ended %s: %s", params.Turn.Status, failure)
				}
				if result.Text = strings.TrimSpace(text); result.Text == "" {
					return agentdriver.ModelCallResult{}, errors.New("the model returned no text")
				}
				return result, nil
			}
		}
	}
}
