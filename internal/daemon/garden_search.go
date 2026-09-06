package daemon

import (
	"errors"
	"fmt"
	"net"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleSeedSearch(conn net.Conn, msg *protocol.SeedSearchMessage) {
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, "search", err)
		return
	}
	terms, err := garden.SearchTerms(msg.Query)
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
			"max_results=%d, asked for %d; that is past the whole garden, so no query needs it",
			garden.MaxSearchResults, limit))
		return
	}
	if limit == 0 {
		limit = garden.DefaultSearchResults
	}
	read, err := d.readGardenTo(0)
	if err != nil {
		d.sendGardenError(conn, "search", err)
		return
	}
	logs, err := d.readGardenLogs()
	if err != nil {
		d.sendGardenError(conn, "search", err)
		return
	}
	subjects := make([]garden.SearchSubject, 0, len(read.seeds))
	for _, seed := range read.seeds {
		subjects = append(subjects, garden.SearchSubject{Seed: seed, Log: logs[seed.ID]})
	}
	hits, matched := garden.Search(subjects, terms, limit)
	result := &protocol.SeedSearchResult{
		Hits:     make([]protocol.SeedSearchHit, 0, len(hits)),
		Matched:  matched,
		Limit:    limit,
		Searched: len(read.seeds),
	}
	for _, hit := range hits {
		result.Hits = append(result.Hits, protocol.SeedSearchHit{
			Seed:    seedToProtocol(hit.Seed, read.docs[hit.Seed.ID], read.ready[hit.Seed.ID]),
			Where:   hit.Where,
			Snippet: hit.Snippet,
		})
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedSearchResult: result})
}

func (d *Daemon) readGardenLogs() (map[string][]string, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	page := d.gardenNotePageSize
	if page <= 0 {
		page = docstore.MaxLimit
	}
	logs := map[string][]string{}
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace:  garden.Namespace,
			Collection: garden.CollectionNotes,
			Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
			Limit:      page,
			After:      after,
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range read.Documents {
			note, err := garden.DecodeNote(doc.Body)
			if err != nil {
				d.logf("garden: note %s has an unreadable body: %v", doc.ID, err)
				continue
			}
			logs[note.Seed] = append(logs[note.Seed], note.Body)
		}
		if len(read.Documents) < page {
			return logs, nil
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}
