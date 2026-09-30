package transcript

import (
	"encoding/json"
	"math"
	"os"
	"strings"
)

type TokenUsage struct {
	Key                          string
	Model                        string
	Purpose                      string
	FastMode                     bool
	InputTokens                  int64
	OutputTokens                 int64
	CacheWrite5mTokens           int64
	CacheWrite1hTokens           int64
	CacheWriteUnclassifiedTokens int64
	CacheReadTokens              int64
	ReportedCostUSD              float64
}

func (u TokenUsage) HasTokens() bool {
	return u.InputTokens > 0 ||
		u.OutputTokens > 0 ||
		u.CacheWrite5mTokens > 0 ||
		u.CacheWrite1hTokens > 0 ||
		u.CacheWriteUnclassifiedTokens > 0 ||
		u.CacheReadTokens > 0
}

type UsageExtractor struct {
	agent             string
	codexModel        string
	codexFastMode     bool
	codexTurnFastMode bool
	codexUsageStarted bool
	last              TokenUsage
	hasLast           bool
}

func NewUsageExtractor(agent string) *UsageExtractor {
	return &UsageExtractor{agent: strings.ToLower(strings.TrimSpace(agent))}
}

func SupportsUsage(agent string) bool {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude", "codex", "pi":
		return true
	default:
		return false
	}
}

func (e *UsageExtractor) Observe(line []byte, sourceKey string) (TokenUsage, bool) {
	var usage TokenUsage
	var ok bool
	switch e.agent {
	case "claude":
		usage, ok = extractClaudeUsage(line)
	case "codex":
		usage, ok = e.extractCodexUsage(line, sourceKey)
	case "pi":
		usage, ok = extractPiUsage(line)
	default:
		return TokenUsage{}, false
	}
	if !ok {
		return TokenUsage{}, false
	}
	if e.hasLast && e.last == usage {
		return TokenUsage{}, false
	}
	e.last = usage
	e.hasLast = true
	return usage, true
}

func (e *UsageExtractor) setCodexModel(model string) {
	e.codexModel = strings.TrimSpace(model)
}

func (e *UsageExtractor) seedCodexUsageBefore(f *os.File, before int64) error {
	if e.agent != "codex" {
		return nil
	}
	var records [][]byte
	foundContext := false
	for before > 0 {
		line, start, ok, err := previousCompleteLine(f, before)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		kind := codexUsageRecordKind(line)
		if !foundContext && kind != "" {
			records = append(records, line)
		}
		if kind == "thread_settings_applied" && foundContext {
			records = append(records, line)
			break
		}
		if kind == "turn_context" {
			foundContext = true
		}
		before = start
	}
	for i := len(records) - 1; i >= 0; i-- {
		_, _ = e.extractCodexUsage(records[i], "bootstrap")
	}
	return nil
}

func codexUsageRecordKind(line []byte) string {
	var entry struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return ""
	}
	if entry.Type == "turn_context" {
		return entry.Type
	}
	if entry.Type == "event_msg" && (entry.Payload.Type == "thread_settings_applied" || entry.Payload.Type == "token_count") {
		return entry.Payload.Type
	}
	return ""
}

type claudeUsageFields struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreation            struct {
		Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	Iterations []claudeUsageFields `json:"iterations"`
}

func extractClaudeUsage(line []byte) (TokenUsage, bool) {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			ID    string             `json:"id"`
			Model string             `json:"model"`
			Usage *claudeUsageFields `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" {
		return TokenUsage{}, false
	}
	id := strings.TrimSpace(entry.Message.ID)
	if id == "" || entry.Message.Usage == nil {
		return TokenUsage{}, false
	}

	usage := TokenUsage{Key: "claude:" + id, Model: strings.TrimSpace(entry.Message.Model)}
	if len(entry.Message.Usage.Iterations) == 0 {
		if !addClaudeUsage(&usage, *entry.Message.Usage) {
			return TokenUsage{}, false
		}
		return usage, true
	}
	for _, iteration := range entry.Message.Usage.Iterations {
		if !addClaudeUsage(&usage, iteration) {
			return TokenUsage{}, false
		}
	}
	return usage, true
}

func addClaudeUsage(dst *TokenUsage, src claudeUsageFields) bool {
	values := [...]int64{
		src.InputTokens,
		src.OutputTokens,
		src.CacheCreationInputTokens,
		src.CacheReadInputTokens,
		src.CacheCreation.Ephemeral5mInputTokens,
		src.CacheCreation.Ephemeral1hInputTokens,
	}
	for _, value := range values {
		if value < 0 {
			return false
		}
	}

	classifiedWrite := src.CacheCreation.Ephemeral5mInputTokens + src.CacheCreation.Ephemeral1hInputTokens
	if classifiedWrite < 0 {
		return false
	}
	unclassifiedWrite := src.CacheCreationInputTokens - classifiedWrite
	if unclassifiedWrite < 0 {
		unclassifiedWrite = 0
	}
	updates := []struct {
		dst   *int64
		value int64
	}{
		{&dst.InputTokens, src.InputTokens},
		{&dst.OutputTokens, src.OutputTokens},
		{&dst.CacheWrite5mTokens, src.CacheCreation.Ephemeral5mInputTokens},
		{&dst.CacheWrite1hTokens, src.CacheCreation.Ephemeral1hInputTokens},
		{&dst.CacheWriteUnclassifiedTokens, unclassifiedWrite},
		{&dst.CacheReadTokens, src.CacheReadInputTokens},
	}
	for _, update := range updates {
		if update.value > math.MaxInt64-*update.dst {
			return false
		}
	}
	for _, update := range updates {
		*update.dst += update.value
	}
	return true
}

func (e *UsageExtractor) extractCodexUsage(line []byte, sourceKey string) (TokenUsage, bool) {
	if model, ok := codexTurnContextModel(line); ok {
		e.setCodexModel(model)
		e.codexTurnFastMode = e.codexFastMode
		e.codexUsageStarted = false
		return TokenUsage{}, false
	}
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return TokenUsage{}, false
	}
	if envelope.Type != "event_msg" || strings.TrimSpace(sourceKey) == "" {
		return TokenUsage{}, false
	}
	var payload struct {
		Type           string `json:"type"`
		ThreadSettings *struct {
			ServiceTier string `json:"service_tier"`
		} `json:"thread_settings"`
		Info *struct {
			LastTokenUsage struct {
				InputTokens           int64 `json:"input_tokens"`
				CachedInputTokens     int64 `json:"cached_input_tokens"`
				CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
				OutputTokens          int64 `json:"output_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	}
	if json.Unmarshal(envelope.Payload, &payload) != nil {
		return TokenUsage{}, false
	}
	if payload.Type == "thread_settings_applied" && payload.ThreadSettings != nil {
		tier := payload.ThreadSettings.ServiceTier
		e.codexFastMode = tier == "priority" || tier == "fast"
		// Codex can persist the turn's settings immediately after its turn_context.
		if !e.codexUsageStarted {
			e.codexTurnFastMode = e.codexFastMode
		}
		return TokenUsage{}, false
	}
	if payload.Type != "token_count" || payload.Info == nil {
		return TokenUsage{}, false
	}
	last := payload.Info.LastTokenUsage
	if last.InputTokens < 0 || last.CachedInputTokens < 0 || last.CacheWriteInputTokens < 0 || last.OutputTokens < 0 ||
		last.CachedInputTokens+last.CacheWriteInputTokens > last.InputTokens {
		return TokenUsage{}, false
	}
	e.codexUsageStarted = true
	return TokenUsage{
		Key:                "codex:" + strings.TrimSpace(sourceKey),
		Model:              e.codexModel,
		FastMode:           e.codexTurnFastMode,
		InputTokens:        last.InputTokens - last.CachedInputTokens - last.CacheWriteInputTokens,
		OutputTokens:       last.OutputTokens,
		CacheReadTokens:    last.CachedInputTokens,
		CacheWrite5mTokens: last.CacheWriteInputTokens,
	}, true
}

func codexTurnContextModel(line []byte) (string, bool) {
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil || envelope.Type != "turn_context" {
		return "", false
	}
	var payload struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(envelope.Payload, &payload) != nil {
		return "", false
	}
	return payload.Model, true
}
