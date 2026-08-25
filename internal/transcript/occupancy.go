package transcript

import (
	"encoding/json"
	"strings"
)

// Not TokenUsage: that SUMS a message's iterations because each was billed, while occupancy is the size of the LAST request's prompt.

type ContextObservation struct {
	Tokens int64
	// Window is 0 when the record states none. Claude's transcript never states one, so the budget cannot be a fraction of it.
	Window int64
}

func SupportsContextOccupancy(agent string) bool { return SupportsUsage(agent) }

func ContextOccupancy(agent string, line []byte) (ContextObservation, bool) {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude":
		return claudeContextOccupancy(line)
	case "codex":
		return codexContextOccupancy(line)
	default:
		return ContextObservation{}, false
	}
}

func claudeContextOccupancy(line []byte) (ContextObservation, bool) {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Usage *claudeUsageFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" || entry.Message.Usage == nil {
		return ContextObservation{}, false
	}
	fields := *entry.Message.Usage
	if n := len(fields.Iterations); n > 0 {
		fields = fields.Iterations[n-1]
	}
	tokens := fields.InputTokens + fields.CacheReadInputTokens + fields.CacheCreationInputTokens
	if fields.InputTokens < 0 || fields.CacheReadInputTokens < 0 || fields.CacheCreationInputTokens < 0 || tokens <= 0 {
		return ContextObservation{}, false
	}
	return ContextObservation{Tokens: tokens}, true
}

func codexContextOccupancy(line []byte) (ContextObservation, bool) {
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Type != "event_msg" {
		return ContextObservation{}, false
	}
	var payload struct {
		Type string `json:"type"`
		Info *struct {
			LastTokenUsage struct {
				// Codex's input_tokens already includes cached_input_tokens, so it is the whole prompt on its own.
				InputTokens int64 `json:"input_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	}
	if json.Unmarshal(envelope.Payload, &payload) != nil || payload.Type != "token_count" || payload.Info == nil {
		return ContextObservation{}, false
	}
	tokens := payload.Info.LastTokenUsage.InputTokens
	if tokens <= 0 {
		return ContextObservation{}, false
	}
	window := payload.Info.ModelContextWindow
	if window < 0 {
		window = 0
	}
	return ContextObservation{Tokens: tokens, Window: window}, true
}
