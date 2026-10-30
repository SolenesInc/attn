package daemon

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleSeedSearch(conn net.Conn, msg *protocol.SeedSearchMessage) {
	words, err := garden.SearchWords(msg.Query)
	if err != nil {
		d.sendGardenError(conn, "search", err)
		return
	}
	limit := protocol.Deref(msg.Limit)
	if limit < 0 {
		d.sendGardenError(conn, "search", fmt.Errorf("asked for %d hits; a count of hits cannot be negative", limit))
		return
	}
	if limit > garden.MaxSearchResults {
		d.sendGardenError(conn, "search", fmt.Errorf(
			"max_results=%d, asked for %d; use a smaller limit or narrow the query",
			garden.MaxSearchResults, limit))
		return
	}
	if limit == 0 {
		limit = garden.DefaultSearchResults
	}
	read, err := d.readGarden(protocol.Deref(msg.ProfileID))
	if err != nil {
		d.sendGardenError(conn, "search", err)
		return
	}
	seedRows, err := d.store.MatchText(garden.SeedsSchema(), words, "")
	if err != nil {
		d.sendGardenError(conn, "search", err)
		return
	}
	noteRows, err := d.store.MatchText(garden.NotesSchema(), words, "seed")
	if err != nil {
		d.sendGardenError(conn, "search", err)
		return
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
	for _, hit := range hits {
		spans, err := d.store.TextSpans(hit.Quoted.Schema, hit.Quoted.ID, words)
		if err != nil {
			d.sendGardenError(conn, "search", err)
			return
		}
		text := hit.Seed.Title
		if hit.Where == garden.MatchBody {
			text = hit.Seed.Body
		}
		if hit.Where == garden.MatchLog {
			schema, err := d.collectionFor(hit.Quoted.Schema.Namespace, hit.Quoted.Schema.Collection)
			if err != nil {
				d.sendGardenError(conn, "search", err)
				return
			}
			doc, found, err := d.store.GetDocument(*schema, hit.Quoted.ID)
			if err != nil {
				d.sendGardenError(conn, "search", err)
				return
			}
			if found {
				var note struct {
					Body string `json:"body"`
				}
				if err := json.Unmarshal(doc.Body, &note); err != nil {
					d.sendGardenError(conn, "search", err)
					return
				}
				text = note.Body
			}
		}
		result.Hits = append(result.Hits, protocol.SeedSearchHit{
			Seed:       read.seed(hit.Seed, read.docs[hit.Seed.ID]),
			Where:      hit.Where,
			Snippet:    garden.Snippet(text, spans[hit.Quoted.Field]),
			LastActive: hit.LastActive.UTC().Format(docstore.TimeFormat),
		})
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedSearchResult: result})
}
