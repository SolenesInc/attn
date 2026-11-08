package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type seedBodyConflictError string

func (e seedBodyConflictError) Error() string { return string(e) }

func (d *Daemon) bodyVersionsCollection() (*docstore.CollectionSchema, error) {
	return d.collectionFor(garden.Namespace, garden.CollectionBodyVersions)
}

func (d *Daemon) seedBodiesCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(garden.Namespace, garden.CollectionSeedBodies)
}

func seedBodyCommit(schema docstore.CollectionSchema, id, body string, planting bool) (store.DocumentCommit, error) {
	encoded, err := garden.EncodeSeedBody(body)
	if err != nil {
		return store.DocumentCommit{}, err
	}
	write := store.DocumentWrite{Schema: schema, ID: id, Body: encoded}
	if planting {
		absent := docstore.ExpectAbsent
		write.Expected = &absent
	}
	return store.DocumentCommit{Write: write, Fact: documentChangedFact(garden.Namespace, garden.CollectionSeedBodies, id, false)}, nil
}

func (d *Daemon) adoptInlineSeedBodies() error {
	if d.store == nil {
		return nil
	}
	seedSchema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	inline, err := d.store.ReadInlineSeedBodies(*seedSchema)
	if err != nil {
		return err
	}
	for _, found := range inline {
		if err := d.adoptInlineSeedBody(*seedSchema, found); err != nil {
			return fmt.Errorf("seed %s holds a body written by an older attn build that could not be moved: %w", found.ID, err)
		}
	}
	if len(inline) > 0 {
		d.logf("garden: adopted %d seed bodies written by an older attn build", len(inline))
	}
	return nil
}

func (d *Daemon) adoptInlineSeedBody(seedSchema docstore.CollectionSchema, inline store.InlineSeedBody) error {
	current, err := d.readSeedBody(inline.ID)
	if err != nil {
		return err
	}
	latest, err := d.latestSeedBodyVersion(inline.ID)
	if err != nil {
		return err
	}
	if inline.Body != current && (current != "" || latest.Version > 0) {
		_, err := d.commitSeedBodyEdit(&protocol.SeedEditMessage{
			SeedID: inline.ID, Body: inline.Body, BaseBodySHA256: garden.BodySHA256(current),
			Note: protocol.Ptr("written by an older attn build, which could not see the previous text; that text stays in the previous body version"),
		})
		return err
	}
	seed, doc, err := d.readSeed(inline.ID)
	if err != nil {
		return err
	}
	seedBody, err := seed.Encode()
	if err != nil {
		return err
	}
	commits := []store.DocumentCommit{
		{Write: store.DocumentWrite{Schema: seedSchema, ID: seed.ID, Body: seedBody, Expected: &doc.Rev}, Fact: documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)},
	}
	if inline.Body != current {
		bodySchema, err := d.seedBodiesCollection()
		if err != nil {
			return err
		}
		bodyCommit, err := seedBodyCommit(*bodySchema, seed.ID, inline.Body, false)
		if err != nil {
			return err
		}
		commits = append(commits, bodyCommit)
	}
	written, _, err := d.store.CommitDocumentWritesWithEvents(commits, nil, d.gardenTime())
	if err != nil {
		return err
	}
	d.coalesceSnapshots(func() {
		for i, commit := range commits {
			d.announceCommittedWrite(commit.Fact, written[i].Seq)
		}
	})
	return nil
}

func (d *Daemon) readSeedBody(id string) (string, error) {
	schema, err := d.seedBodiesCollection()
	if err != nil {
		return "", err
	}
	doc, found, err := d.store.GetDocument(*schema, id)
	if err != nil || !found {
		return "", err
	}
	return garden.DecodeSeedBody(doc.Body)
}

func (d *Daemon) withSeedBodies(seeds []protocol.Seed) []protocol.Seed {
	out := slices.Clone(seeds)
	schema, err := d.seedBodiesCollection()
	if err != nil {
		d.logf("garden: reading seed bodies: %v", err)
		return out
	}
	ids := make([]string, len(out))
	for i, seed := range out {
		ids[i] = seed.ID
	}
	docs, err := d.store.ReadDocumentsByID(*schema, ids)
	if err != nil {
		d.logf("garden: reading %d seed bodies: %v", len(ids), err)
		return out
	}
	for i := range out {
		doc, found := docs[out[i].ID]
		if !found {
			continue
		}
		if out[i].Body, err = garden.DecodeSeedBody(doc.Body); err != nil {
			d.logf("garden: seed %s has an unreadable body document: %v", out[i].ID, err)
		}
	}
	return out
}

func (d *Daemon) matchSeedText(words []string) ([]docstore.TextMatch, error) {
	titles, err := d.store.MatchText(garden.SeedsSchema(), words, "")
	if err != nil {
		return nil, err
	}
	bodies, err := d.store.MatchText(garden.SeedBodiesSchema(), words, "")
	if err != nil {
		return nil, err
	}
	return garden.JoinSeedText(titles, bodies, len(words)), nil
}

func (d *Daemon) latestSeedBodyVersion(id string) (garden.BodyVersion, error) {
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace: garden.Namespace, Collection: garden.CollectionBodyVersions,
		Filters: []docstore.Filter{{Field: "seed", Op: docstore.OpEq, Value: id}},
		Sort:    &docstore.Sort{Field: "version", Desc: true}, Limit: 1,
	})
	if err != nil || len(read.Documents) == 0 {
		return garden.BodyVersion{}, err
	}
	return garden.DecodeBodyVersion(read.Documents[0].Body)
}

func (d *Daemon) handleSeedEdit(conn net.Conn, msg *protocol.SeedEditMessage) {
	result, err := d.applySeedBodyEdit(msg)
	if err != nil {
		var conflict seedBodyConflictError
		if errors.As(err, &conflict) {
			err = fmt.Errorf("%w; run attn seed export %s and reapply your change", err, msg.SeedID)
		}
		d.sendGardenError(conn, "edit", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedEditResult: result})
}

func (d *Daemon) handleSeedRollback(conn net.Conn, msg *protocol.SeedRollbackMessage) {
	result, err := d.rollbackSeedBody(msg)
	if err != nil {
		d.sendGardenError(conn, "rollback", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedRollbackResult: result})
}

func (d *Daemon) rollbackSeedBody(msg *protocol.SeedRollbackMessage) (*protocol.SeedRollbackResult, error) {
	seed, _, err := d.readSeed(msg.SeedID)
	if err != nil {
		return nil, err
	}
	latest, err := d.latestSeedBodyVersion(seed.ID)
	if err != nil {
		return nil, err
	}
	if latest.Version == 0 {
		return nil, protocol.Refuse(protocol.RefusalWrongState, "%s has no body edits to roll back", seed.ID)
	}
	version := latest.Version - 1
	if msg.Version != nil {
		version = *msg.Version
	}
	schema, err := d.bodyVersionsCollection()
	if err != nil {
		return nil, err
	}
	target := garden.BodyVersion{Seed: seed.ID, Version: version}
	doc, found, err := d.store.GetDocument(*schema, target.ID())
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, protocol.Refuse(protocol.RefusalNotFound, "%s has no body v%d; available versions: v1 through v%d", seed.ID, version, latest.Version)
	}
	target, err = garden.DecodeBodyVersion(doc.Body)
	if err != nil {
		return nil, err
	}
	body, err := d.readSeedBody(seed.ID)
	if err != nil {
		return nil, err
	}
	pausepoint.At(pausepoint.SeedRollbackRead)
	edit, err := d.applySeedBodyEdit(&protocol.SeedEditMessage{
		SeedID: seed.ID, Body: target.Body, BaseBodySHA256: garden.BodySHA256(body),
		SourceSessionID: msg.SourceSessionID, Note: protocol.Ptr(fmt.Sprintf("rollback to v%d", version)),
	})
	if err != nil {
		var conflict seedBodyConflictError
		if errors.As(err, &conflict) {
			retry := fmt.Sprintf("attn seed rollback %s", seed.ID)
			if msg.Version != nil {
				retry += fmt.Sprintf(" %d", *msg.Version)
			}
			return nil, fmt.Errorf("%w; run %s again", err, retry)
		}
		return nil, err
	}
	return &protocol.SeedRollbackResult{
		Seed: edit.Seed, BodyVersion: edit.BodyVersion, PreviousBytes: edit.PreviousBytes,
		NewBytes: edit.NewBytes, RestoredVersion: version,
	}, nil
}

func (d *Daemon) applySeedBodyEdit(msg *protocol.SeedEditMessage) (*protocol.SeedEditResult, error) {
	if err := garden.ValidateBodyEdit(msg.Body); err != nil {
		return nil, err
	}
	var result *protocol.SeedEditResult
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(_ foregroundCleanupProtection) error {
		var err error
		result, err = d.commitSeedBodyEdit(msg)
		return err
	})
	return result, err
}

func (d *Daemon) commitSeedBodyEdit(msg *protocol.SeedEditMessage) (*protocol.SeedEditResult, error) {
	seedSchema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	versionSchema, err := d.bodyVersionsCollection()
	if err != nil {
		return nil, err
	}
	bodySchema, err := d.seedBodiesCollection()
	if err != nil {
		return nil, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(msg.SeedID)
		if err != nil {
			return nil, err
		}
		current, err := d.readSeedBody(seed.ID)
		if err != nil {
			return nil, err
		}
		if msg.BaseBodySHA256 != garden.BodySHA256(current) {
			return nil, seedBodyConflictError(fmt.Sprintf("%s's body changed before it could be saved", seed.ID))
		}
		latest, err := d.latestSeedBodyVersion(seed.ID)
		if err != nil {
			return nil, err
		}
		versions := []garden.BodyVersion{}
		if latest.Version == 0 {
			latest = garden.BodyVersion{Seed: seed.ID, Version: 1, Body: current}
			versions = append(versions, latest)
		}
		version := garden.BodyVersion{Seed: seed.ID, Version: latest.Version + 1, Body: msg.Body}
		versions = append(versions, version)
		previousBytes := len(current)
		seedBody, err := seed.Encode()
		if err != nil {
			return nil, err
		}
		author := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
		detail := fmt.Sprintf("body v%d: %d → %d bytes", version.Version, previousBytes, len(msg.Body))
		if text := strings.Join(strings.Fields(protocol.Deref(msg.Note)), " "); text != "" {
			detail += " — " + text
		}
		edited, err := seedEvents.Occur(gardenSeedEventModel(), gardenSeedEventVocabulary().BodyEdited, seed.ID, seedEvents.CausePayload{CausedBySessionID: author, CausedBy: d.actorOfSession(author).String()})
		if err != nil {
			return nil, err
		}
		log, err := d.seedChangeLog(seedChange{
			author: d.actorOfSession(author), cause: author,
			events: []seedEvent{{occurrence: edited, detail: detail}},
		})
		if err != nil {
			return nil, err
		}
		absent := docstore.ExpectAbsent
		commits := append([]store.DocumentCommit{
			{Write: store.DocumentWrite{Schema: *seedSchema, ID: seed.ID, Body: seedBody, Expected: &doc.Rev}, Fact: documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)},
		}, log.commits...)
		bodyCommit, err := seedBodyCommit(*bodySchema, seed.ID, msg.Body, false)
		if err != nil {
			return nil, err
		}
		commits = append(commits, bodyCommit)
		for _, saved := range versions {
			body, err := saved.Encode()
			if err != nil {
				return nil, err
			}
			commits = append(commits, store.DocumentCommit{
				Write: store.DocumentWrite{Schema: *versionSchema, ID: saved.ID(), Body: body, Expected: &absent},
				Fact:  documentChangedFact(garden.Namespace, garden.CollectionBodyVersions, saved.ID(), false),
			})
		}
		now := d.gardenTime()
		written, eventSeqs, err := d.store.CommitDocumentWritesWithEvents(commits, log.events, now)
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d.coalesceSnapshots(func() {
			for i, commit := range commits {
				d.announceCommittedWrite(commit.Fact, written[i].Seq)
			}
			announceGardenSeedEvents(d, eventSeqs)
		})
		doc.Body, doc.Rev, doc.UpdatedAt = seedBody, written[0].Rev, now
		return &protocol.SeedEditResult{
			Seed: d.seedWire(seed, doc), BodyVersion: version.Version,
			PreviousBytes: previousBytes, NewBytes: len(msg.Body),
		}, nil
	}
	return nil, seedBodyConflictError(fmt.Sprintf("%s changed during all %d attempts to edit it", msg.SeedID, attempts))
}
