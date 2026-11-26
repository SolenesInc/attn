package garden

func Window(seeds []Seed, limit int) []Seed {
	if len(seeds) <= limit {
		return seeds
	}
	byID := make(map[string]Seed, len(seeds))
	for _, seed := range seeds {
		byID[seed.ID] = seed
	}
	keep := make(map[string]bool, limit)
	for _, seed := range seeds {
		if Closed(seed.Status) {
			continue
		}
		for cur := seed; !keep[cur.ID]; {
			keep[cur.ID] = true
			parentID, ok := ParentOf(cur)
			if !ok {
				break
			}
			parent, exists := byID[parentID]
			if !exists {
				break
			}
			cur = parent
		}
	}
	for _, seed := range seeds {
		if len(keep) >= limit {
			break
		}
		keep[seed.ID] = true
	}
	out := make([]Seed, 0, len(keep))
	for _, seed := range seeds {
		if keep[seed.ID] {
			out = append(out, seed)
		}
	}
	return out
}

func LiveWindow(seeds []Seed, plots PlotTree, pinned map[string]bool) []Seed {
	keep := make(map[string]bool)
	for _, seed := range seeds {
		if Closed(seed.Status) && !pinned[seed.ID] {
			continue
		}
		for cur := seed; !keep[cur.ID]; {
			keep[cur.ID] = true
			parentID, ok := ParentOf(cur)
			if !ok {
				break
			}
			parent, exists := plots.Seed(parentID)
			if !exists {
				break
			}
			cur = parent
		}
	}
	out := make([]Seed, 0, len(keep))
	for _, seed := range seeds {
		if keep[seed.ID] {
			out = append(out, seed)
		}
	}
	return out
}
