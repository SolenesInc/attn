package sessioncost

var builtInRateCards = map[string]RateCard{
	"claude-fable-5-1":          withCacheRead(anthropicRates(10, 50), 0.25),
	"claude-fable-5":            anthropicRates(10, 50),
	"claude-opus-5-5":           withCacheRead(anthropicRates(4, 20), 0.2),
	"claude-opus-5":             anthropicRates(5, 25),
	"claude-opus-4-8":           anthropicRates(5, 25),
	"claude-opus-4-6":           anthropicRates(5, 25),
	"claude-sonnet-5":           anthropicRates(2, 10),
	"claude-sonnet-4-6":         anthropicRates(3, 15),
	"claude-haiku-4-5":          anthropicRates(1, 5),
	"claude-haiku-4-5-20251001": anthropicRates(1, 5),

	"gpt-5-codex":  openAIRates(1.25, 10, 0.125, 0),
	"gpt-5.4-mini": openAIRates(0.75, 4.5, 0.075, 0),
	"gpt-5.5":      openAIRates(5, 30, 0.5, 0),

	"gpt-5.6-sol":   openAIRates(5, 30, 0.5, 6.25),
	"gpt-5.6-terra": openAIRates(2, 12, 0.2, 2.5),
	"gpt-5.6-luna":  openAIRates(0.2, 1.2, 0.02, 0.25),

	"gpt-6-astra": openAIRates(10, 50, 1, 12.5),
	"gpt-6-sol":   openAIRates(2, 10, 0.2, 2.5),
	"gpt-6-luna":  openAIRates(0.1, 0.5, 0.01, 0.125),
}

const (
	openAILongContextPromptTokens     = 272_000
	openAILongContextInputMultiplier  = 2
	openAILongContextOutputMultiplier = 1.5
)

var openAILongContextModels = map[string]bool{
	"gpt-5.5":       true,
	"gpt-5.6-sol":   true,
	"gpt-5.6-terra": true,
	"gpt-5.6-luna":  true,
	"gpt-6-astra":   true,
	"gpt-6-sol":     true,
	"gpt-6-luna":    true,
}

func longContextRates(card RateCard) RateCard {
	return RateCard{
		InputUSDPerMTok:        card.InputUSDPerMTok * openAILongContextInputMultiplier,
		OutputUSDPerMTok:       card.OutputUSDPerMTok * openAILongContextOutputMultiplier,
		CacheReadUSDPerMTok:    card.CacheReadUSDPerMTok * openAILongContextInputMultiplier,
		CacheWrite5mUSDPerMTok: card.CacheWrite5mUSDPerMTok * openAILongContextInputMultiplier,
		CacheWrite1hUSDPerMTok: card.CacheWrite1hUSDPerMTok * openAILongContextInputMultiplier,
	}
}

func anthropicRates(input, output float64) RateCard {
	return RateCard{
		InputUSDPerMTok:        input,
		OutputUSDPerMTok:       output,
		CacheReadUSDPerMTok:    input * 0.1,
		CacheWrite5mUSDPerMTok: input * 1.25,
		CacheWrite1hUSDPerMTok: input * 2,
	}
}

func withCacheRead(card RateCard, cacheRead float64) RateCard {
	card.CacheReadUSDPerMTok = cacheRead
	return card
}

func openAIRates(input, output, cacheRead, cacheWrite float64) RateCard {
	return RateCard{
		InputUSDPerMTok:        input,
		OutputUSDPerMTok:       output,
		CacheReadUSDPerMTok:    cacheRead,
		CacheWrite5mUSDPerMTok: cacheWrite,
		CacheWrite1hUSDPerMTok: cacheWrite,
	}
}
