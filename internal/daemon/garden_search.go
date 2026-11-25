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
	"github.com/victorarias/attn/internal/setting"
)

const maxGardenSearchTerms = 32

func (d *Daemon) seedSearch(msg *seedSearchRequest) (*protocol.SeedSearchResult, error) {
	words, err := garden.SearchWords(msg.Query)
	if err != nil {
		return nil, gardenFailure("search", err)
	}
	limit := protocol.Deref(msg.Limit)
	if limit < 0 {
		return nil, gardenFailure("search", protocol.Refuse(protocol.RefusalInvalidInput, "asked for %d hits; a count of hits cannot be negative", limit))
	}
	if limit > garden.MaxSearchResults {
		return nil, gardenFailure("search", protocol.Refuse(protocol.RefusalLimit,
			"max_results=%d, asked for %d; use a smaller limit or narrow the query",
			garden.MaxSearchResults, limit))
	}
	if limit == 0 {
		limit = garden.DefaultSearchResults
	}
	read, err := d.readGarden(protocol.Deref(msg.ProfileID))
	if err != nil {
		return nil, gardenFailure("search", err)
	}
	seedRows, err := d.matchSeedText(words)
	if err != nil {
		return nil, gardenFailure("search", err)
	}
	noteRows, err := d.store.MatchText(garden.NotesSchema(), words, "seed")
	if err != nil {
		return nil, gardenFailure("search", err)
	}
	ranked := garden.Rank(words, seedRows, noteRows, read.seeds)
	hits := ranked.Hits[:min(limit, len(ranked.Hits))]
	result := &protocol.SeedSearchResult{
		Hits:     make([]protocol.SeedSearchHit, 0, len(hits)),
		Matched:  ranked.Matched,
		Partial:  ranked.Partial,
		Limit:    limit,
		Searched: len(read.seeds),
	}
	quotes := map[string]map[string]docstore.TextQuote{}
	ids := map[string][]string{}
	schemas := map[string]docstore.CollectionSchema{}
	for _, hit := range hits {
		key := docstore.Target(hit.Quoted.Schema.Namespace, hit.Quoted.Schema.Collection)
		ids[key] = append(ids[key], hit.Quoted.ID)
		schemas[key] = hit.Quoted.Schema
	}
	for key, schema := range schemas {
		quotes[key], err = d.store.TextSpans(schema, ids[key], words)
		if err != nil {
			return nil, gardenFailure("search", err)
		}
	}
	for _, hit := range hits {
		key := docstore.Target(hit.Quoted.Schema.Namespace, hit.Quoted.Schema.Collection)
		quote, found := quotes[key][hit.Quoted.ID]
		snippet := ""
		if found {
			snippet = garden.Snippet(quote.Text[hit.Quoted.Field], quote.Spans[hit.Quoted.Field])
		}

		result.Hits = append(result.Hits, protocol.SeedSearchHit{
			Seed:       read.seed(hit.Seed, read.docs[hit.Seed.ID]),
			Where:      hit.Where,
			Snippet:    snippet,
			LastActive: hit.LastActive.UTC().Format(docstore.TimeFormat),
		})
	}
	return result, nil
}

type ftsNoteMatch struct {
	id        string
	createdAt time.Time
	carries   []bool
}

type ftsSeedMatch struct {
	titleCarries []bool
	bodyCarries  []bool
	notes        []ftsNoteMatch
	bm25         float64
	lastActive   time.Time
}

type gardenSearchCandidate struct {
	seed             garden.Seed
	tier             int
	titleAt          int
	bm25             float64
	lastActive       time.Time
	rrf              float64
	where            string
	idHit            bool
	hasTitleFTS      bool
	bodyHit          bool
	pulseHit         bool
	logNoteID        string
	unexplainedTerms []string
	inSubtree        bool
}

func ftsWordsForTerm(term string) ([]string, bool) {
	if strings.HasPrefix(term, "s-") {
		suffix := term[2:]
		if len(suffix) == 6 && garden.ValidateID(term) == nil {
			return []string{suffix}, true
		}
		return nil, true
	}
	ws := docstore.Words(term)
	return ws, len(ws) > 1
}

func normalizeTerms(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func openBlockerCounts(seeds []garden.Seed) map[string]int {
	blockers := map[string]int{}
	for _, seed := range seeds {
		if garden.Closed(seed.Status) {
			continue
		}
		for _, edge := range seed.Edges {
			if edge.Kind == garden.EdgeBlocks {
				blockers[edge.To]++
			}
		}
	}
	return blockers
}

func satisfiesSearchLens(read gardenRead, seed garden.Seed, isValues []string, blockers map[string]int) bool {
	closed := garden.Closed(seed.Status)
	if len(isValues) == 0 {
		return !closed
	}
	for _, value := range isValues {
		switch value {
		case "any":
			return true
		case "open":
			if !closed {
				return true
			}
		case "closed":
			if closed {
				return true
			}
		case "ready":
			if read.ready[seed.ID] && !closed {
				return true
			}
		case "blocked":
			if !closed && blockers[seed.ID] > 0 {
				return true
			}
		case "parked":
			if seed.Status == garden.StatusParked {
				return true
			}
		case "standby":
			if read.standbyView(seed) != nil && !closed {
				return true
			}
		}
	}
	return false
}

func matchesBedTokens(read gardenRead, seed garden.Seed, beds []string) bool {
	root := gardenRootOf(seed, read.plots)
	isAuto := root.AutomationID > 0
	effBedID, effBedName := read.rootBed(root)
	bedName := strings.ToLower(effBedName)
	bedID := strings.ToLower(effBedID)
	bedSlug := ""
	if effBedID != "" {
		if bed, ok := read.beds[effBedID]; ok {
			bedSlug = strings.ToLower(bed.Slug)
		}
	}
	for _, want := range beds {
		switch want {
		case "unassigned", "none":
			if !isAuto && bedID == "" && bedName == "" {
				return true
			}
		case "automations", "auto":
			if isAuto {
				return true
			}
		default:
			if !isAuto && ((bedName != "" && strings.Contains(bedName, want)) ||
				(bedSlug != "" && strings.Contains(bedSlug, want)) ||
				(bedID != "" && bedID == want)) {
				return true
			}
		}
	}
	return false
}

func toProtocolRanges(spans [][2]int) []protocol.GardenSearchRange {
	if len(spans) == 0 {
		return nil
	}
	out := make([]protocol.GardenSearchRange, len(spans))
	for i, span := range spans {
		out[i] = protocol.GardenSearchRange{Start: span[0], End: span[1]}
	}
	return out
}

func carriesAll(carried []bool, indices []int) bool {
	if len(indices) == 0 || len(carried) == 0 {
		return false
	}
	for _, idx := range indices {
		if idx >= len(carried) || !carried[idx] {
			return false
		}
	}
	return true
}

func (d *Daemon) handleGardenSearch(client *wsClient, profileID string, msg *protocol.GardenSearchMessage) {
	reply := protocol.GardenSearchResultMessage{
		Event:     protocol.EventGardenSearchResult,
		RequestID: msg.RequestID,
		Hits:      []protocol.GardenSearchHit{},
	}
	fail := func(err error) {
		reply.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, reply)
	}
	offset := protocol.Deref(msg.Offset)
	if offset < 0 {
		fail(fmt.Errorf("offset %d cannot be negative", offset))
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
	terms := normalizeTerms(msg.Terms)
	if len(terms) > maxGardenSearchTerms {
		terms = terms[:maxGardenSearchTerms]
	}
	isValues := normalizeTerms(msg.Is)
	tenders := normalizeTerms(msg.Tenders)
	beds := normalizeTerms(msg.Beds)
	bedScope := strings.TrimSpace(protocol.Deref(msg.BedScope))
	plotID := strings.TrimSpace(protocol.Deref(msg.PlotID))
	wide := protocol.Deref(msg.Wide)

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

	var words []string
	var singleWords []string
	wordIndex := map[string]int{}
	singleSeen := map[string]bool{}
	termWordIndices := make([][]int, len(terms))
	termCompound := make([]bool, len(terms))
	if len(terms) > 0 {
		for i, term := range terms {
			ws, compound := ftsWordsForTerm(term)
			termCompound[i] = compound
			for _, w := range ws {
				idx, exists := wordIndex[w]
				if !exists {
					idx = len(words)
					wordIndex[w] = idx
					words = append(words, w)
				}
				termWordIndices[i] = append(termWordIndices[i], idx)
				if !compound && !singleSeen[w] {
					singleSeen[w] = true
					singleWords = append(singleWords, w)
				}
			}
		}
	}

	ftsBySeed := map[string]*ftsSeedMatch{}
	if len(words) > 0 {
		seedRows, err := d.matchSeedText(words)
		if err != nil {
			fail(err)
			return
		}
		noteRows, err := d.store.MatchText(garden.NotesSchema(), words, "seed")
		if err != nil {
			fail(err)
			return
		}
		ensure := func(id string) *ftsSeedMatch {
			m := ftsBySeed[id]
			if m == nil {
				m = &ftsSeedMatch{
					titleCarries: make([]bool, len(words)),
					bodyCarries:  make([]bool, len(words)),
				}
				ftsBySeed[id] = m
			}
			return m
		}
		for _, row := range seedRows {
			if _, ok := read.plots.Seed(row.ID); !ok {
				continue
			}
			m := ensure(row.ID)
			if row.CreatedAt.After(m.lastActive) {
				m.lastActive = row.CreatedAt
			}
			for i, carried := range row.Carries[0] {
				if carried {
					m.titleCarries[i] = true
				}
			}
			for i, carried := range row.Carries[1] {
				if carried {
					m.bodyCarries[i] = true
				}
			}
			m.bm25 = 10*row.Score[0] + 3*row.Score[1]
		}
		for _, row := range noteRows {
			if _, ok := read.plots.Seed(row.Key); !ok {
				continue
			}
			m := ensure(row.Key)
			if row.CreatedAt.After(m.lastActive) {
				m.lastActive = row.CreatedAt
			}
			m.notes = append(m.notes, ftsNoteMatch{
				id:        row.ID,
				createdAt: row.CreatedAt,
				carries:   row.Carries[0],
			})
			if row.Score[0] > 0 {
				m.bm25 += row.Score[0]
			}
		}
	}

	var blockers map[string]int
	if slices.Contains(isValues, "blocked") {
		blockers = openBlockerCounts(read.seeds)
	}
	v2Enabled := d.on(setting.GardenV2Enabled)
	var subtree map[string]bool
	if plotID != "" {
		inPlot := read.plots.InPlot(plotID)
		subtree = make(map[string]bool, len(inPlot))
		for _, seed := range inPlot {
			if seed.ID != plotID {
				subtree[seed.ID] = true
			}
		}
	}

	var matched []gardenSearchCandidate
	for _, seed := range read.seeds {
		tenderName := ""
		if !seed.Tender.IsZero() {
			view := read.parties.view(seed.Tender)
			tenderName = view.Name
			if tenderName == "" {
				tenderName = string(view.Ref)
			}
		}
		tenderLower := strings.ToLower(tenderName)
		if len(tenders) > 0 {
			hitTender := false
			if tenderLower != "" {
				for _, want := range tenders {
					if strings.Contains(tenderLower, want) {
						hitTender = true
						break
					}
				}
			}
			if !hitTender {
				continue
			}
		}
		if len(beds) > 0 {
			if !matchesBedTokens(read, seed, beds) {
				continue
			}
		} else if bedScope != "" {
			if !gardenMatchesBedScope(read, seed, bedScope) {
				continue
			}
		}

		cand := gardenSearchCandidate{
			seed:      seed,
			tier:      2,
			where:     "title",
			inSubtree: subtree == nil || subtree[seed.ID],
		}
		var fts *ftsSeedMatch
		if len(terms) > 0 {
			idLower := strings.ToLower(seed.ID)
			titleLower := strings.ToLower(seed.Title)
			pulseLower := ""
			if v2Enabled && seed.Pulse != "" {
				pulseLower = strings.ToLower(seed.Pulse)
			}
			fts = ftsBySeed[seed.ID]
			if fts != nil {
				cand.bm25 = fts.bm25
			}

			allID, allTitle, allTender := true, true, tenderLower != ""
			anyID, anyTitle := false, false
			unexplainedBody, unexplainedPulse := 0, 0
			var unexplainedIdx []int
			allHit := true
			earliestTitle := -1
			for i, term := range terms {
				hitID := strings.Contains(idLower, term)
				if hitID {
					anyID = true
				} else {
					allID = false
				}
				hitTitleSub := strings.Contains(titleLower, term)
				if hitTitleSub {
					pos := strings.Index(titleLower, term)
					if earliestTitle < 0 || pos < earliestTitle {
						earliestTitle = pos
					}
				}
				hitTitleFTS := !termCompound[i] && fts != nil && carriesAll(fts.titleCarries, termWordIndices[i])
				if hitTitleFTS {
					cand.hasTitleFTS = true
				}
				hitTitle := hitTitleSub || hitTitleFTS
				if hitTitle {
					anyTitle = true
				} else {
					allTitle = false
				}
				hitTender := tenderLower != "" && strings.Contains(tenderLower, term)
				if !hitTender {
					allTender = false
				}
				hitPulse := pulseLower != "" && strings.Contains(pulseLower, term)
				hitBody := fts != nil && carriesAll(fts.bodyCarries, termWordIndices[i])
				hitLog := false
				if fts != nil && len(termWordIndices[i]) > 0 {
					for _, note := range fts.notes {
						if carriesAll(note.carries, termWordIndices[i]) {
							hitLog = true
							break
						}
					}
				}
				if !hitID && !hitTitle && !hitTender && !hitPulse && !hitBody && !hitLog {
					allHit = false
					break
				}
				if !hitID && !hitTitle && !hitTender {
					unexplainedIdx = append(unexplainedIdx, i)
					if hitBody {
						unexplainedBody++
					}
					if hitPulse {
						unexplainedPulse++
					}
				}
			}
			if !allHit {
				continue
			}
			if allID {
				cand.tier = 0
				cand.idHit = true
				cand.where = "id"
			} else if allTitle {
				cand.tier = 1
				if earliestTitle >= 0 {
					cand.titleAt = earliestTitle
				} else {
					cand.titleAt = len(seed.Title)
				}
				cand.where = "title"
			} else if allTender {
				cand.tier = 2
				cand.where = "tender"
			} else {
				cand.tier = 3
				for _, idx := range unexplainedIdx {
					cand.unexplainedTerms = append(cand.unexplainedTerms, terms[idx])
				}
				bestNoteID := ""
				bestNoteCov := 0
				var bestNoteAt time.Time
				if fts != nil && len(unexplainedIdx) > 0 {
					for _, note := range fts.notes {
						cov := 0
						for _, idx := range unexplainedIdx {
							if carriesAll(note.carries, termWordIndices[idx]) {
								cov++
							}
						}
						if cov > bestNoteCov || (cov > 0 && cov == bestNoteCov && note.createdAt.After(bestNoteAt)) {
							bestNoteCov = cov
							bestNoteAt = note.createdAt
							bestNoteID = note.id
						}
					}
				}
				switch {
				case len(unexplainedIdx) == 0:
					switch {
					case anyID:
						cand.where = "id"
					case anyTitle:
						cand.where = "title"
					default:
						cand.where = "tender"
					}
				case unexplainedBody > 0 && unexplainedBody >= unexplainedPulse && unexplainedBody >= bestNoteCov:
					cand.where = garden.MatchBody
					cand.bodyHit = true
				case unexplainedPulse > 0 && unexplainedPulse >= bestNoteCov:
					cand.where = "pulse"
					cand.pulseHit = true
				case bestNoteID != "":
					cand.where = garden.MatchLog
					cand.logNoteID = bestNoteID
				case unexplainedBody > 0:
					cand.where = garden.MatchBody
					cand.bodyHit = true
				case unexplainedPulse > 0:
					cand.where = "pulse"
					cand.pulseHit = true
				default:
					cand.where = "title"
				}
			}
		}

		inScope := plotID == "" || wide || cand.inSubtree
		if len(isValues) == 0 && garden.Closed(seed.Status) {
			if inScope {
				reply.HiddenClosed++
			}
			continue
		}
		if !satisfiesSearchLens(read, seed, isValues, blockers) {
			continue
		}
		if plotID != "" && !wide && !cand.inSubtree {
			reply.Outside++
			continue
		}
		if plotID != "" && wide && cand.inSubtree {
			reply.InPlot++
		}
		if len(terms) > 0 {
			cand.lastActive = seedStateChangedTime(seed, read.docs[seed.ID])
			if fts != nil && fts.lastActive.After(cand.lastActive) {
				cand.lastActive = fts.lastActive
			}
		}
		matched = append(matched, cand)
	}

	if len(terms) > 0 && len(matched) > 1 {
		rankSearchCandidates(matched)
	}

	reply.Total = len(matched)
	if offset >= len(matched) {
		reply.Success = true
		d.sendToClient(client, reply)
		return
	}
	end := min(offset+limit, len(matched))
	page := matched[offset:end]
	if end < len(matched) {
		reply.NextOffset = protocol.Ptr(end)
	}

	var titleIDs, bodyIDs, noteIDs []string
	if len(words) > 0 {
		for _, cand := range page {
			if cand.hasTitleFTS && len(singleWords) > 0 {
				titleIDs = append(titleIDs, cand.seed.ID)
			}
			if cand.bodyHit {
				bodyIDs = append(bodyIDs, cand.seed.ID)
			}
			if cand.logNoteID != "" {
				noteIDs = append(noteIDs, cand.logNoteID)
			}
		}
	}
	var titleQuotes, bodyQuotes, noteQuotes map[string]docstore.TextQuote
	if len(titleIDs) > 0 {
		if titleQuotes, err = d.store.TextSpans(garden.SeedsSchema(), titleIDs, singleWords); err != nil {
			fail(err)
			return
		}
	}
	if len(bodyIDs) > 0 {
		if bodyQuotes, err = d.store.TextSpans(garden.SeedBodiesSchema(), bodyIDs, words); err != nil {
			fail(err)
			return
		}
	}
	if len(noteIDs) > 0 {
		if noteQuotes, err = d.store.TextSpans(garden.NotesSchema(), noteIDs, words); err != nil {
			fail(err)
			return
		}
	}

	reply.Hits = make([]protocol.GardenSearchHit, 0, len(page))
	for _, cand := range page {
		wireSeed := read.seedWithoutBody(cand.seed, read.docs[cand.seed.ID])
		hit := protocol.GardenSearchHit{
			Seed:  wireSeed,
			Where: cand.where,
			IDHit: cand.idHit,
		}
		if parentID, ok := garden.ParentOf(cand.seed); ok {
			if parent, exists := read.plots.Seed(parentID); exists {
				hit.PlotTitle = protocol.Ptr(parent.Title)
			}
		}
		if len(terms) > 0 {
			var titleSpans [][2]int
			if cand.hasTitleFTS {
				if q, ok := titleQuotes[cand.seed.ID]; ok && len(q.Spans) > 0 {
					titleSpans = append(titleSpans, q.Spans[0]...)
				}
			}
			titleSpans = append(titleSpans, garden.TextRanges(cand.seed.Title, terms)...)
			hit.TitleRanges = toProtocolRanges(garden.MergeSpans(titleSpans))
		}
		if cand.tier == 3 {
			snippetTerms := cand.unexplainedTerms
			if len(snippetTerms) == 0 {
				snippetTerms = terms
			}
			quoteSnippet := func(q docstore.TextQuote, found bool) {
				if !found || len(q.Text) == 0 {
					return
				}
				spans := garden.TextRanges(q.Text[0], snippetTerms)
				if len(spans) == 0 && len(q.Spans) > 0 {
					spans = q.Spans[0]
				}
				text, proj := garden.SnippetWithSpans(q.Text[0], spans)
				if text != "" {
					hit.Snippet = protocol.Ptr(text)
					hit.SnippetRanges = toProtocolRanges(proj)
				}
			}
			switch {
			case cand.bodyHit:
				q, ok := bodyQuotes[cand.seed.ID]
				quoteSnippet(q, ok)
			case cand.pulseHit && cand.seed.Pulse != "":
				spans := garden.TextRanges(cand.seed.Pulse, snippetTerms)
				text, proj := garden.SnippetWithSpans(cand.seed.Pulse, spans)
				if text != "" {
					hit.Snippet = protocol.Ptr(text)
					hit.SnippetRanges = toProtocolRanges(proj)
				}
			case cand.logNoteID != "":
				q, ok := noteQuotes[cand.logNoteID]
				quoteSnippet(q, ok)
			}
		}
		reply.Hits = append(reply.Hits, hit)
	}
	reply.Success = true
	d.sendToClient(client, reply)
}

func rankSearchCandidates(candidates []gardenSearchCandidate) {
	byBM25 := make([]int, len(candidates))
	byTime := make([]int, len(candidates))
	for i := range candidates {
		byBM25[i] = i
		byTime[i] = i
	}
	sort.SliceStable(byBM25, func(i, j int) bool {
		return candidates[byBM25[i]].bm25 > candidates[byBM25[j]].bm25
	})
	for rank, idx := range byBM25 {
		candidates[idx].rrf = 1 / float64(10+rank)
	}
	sort.SliceStable(byTime, func(i, j int) bool {
		return candidates[byTime[i]].lastActive.After(candidates[byTime[j]].lastActive)
	})
	for rank, idx := range byTime {
		candidates[idx].rrf += 1 / float64(10+rank)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.tier == 1 && a.titleAt != b.titleAt {
			return a.titleAt < b.titleAt
		}
		aOpen, bOpen := !garden.Closed(a.seed.Status), !garden.Closed(b.seed.Status)
		if aOpen != bOpen {
			return aOpen
		}
		return a.rrf > b.rrf
	})
}
