package store

import (
	"github.com/victorarias/attn/internal/sessioncost"
	"strings"
)

const legacyLongContextPromptTokens = 272_000

var legacyLongContextModels = map[string]bool{
	"gpt-5.5":           true,
	"gpt-5.6-sol":       true,
	"gpt-5.6-terra":     true,
	"gpt-5.6-luna":      true,
	"gpt-6-astra":       true,
	"gpt-6-sol":         true,
	"gpt-6-luna":        true,
	"gpt-6.1-sol":       true,
	"codex-auto-review": true,
}

func legacyCostLedgerKey(model, purpose string) sessioncost.LedgerKey {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		purpose = "agent"
	}
	return sessioncost.LedgerKey{Model: strings.TrimSpace(model), Purpose: purpose}
}

func legacyObservationLedgerKey(observation SessionCostObservation) sessioncost.LedgerKey {
	key := legacyCostLedgerKey(observation.Model, observation.Purpose)
	usage := observation.Usage
	promptTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheWrite5mInputTokens + usage.CacheWrite1hInputTokens + usage.UnclassifiedCacheWriteTokens
	key.LongContext = legacyLongContextModels[key.Model] && promptTokens > legacyLongContextPromptTokens
	key.FastMode = observation.FastMode
	return key
}

func rekeyLegacyLongContextObservations(state *SessionCostState, onlyModel string) bool {
	if state.Ledger == nil {
		return false
	}
	changed := false
	for _, observation := range state.Observations {
		if onlyModel != "" && observation.Model != onlyModel || onlyModel == "" && observation.Model == "gpt-6.1-sol" {
			continue
		}
		standard := legacyCostLedgerKey(observation.Model, observation.Purpose)
		key := legacyObservationLedgerKey(observation)
		if key == standard {
			continue
		}
		state.Ledger[standard] = state.Ledger[standard].Subtract(observation.Usage)
		if state.Ledger[standard] == (sessioncost.Usage{}) {
			delete(state.Ledger, standard)
		}
		state.Ledger[key] = state.Ledger[key].Add(observation.Usage)
		changed = true
	}
	return changed
}

const (
	legacySlugMaxWords = 6
	legacySlugMaxChars = 100
)

var legacySlugStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "in": true, "on": true, "at": true,
	"to": true, "for": true, "with": true, "by": true, "from": true, "and": true, "or": true,
	"as": true, "is": true, "into": true, "its": true, "it": true,
}

func legacyGardenStepSlug(title string) string {
	words := legacySlugWords(title)
	kept := words[:0:0]
	for _, word := range words {
		if !legacySlugStopWords[word] {
			kept = append(kept, word)
		}
	}
	if len(kept) == 0 {
		kept = words
	}
	if len(kept) > legacySlugMaxWords {
		kept = kept[:legacySlugMaxWords]
	}
	slug := strings.Join(kept, "-")
	if runes := []rune(slug); len(runes) > legacySlugMaxChars {
		slug = strings.Trim(string(runes[:legacySlugMaxChars]), "-")
	}
	if slug == "" {
		return "seed"
	}
	return slug
}

func legacySlugWords(title string) []string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, char := range strings.ToLower(strings.TrimSpace(title)) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			word.WriteRune(char)
		} else {
			flush()
		}
	}
	flush()
	return words
}
