package fakeagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type ClaudeModel struct {
	Value                 string   `json:"value"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels,omitempty"`
}

var ClaudeModels = []ClaudeModel{
	{Value: "default", SupportsEffort: true, SupportedEffortLevels: []string{"low", "medium", "high", "max"}},
	{Value: "claude-opus-fake", SupportsEffort: true, SupportedEffortLevels: []string{"low", "medium", "high", "max"}},
	{Value: "claude-sonnet-fake", SupportsEffort: true, SupportedEffortLevels: []string{"medium", "high"}},
	{Value: "claude-haiku-fake"},
}

func claudeModelDiscovery() int {
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake claude: read the initialize request: %v\n", err)
		return 1
	}
	var request struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(line, &request); err != nil || request.Type != "control_request" {
		fmt.Fprintf(os.Stderr, "fake claude: want an initialize control request, got %q\n", line)
		return 1
	}
	if err := printJSONLines(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": request.RequestID, "response": map[string]any{"models": ClaudeModels},
	}}); err != nil {
		return 1
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	return 0
}

func codexModelDiscovery() int {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if err := decoder.Decode(&request); err != nil {
			if err == io.EOF {
				return 0
			}
			return 1
		}
		if request.ID == nil {
			continue
		}
		var result any = map[string]any{}
		if request.Method == "model/list" {
			models := []any{}
			for _, id := range []string{"gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
				efforts := []any{}
				for _, effort := range []string{"low", "medium", "high", "xhigh", "max", "ultra"} {
					efforts = append(efforts, map[string]string{"reasoningEffort": effort})
				}
				models = append(models, map[string]any{"id": id, "model": id, "displayName": id, "supportedReasoningEfforts": efforts})
			}
			result = map[string]any{"data": models, "nextCursor": nil}
		}
		if err := encoder.Encode(map[string]any{"id": *request.ID, "result": result}); err != nil {
			return 1
		}
	}
}
