package daemon

import (
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

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
