package garden

import (
	"sort"
	"strings"
)

// Held returns the open seeds the named member holds, freshest claim first.
func Held(seeds []Seed, member string) []Seed {
	member = strings.TrimSpace(member)
	if member == "" {
		return nil
	}
	out := []Seed{}
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(seed.TenderMember), member) {
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

// PlotsOf returns the plots the given seeds sit in, once each, in seed order.
func PlotsOf(seeds []Seed, held []Seed) []Seed {
	index := byID(seeds)
	seen := make(map[string]bool, len(held))
	out := []Seed{}
	for _, seed := range held {
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
