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
	{Value: "claude-fake-opus", SupportsEffort: true, SupportedEffortLevels: []string{"low", "medium", "high", "max"}},
	{Value: "claude-fake-sonnet", SupportsEffort: true, SupportedEffortLevels: []string{"medium", "high"}},
	{Value: "claude-fake-haiku"},
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
