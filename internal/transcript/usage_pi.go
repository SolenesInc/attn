package transcript

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/victorarias/attn/internal/sessioncost"
)

// The custom entry attn's Guardian appends after every judgement it makes.
const piGuardianUsageCustomType = "attn-guardian-usage"

// pi's session file is one JSON entry per line, and prices its own traffic.
// Format: @earendil-works/pi-coding-agent docs/session-format.md.
type piUsageFields struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

type piUsageEntry struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	CustomType string `json:"customType"`
	Message    struct {
		Role  string         `json:"role"`
		Model string         `json:"model"`
		Usage *piUsageFields `json:"usage"`
	} `json:"message"`
	Data struct {
		Model string         `json:"model"`
		Usage *piUsageFields `json:"usage"`
	} `json:"data"`
}

func extractPiUsage(line []byte) (TokenUsage, bool) {
	var entry piUsageEntry
	if json.Unmarshal(line, &entry) != nil {
		return TokenUsage{}, false
	}
	id := strings.TrimSpace(entry.ID)
	if id == "" {
		return TokenUsage{}, false
	}
	switch entry.Type {
	case "message":
		if entry.Message.Role != "assistant" || entry.Message.Usage == nil {
			return TokenUsage{}, false
		}
		return piTokenUsage(id, entry.Message.Model, sessioncost.PurposeAgent, *entry.Message.Usage)
	case "custom":
		if entry.CustomType != piGuardianUsageCustomType || entry.Data.Usage == nil {
			return TokenUsage{}, false
		}
		return piTokenUsage(id, entry.Data.Model, sessioncost.PurposeGuardian, *entry.Data.Usage)
	default:
		return TokenUsage{}, false
	}
}

func piTokenUsage(id, model, purpose string, fields piUsageFields) (TokenUsage, bool) {
	for _, value := range [...]int64{fields.Input, fields.Output, fields.CacheRead, fields.CacheWrite} {
		if value < 0 {
			return TokenUsage{}, false
		}
	}
	cost := fields.Cost.Total
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		cost = 0
	}
	usage := TokenUsage{
		Key:     "pi:" + id,
		Model:   strings.TrimSpace(model),
		Purpose: purpose,
		// pi counts cache reads outside `input` and never says how long a cache
		// write lives, so the write stays unclassified and its price comes from pi.
		InputTokens:                  fields.Input,
		OutputTokens:                 fields.Output,
		CacheReadTokens:              fields.CacheRead,
		CacheWriteUnclassifiedTokens: fields.CacheWrite,
		ReportedCostUSD:              cost,
	}
	if !usage.HasTokens() {
		return TokenUsage{}, false
	}
	return usage, true
}
