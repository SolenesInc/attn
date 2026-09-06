package sessioncost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

const SessionCostPricePrefix = "session_cost.price."

// Purpose says whose work a row of usage is: the agent's own turns, or a
// reviewer attn runs beside it.
const (
	PurposeAgent    = "agent"
	PurposeGuardian = "guardian"
)

// Usage is one model's billable token traffic. InputTokens excludes cache reads:
// an adapter whose provider counts them in must subtract before adding.
type Usage struct {
	InputTokens                  int64 `json:"input_tokens"`
	OutputTokens                 int64 `json:"output_tokens"`
	CacheReadInputTokens         int64 `json:"cache_read_input_tokens"`
	CacheWrite5mInputTokens      int64 `json:"cache_write_5m_input_tokens"`
	CacheWrite1hInputTokens      int64 `json:"cache_write_1h_input_tokens"`
	UnclassifiedCacheWriteTokens int64 `json:"unclassified_cache_write_tokens"`
	// What the harness itself charged for this traffic. Priced only when attn
	// has no rate card for the model, which is every model it has never heard of.
	ReportedCostUSD float64 `json:"reported_cost_usd,omitempty"`
}

func (u Usage) HasUsage() bool {
	return u.valid() && u.hasUsage()
}

func (u Usage) Add(other Usage) Usage {
	if !u.valid() || !other.valid() {
		return invalidUsage()
	}
	next, ok := addUsage(u, other)
	if !ok {
		return invalidUsage()
	}
	return next
}

func (u Usage) Subtract(other Usage) Usage {
	if !u.valid() || !other.valid() ||
		other.InputTokens > u.InputTokens ||
		other.OutputTokens > u.OutputTokens ||
		other.CacheReadInputTokens > u.CacheReadInputTokens ||
		other.CacheWrite5mInputTokens > u.CacheWrite5mInputTokens ||
		other.CacheWrite1hInputTokens > u.CacheWrite1hInputTokens ||
		other.UnclassifiedCacheWriteTokens > u.UnclassifiedCacheWriteTokens ||
		other.ReportedCostUSD > u.ReportedCostUSD {
		return invalidUsage()
	}
	return Usage{
		InputTokens:                  u.InputTokens - other.InputTokens,
		OutputTokens:                 u.OutputTokens - other.OutputTokens,
		CacheReadInputTokens:         u.CacheReadInputTokens - other.CacheReadInputTokens,
		CacheWrite5mInputTokens:      u.CacheWrite5mInputTokens - other.CacheWrite5mInputTokens,
		CacheWrite1hInputTokens:      u.CacheWrite1hInputTokens - other.CacheWrite1hInputTokens,
		UnclassifiedCacheWriteTokens: u.UnclassifiedCacheWriteTokens - other.UnclassifiedCacheWriteTokens,
		ReportedCostUSD:              u.ReportedCostUSD - other.ReportedCostUSD,
	}
}

// LedgerKey is one priceable row: a model, and whose work it was. Keys written
// before purposes existed carry no purpose and read back as the agent's own.
type LedgerKey struct {
	Model   string
	Purpose string
}

func NewLedgerKey(model, purpose string) LedgerKey {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		purpose = PurposeAgent
	}
	return LedgerKey{Model: strings.TrimSpace(model), Purpose: purpose}
}

// AgentKey names a model's row of the session's own traffic, GuardianKey the
// same model's row of what the reviewer spent.
func AgentKey(model string) LedgerKey {
	return NewLedgerKey(model, PurposeAgent)
}

func GuardianKey(model string) LedgerKey {
	return NewLedgerKey(model, PurposeGuardian)
}

func (k LedgerKey) MarshalText() ([]byte, error) {
	return []byte(NewLedgerKey(k.Model, k.Purpose).Purpose + "|" + strings.TrimSpace(k.Model)), nil
}

func (k *LedgerKey) UnmarshalText(text []byte) error {
	raw := string(text)
	purpose, model, found := strings.Cut(raw, "|")
	if !found {
		*k = NewLedgerKey(raw, PurposeAgent)
		return nil
	}
	*k = NewLedgerKey(model, purpose)
	return nil
}

type Ledger map[LedgerKey]Usage

func (l Ledger) Add(key LedgerKey, usage Usage) bool {
	if l == nil || !usage.hasUsage() || !usage.valid() {
		return false
	}
	key = NewLedgerKey(key.Model, key.Purpose)
	current := l[key]
	if !current.valid() {
		return false
	}
	next, ok := addUsage(current, usage)
	if !ok {
		return false
	}
	l[key] = next
	return true
}

type RateCard struct {
	InputUSDPerMTok        float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok       float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok    float64 `json:"cache_read_usd_per_mtok"`
	CacheWrite5mUSDPerMTok float64 `json:"cache_write_5m_usd_per_mtok"`
	CacheWrite1hUSDPerMTok float64 `json:"cache_write_1h_usd_per_mtok"`
}

type ModelSummary struct {
	Model            string
	Purpose          string
	Usage            Usage
	TotalTokens      int64
	CostUSD          *float64
	HasUnpricedUsage bool
	UnpricedReason   string
}

type Summary struct {
	Models           []ModelSummary
	TotalTokens      int64
	CostUSD          *float64
	HasUnpricedUsage bool
	HasUsage         bool
	Valid            bool
}

func ParseOverrides(settings map[string]string) (map[string]RateCard, error) {
	keys := make([]string, 0)
	for key := range settings {
		if strings.HasPrefix(key, SessionCostPricePrefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	overrides := make(map[string]RateCard, len(keys))
	for _, key := range keys {
		model := strings.TrimSpace(strings.TrimPrefix(key, SessionCostPricePrefix))
		if model == "" {
			return nil, fmt.Errorf("session cost price setting %q has no model id", key)
		}
		raw := strings.TrimSpace(settings[key])
		if raw == "" {
			continue
		}
		card, err := parseRateCard(raw)
		if err != nil {
			return nil, fmt.Errorf("session cost price for %s: %w", model, err)
		}
		overrides[model] = card
	}
	return overrides, nil
}

func Price(ledger Ledger, settings map[string]string) (usd float64, known bool, hasUsage bool) {
	summary := Summarize(ledger, settings)
	if !summary.HasUsage {
		return 0, false, false
	}
	if !summary.Valid || summary.HasUnpricedUsage || summary.CostUSD == nil {
		return 0, false, true
	}
	return *summary.CostUSD, true, true
}

func Summarize(ledger Ledger, settings map[string]string) Summary {
	summary := Summary{Valid: true}
	rows := make(Ledger, len(ledger))
	keys := make([]LedgerKey, 0, len(ledger))
	for key, usage := range ledger {
		if !usage.hasAnyValue() {
			continue
		}
		normalized := NewLedgerKey(key.Model, key.Purpose)
		if _, seen := rows[normalized]; !seen {
			keys = append(keys, normalized)
		}
		rows[normalized] = rows[normalized].Add(usage)
	}
	// The agent's own rows come first: a reviewer is an aside to the work, and a
	// stable order keeps the breakdown from reshuffling under the pointer.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Purpose != keys[j].Purpose {
			return keys[i].Purpose == PurposeAgent
		}
		return keys[i].Model < keys[j].Model
	})
	for _, key := range keys {
		model := key.Model
		usage := rows[key]
		summary.HasUsage = true
		if !usage.valid() {
			summary.Valid = false
			return summary
		}
		total, ok := usage.totalTokens()
		if !ok || total > math.MaxInt64-summary.TotalTokens {
			summary.Valid = false
			return summary
		}
		row := ModelSummary{Model: model, Purpose: key.Purpose, Usage: usage, TotalTokens: total}
		summary.TotalTokens += total

		card, cardKnown, invalidOverride := rateCardForModel(model, settings)
		// A broken override still counts as an opinion about the price: surface it
		// instead of quietly billing the harness's number in its place.
		if reported, ok := reportedCost(usage, cardKnown || invalidOverride); ok {
			row.CostUSD = floatPtr(reported)
		} else if invalidOverride {
			row.HasUnpricedUsage = true
			row.UnpricedReason = "Price override is invalid."
		} else if !cardKnown {
			row.HasUnpricedUsage = true
			row.UnpricedReason = "No price is configured for this model."
		} else {
			classified := usage
			classified.UnclassifiedCacheWriteTokens = 0
			if classified.hasUsage() {
				priced := priceUsage(classified, card)
				if math.IsNaN(priced) || math.IsInf(priced, 0) {
					summary.Valid = false
					return summary
				}
				row.CostUSD = floatPtr(priced)
			}
			if usage.UnclassifiedCacheWriteTokens > 0 {
				row.HasUnpricedUsage = true
				row.UnpricedReason = "Cache write duration is unavailable."
			}
		}

		if row.CostUSD != nil {
			if summary.CostUSD == nil {
				summary.CostUSD = floatPtr(0)
			}
			*summary.CostUSD += *row.CostUSD
			if math.IsNaN(*summary.CostUSD) || math.IsInf(*summary.CostUSD, 0) {
				summary.Valid = false
				return summary
			}
		}
		summary.HasUnpricedUsage = summary.HasUnpricedUsage || row.HasUnpricedUsage
		summary.Models = append(summary.Models, row)
	}
	return summary
}

// The harness's own price is the fallback for traffic attn cannot price: a model
// it has no card for, or cache writes whose duration the card still needs.
func reportedCost(usage Usage, priceable bool) (float64, bool) {
	if priceable && usage.UnclassifiedCacheWriteTokens == 0 {
		return 0, false
	}
	cost := usage.ReportedCostUSD
	if cost <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, false
	}
	return cost, true
}

func rateCardForModel(model string, settings map[string]string) (RateCard, bool, bool) {
	if raw, ok := settings[SessionCostPricePrefix+model]; ok && strings.TrimSpace(raw) != "" {
		card, err := parseRateCard(strings.TrimSpace(raw))
		return card, err == nil, err != nil
	}
	card, ok := builtInRateCards[model]
	return card, ok, false
}

func floatPtr(value float64) *float64 {
	return &value
}

func (u Usage) hasUsage() bool {
	return u.InputTokens > 0 ||
		u.OutputTokens > 0 ||
		u.CacheReadInputTokens > 0 ||
		u.CacheWrite5mInputTokens > 0 ||
		u.CacheWrite1hInputTokens > 0 ||
		u.UnclassifiedCacheWriteTokens > 0
}

func (u Usage) hasAnyValue() bool {
	return u.InputTokens != 0 ||
		u.OutputTokens != 0 ||
		u.CacheReadInputTokens != 0 ||
		u.CacheWrite5mInputTokens != 0 ||
		u.CacheWrite1hInputTokens != 0 ||
		u.UnclassifiedCacheWriteTokens != 0 ||
		u.ReportedCostUSD != 0
}

func (u Usage) valid() bool {
	return u.InputTokens >= 0 &&
		u.OutputTokens >= 0 &&
		u.CacheReadInputTokens >= 0 &&
		u.CacheWrite5mInputTokens >= 0 &&
		u.CacheWrite1hInputTokens >= 0 &&
		u.UnclassifiedCacheWriteTokens >= 0 &&
		u.ReportedCostUSD >= 0 &&
		!math.IsNaN(u.ReportedCostUSD) &&
		!math.IsInf(u.ReportedCostUSD, 0)
}

func (u Usage) totalTokens() (int64, bool) {
	total := int64(0)
	for _, value := range [...]int64{
		u.InputTokens,
		u.OutputTokens,
		u.CacheReadInputTokens,
		u.CacheWrite5mInputTokens,
		u.CacheWrite1hInputTokens,
		u.UnclassifiedCacheWriteTokens,
	} {
		if value < 0 || value > math.MaxInt64-total {
			return 0, false
		}
		total += value
	}
	return total, true
}

func addUsage(a, b Usage) (Usage, bool) {
	values := [6][2]int64{
		{a.InputTokens, b.InputTokens},
		{a.OutputTokens, b.OutputTokens},
		{a.CacheReadInputTokens, b.CacheReadInputTokens},
		{a.CacheWrite5mInputTokens, b.CacheWrite5mInputTokens},
		{a.CacheWrite1hInputTokens, b.CacheWrite1hInputTokens},
		{a.UnclassifiedCacheWriteTokens, b.UnclassifiedCacheWriteTokens},
	}
	for _, pair := range values {
		if pair[1] > math.MaxInt64-pair[0] {
			return Usage{}, false
		}
	}
	cost := a.ReportedCostUSD + b.ReportedCostUSD
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return Usage{}, false
	}
	return Usage{
		InputTokens:                  a.InputTokens + b.InputTokens,
		OutputTokens:                 a.OutputTokens + b.OutputTokens,
		CacheReadInputTokens:         a.CacheReadInputTokens + b.CacheReadInputTokens,
		CacheWrite5mInputTokens:      a.CacheWrite5mInputTokens + b.CacheWrite5mInputTokens,
		CacheWrite1hInputTokens:      a.CacheWrite1hInputTokens + b.CacheWrite1hInputTokens,
		UnclassifiedCacheWriteTokens: a.UnclassifiedCacheWriteTokens + b.UnclassifiedCacheWriteTokens,
		ReportedCostUSD:              cost,
	}, true
}

func invalidUsage() Usage {
	return Usage{InputTokens: -1}
}

type requiredRateCard struct {
	InputUSDPerMTok        *float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok       *float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok    *float64 `json:"cache_read_usd_per_mtok"`
	CacheWrite5mUSDPerMTok *float64 `json:"cache_write_5m_usd_per_mtok"`
	CacheWrite1hUSDPerMTok *float64 `json:"cache_write_1h_usd_per_mtok"`
}

func parseRateCard(raw string) (RateCard, error) {
	var decoded requiredRateCard
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return RateCard{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return RateCard{}, err
	}
	if decoded.InputUSDPerMTok == nil || decoded.OutputUSDPerMTok == nil ||
		decoded.CacheReadUSDPerMTok == nil || decoded.CacheWrite5mUSDPerMTok == nil ||
		decoded.CacheWrite1hUSDPerMTok == nil {
		return RateCard{}, errors.New("override requires input, output, cache-read, 5-minute cache-write, and 1-hour cache-write rates")
	}
	card := RateCard{
		InputUSDPerMTok:        *decoded.InputUSDPerMTok,
		OutputUSDPerMTok:       *decoded.OutputUSDPerMTok,
		CacheReadUSDPerMTok:    *decoded.CacheReadUSDPerMTok,
		CacheWrite5mUSDPerMTok: *decoded.CacheWrite5mUSDPerMTok,
		CacheWrite1hUSDPerMTok: *decoded.CacheWrite1hUSDPerMTok,
	}
	for _, rate := range []float64{
		card.InputUSDPerMTok,
		card.OutputUSDPerMTok,
		card.CacheReadUSDPerMTok,
		card.CacheWrite5mUSDPerMTok,
		card.CacheWrite1hUSDPerMTok,
	} {
		if rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return RateCard{}, errors.New("rates must be finite and non-negative")
		}
	}
	return card, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected data after override")
		}
		return err
	}
	return nil
}

func priceUsage(usage Usage, card RateCard) float64 {
	return (float64(usage.InputTokens)*card.InputUSDPerMTok +
		float64(usage.OutputTokens)*card.OutputUSDPerMTok +
		float64(usage.CacheReadInputTokens)*card.CacheReadUSDPerMTok +
		float64(usage.CacheWrite5mInputTokens)*card.CacheWrite5mUSDPerMTok +
		float64(usage.CacheWrite1hInputTokens)*card.CacheWrite1hUSDPerMTok) / 1_000_000
}
