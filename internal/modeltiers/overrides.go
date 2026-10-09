package modeltiers

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func ParseOverrides(raw string) (Overrides, error) {
	result := Overrides{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	var entries []Override
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("invalid model_tier_overrides: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("model_tier_overrides must contain one JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("model_tier_overrides must contain one JSON array")
	}
	for _, entry := range entries {
		if entry.Harness == "" || entry.Model == "" {
			return nil, fmt.Errorf("model_tier_overrides requires a harness and model for each override")
		}
		if err := delegationprefs.ValidateSelection(delegationprefs.Selection{Harness: entry.Harness, Provider: entry.Provider, Model: entry.Model}, true); err != nil {
			return nil, fmt.Errorf("invalid model_tier_overrides identity: %w", err)
		}
		switch entry.Tier {
		case Light, Standard, Deep:
		default:
			return nil, fmt.Errorf("model_tier_overrides tier %q must be light, standard or deep", entry.Tier)
		}
		if _, exists := result[entry.Key]; exists {
			return nil, fmt.Errorf("model_tier_overrides repeats %s/%s/%s", entry.Harness, entry.Provider, entry.Model)
		}
		result[entry.Key] = entry.Tier
	}
	return result, nil
}
