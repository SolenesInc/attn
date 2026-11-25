package daemon

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	defaultGardenPageLimit = 50
	maxGardenPageLimit     = 200
	maxGardenLookupIDs     = 200
	maxGardenParentHops    = 32
)

func gardenRootOf(seed garden.Seed, plots garden.PlotTree) garden.Seed {
	cur := seed
	for range maxGardenParentHops {
		parentID, ok := garden.ParentOf(cur)
		if !ok {
			break
		}
		parent, exists := plots.Seed(parentID)
		if !exists {
			break
		}
		cur = parent
	}
	return cur
}

func gardenAncestorsOf(seed garden.Seed, plots garden.PlotTree) []garden.Seed {
	var chain []garden.Seed
	seen := map[string]bool{seed.ID: true}
	cur := seed
	for range maxGardenParentHops {
		parentID, ok := garden.ParentOf(cur)
		if !ok || seen[parentID] {
			break
		}
		parent, exists := plots.Seed(parentID)
		if !exists {
			break
		}
		seen[parentID] = true
		chain = append(chain, parent)
		cur = parent
	}
	slices.Reverse(chain)
	return chain
}

func gardenRelatedOf(seed garden.Seed, seeds []garden.Seed, plots garden.PlotTree) []garden.Seed {
	seen := map[string]bool{seed.ID: true}
	var out []garden.Seed
	add := func(id string) {
		if seen[id] {
			return
		}
		other, exists := plots.Seed(id)
		if !exists {
			return
		}
		seen[id] = true
		out = append(out, other)
	}
	for _, edge := range seed.Edges {
		if edge.Kind == garden.EdgePartOf {
			continue
		}
		add(edge.To)
	}
	for _, candidate := range seeds {
		if seen[candidate.ID] {
			continue
		}
		for _, edge := range candidate.Edges {
			if edge.Kind != garden.EdgePartOf && edge.To == seed.ID {
				add(candidate.ID)
				break
			}
		}
	}
	return out
}

func openAncestors(seeds []garden.Seed, plots garden.PlotTree) map[string]bool {
	hasOpen := map[string]bool{}
	for _, seed := range seeds {
		if garden.Closed(seed.Status) {
			continue
		}
		cur := seed
		for range maxGardenParentHops {
			parentID, ok := garden.ParentOf(cur)
			if !ok || hasOpen[parentID] {
				break
			}
			parent, exists := plots.Seed(parentID)
			if !exists {
				break
			}
			hasOpen[parentID] = true
			cur = parent
		}
	}
	return hasOpen
}

func addClosedTally(tally *protocol.GardenClosedTally, status string) {
	switch status {
	case garden.StatusHarvested:
		tally.Harvested++
	case garden.StatusWithered:
		tally.Withered++
	}
}

func gardenClosedCounts(read gardenRead) *protocol.GardenClosedCounts {
	hasOpen := openAncestors(read.seeds, read.plots)
	counts := &protocol.GardenClosedCounts{ByBed: []protocol.GardenBedClosedTally{}}
	byBed := map[string]*protocol.GardenClosedTally{}
	for _, seed := range read.seeds {
		if parentID, ok := garden.ParentOf(seed); ok {
			if _, exists := read.plots.Seed(parentID); exists {
				continue
			}
		}
		if !garden.Closed(seed.Status) || hasOpen[seed.ID] {
			continue
		}
		if seed.AutomationID > 0 {
			addClosedTally(&counts.Automations, seed.Status)
			continue
		}
		addClosedTally(&counts.All, seed.Status)
		effBedID, _ := read.rootBed(seed)
		if effBedID == "" {
			addClosedTally(&counts.Unassigned, seed.Status)
			continue
		}
		entry := byBed[effBedID]
		if entry == nil {
			entry = &protocol.GardenClosedTally{}
			byBed[effBedID] = entry
		}
		addClosedTally(entry, seed.Status)
	}
	if len(byBed) > 0 {
		bedIDs := make([]string, 0, len(byBed))
		for id := range byBed {
			bedIDs = append(bedIDs, id)
		}
		slices.Sort(bedIDs)
		counts.ByBed = make([]protocol.GardenBedClosedTally, 0, len(bedIDs))
		for _, id := range bedIDs {
			t := byBed[id]
			counts.ByBed = append(counts.ByBed, protocol.GardenBedClosedTally{
				BedID:     id,
				Harvested: t.Harvested,
				Withered:  t.Withered,
			})
		}
	}
	return counts
}

func gardenMatchesBedScope(read gardenRead, seed garden.Seed, scope string) bool {
	if scope == "" {
		return true
	}
	root := gardenRootOf(seed, read.plots)
	isAuto := root.AutomationID > 0
	switch scope {
	case "automations":
		return isAuto
	case "all":
		return !isAuto
	case "unassigned":
		if isAuto {
			return false
		}
		effBedID, _ := read.rootBed(root)
		return effBedID == ""
	default:
		if isAuto {
			return false
		}
		effBedID, _ := read.rootBed(root)
		return effBedID == scope
	}
}

func seedStateChangedTime(seed garden.Seed, doc docstore.Document) time.Time {
	if raw := strings.TrimSpace(seed.StateChangedAt); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UTC()
		}
	}
	if !doc.UpdatedAt.IsZero() {
		return doc.UpdatedAt.UTC()
	}
	return doc.CreatedAt.UTC()
}

func formatPageCursor(at time.Time, id string) string {
	return at.UTC().Format(time.RFC3339Nano) + "|" + id
}

func parsePageCursor(raw string) (time.Time, string, bool) {
	ts, id, ok := strings.Cut(raw, "|")
	if !ok || id == "" {
		return time.Time{}, raw, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, raw, false
	}
	return parsed.UTC(), id, true
}

func afterCursor(at time.Time, id string, cursorAt time.Time, cursorID string) bool {
	if cmp := at.Compare(cursorAt); cmp != 0 {
		return cmp < 0
	}
	return id < cursorID
}

type closedPageKey struct {
	idx int
	at  time.Time
}

func (d *Daemon) handleGardenSeedsPage(client *wsClient, profileID string, msg *protocol.GardenSeedsPageMessage) {
	reply := protocol.GardenSeedsPageResultMessage{
		Event:     protocol.EventGardenSeedsPageResult,
		RequestID: msg.RequestID,
		Seeds:     []protocol.Seed{},
	}
	fail := func(err error) {
		reply.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, reply)
	}
	status := strings.TrimSpace(protocol.Deref(msg.Status))
	if status == "" {
		status = "closed"
	}
	if status != "closed" && status != "all" {
		fail(fmt.Errorf("invalid status %q: want closed or all", status))
		return
	}
	limit := protocol.Deref(msg.Limit)
	if limit < 0 {
		fail(fmt.Errorf("limit %d cannot be negative", limit))
		return
	}
	if limit == 0 {
		limit = defaultGardenPageLimit
	}
	if limit > maxGardenPageLimit {
		limit = maxGardenPageLimit
	}
	var cursorAt time.Time
	var cursorID string
	hasCursor := false
	if before := strings.TrimSpace(protocol.Deref(msg.Before)); before != "" {
		var ok bool
		cursorAt, cursorID, ok = parsePageCursor(before)
		if !ok {
			fail(fmt.Errorf("invalid before cursor %q", before))
			return
		}
		hasCursor = true
	}
	if profileID == "" {
		reply.Success = true
		d.sendToClient(client, reply)
		return
	}
	read, err := d.readGarden(profileID)
	if err != nil {
		if docstore.IsUndeclaredCollection(err) {
			reply.Success = true
			d.sendToClient(client, reply)
			return
		}
		fail(err)
		return
	}
	closedOrder := status == "closed"
	var hasOpen map[string]bool
	if closedOrder {
		hasOpen = openAncestors(read.seeds, read.plots)
	}
	plotID := strings.TrimSpace(protocol.Deref(msg.PlotID))
	bedScope := strings.TrimSpace(protocol.Deref(msg.Bed))
	planter := strings.TrimSpace(protocol.Deref(msg.Planter))
	tender := strings.TrimSpace(protocol.Deref(msg.Tender))
	query := strings.ToLower(strings.TrimSpace(protocol.Deref(msg.Query)))

	var pool []garden.Seed
	switch {
	case planter != "" || tender != "":
		pool = read.seeds
	case plotID != "":
		pool = read.plots.Children(plotID)
	default:
		pool = make([]garden.Seed, 0, len(read.seeds))
		for _, seed := range read.seeds {
			if parentID, ok := garden.ParentOf(seed); ok {
				if _, exists := read.plots.Seed(parentID); exists {
					continue
				}
			}
			pool = append(pool, seed)
		}
	}

	filtered := make([]garden.Seed, 0, len(pool))
	for _, seed := range pool {
		if closedOrder && (!garden.Closed(seed.Status) || hasOpen[seed.ID]) {
			continue
		}
		if planter != "" && seed.Planter.String() != planter {
			continue
		}
		if tender != "" {
			if seed.Tender.IsZero() {
				continue
			}
			view := read.parties.view(seed.Tender)
			if string(view.Ref) != tender && protocol.Deref(view.SessionID) != protocol.SessionID(tender) {
				continue
			}
		}
		if plotID == "" && planter == "" && tender == "" && bedScope != "" {
			if !gardenMatchesBedScope(read, seed, bedScope) {
				continue
			}
		}
		if query != "" {
			if !strings.Contains(strings.ToLower(seed.Title), query) && !strings.Contains(strings.ToLower(seed.ID), query) {
				continue
			}
		}
		filtered = append(filtered, seed)
		switch seed.Status {
		case garden.StatusHarvested:
			reply.Harvested++
		case garden.StatusWithered:
			reply.Withered++
		}
	}
	reply.Total = len(filtered)

	var page []garden.Seed
	var lastAt time.Time
	hasMore := false
	if closedOrder {
		keys := make([]closedPageKey, len(filtered))
		for i, seed := range filtered {
			keys[i] = closedPageKey{idx: i, at: seedStateChangedTime(seed, read.docs[seed.ID])}
		}
		slices.SortFunc(keys, func(a, b closedPageKey) int {
			if cmp := b.at.Compare(a.at); cmp != 0 {
				return cmp
			}
			return strings.Compare(filtered[b.idx].ID, filtered[a.idx].ID)
		})
		start := 0
		if hasCursor {
			start = sort.Search(len(keys), func(i int) bool {
				return afterCursor(keys[i].at, filtered[keys[i].idx].ID, cursorAt, cursorID)
			})
		}
		end := min(start+limit, len(keys))
		page = make([]garden.Seed, end-start)
		for i, key := range keys[start:end] {
			page[i] = filtered[key.idx]
		}
		if end < len(keys) && len(page) > 0 {
			hasMore = true
			lastAt = keys[end-1].at
		}
	} else {
		start := 0
		if hasCursor {
			start = sort.Search(len(filtered), func(i int) bool {
				return afterCursor(read.docs[filtered[i].ID].CreatedAt.UTC(), filtered[i].ID, cursorAt, cursorID)
			})
		}
		end := min(start+limit, len(filtered))
		page = filtered[start:end]
		if end < len(filtered) && len(page) > 0 {
			hasMore = true
			lastAt = read.docs[page[len(page)-1].ID].CreatedAt.UTC()
		}
	}

	reply.Seeds = read.wireWithoutBodies(page)
	if hasMore {
		reply.NextBefore = protocol.Ptr(formatPageCursor(lastAt, page[len(page)-1].ID))
	}
	reply.Success = true
	d.sendToClient(client, reply)
}

func (d *Daemon) handleGardenSeedsLookup(client *wsClient, profileID string, msg *protocol.GardenSeedsLookupMessage) {
	reply := protocol.GardenSeedsLookupResultMessage{
		Event:     protocol.EventGardenSeedsLookupResult,
		RequestID: msg.RequestID,
		Seeds:     []protocol.Seed{},
	}
	if profileID == "" || len(msg.Ids) == 0 {
		reply.Success = true
		d.sendToClient(client, reply)
		return
	}
	if len(msg.Ids) > maxGardenLookupIDs {
		reply.Error = protocol.Ptr(fmt.Sprintf("asked for %d seeds; lookup accepts at most %d", len(msg.Ids), maxGardenLookupIDs))
		d.sendToClient(client, reply)
		return
	}
	all, err := d.rememberedGardenSeeds()
	if err != nil {
		if docstore.IsUndeclaredCollection(err) {
			reply.Success = true
			d.sendToClient(client, reply)
			return
		}
		reply.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, reply)
		return
	}
	seen := make(map[string]bool, len(msg.Ids))
	matchedIDs := make([]string, 0, len(msg.Ids))
	for _, raw := range msg.Ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if seed, ok := all.plots.Seed(id); ok && seed.ProfileID == profileID {
			matchedIDs = append(matchedIDs, id)
		}
	}
	if len(matchedIDs) == 0 {
		reply.Success = true
		d.sendToClient(client, reply)
		return
	}
	read, err := d.readGarden(profileID)
	if err != nil {
		if docstore.IsUndeclaredCollection(err) {
			reply.Success = true
			d.sendToClient(client, reply)
			return
		}
		reply.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, reply)
		return
	}
	found := make([]garden.Seed, 0, len(matchedIDs))
	for _, id := range matchedIDs {
		if seed, ok := read.plots.Seed(id); ok {
			found = append(found, seed)
		}
	}
	if len(found) > 0 {
		reply.Seeds = read.wire(found)
	}
	reply.Success = true
	d.sendToClient(client, reply)
}
