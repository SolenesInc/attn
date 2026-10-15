package garden

import (
	"github.com/victorarias/attn/internal/who"
	"sort"
)

func TendedBy(seeds []Seed, party who.Party) []Seed {
	if party.IsZero() {
		return nil
	}
	out := []Seed{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		if seed.Claim.tender == party {
			out = append(out, seed)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StateChangedAt != out[j].StateChangedAt {
			return out[i].StateChangedAt > out[j].StateChangedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func PlotsOf(seeds []Seed, tended []Seed) []Seed {
	index := byID(seeds)
	seen := make(map[string]bool, len(tended))
	out := []Seed{}
	for _, seed := range tended {
		parent, ok := parentOf(seed)
		if !ok || seen[parent] {
			continue
		}
		crown, planted := index[parent]
		if !planted {
			continue
		}
		seen[parent] = true
		out = append(out, crown)
	}
	return out
}
