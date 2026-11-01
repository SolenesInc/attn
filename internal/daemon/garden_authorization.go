package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) saveSeedAuthorization(seedID, text string, setter who.Actor, cause protocol.SessionID) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return err
		}
		if seed.Authorization != nil && seed.Authorization.Text == text {
			return nil
		}
		next := seed
		next.Authorization = &garden.Authorization{Text: text, SetBy: setter, SetAt: formatGardenTime(d.gardenTime())}
		entry := seedMoveNote{
			Note:              garden.Note{Seed: seed.ID, Kind: garden.NoteKindAuthorization, Body: text, Author: setter},
			CausedBySessionID: cause,
		}
		d.lockGardenRoles()
		_, _, err = d.writeSeedChange(*schema, next, doc.Rev, seedChange{author: setter, cause: cause, entries: []seedMoveNote{entry}})
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		return err
	}
	return fmt.Errorf("%s was rewritten under all %d attempts to save its authorization; delegate again", seedID, attempts)
}

func (d *Daemon) seedAuthorizationView(a *garden.Authorization) *protocol.SeedAuthorization {
	if a == nil {
		return nil
	}
	return &protocol.SeedAuthorization{Text: a.Text, SetBy: d.actorView(a.SetBy), SetAt: a.SetAt}
}

func (d *Daemon) authorizationSetterName(a garden.Authorization) string {
	if _, isParty := a.SetBy.Party(); !isParty {
		return ""
	}
	return d.actorView(a.SetBy).Name
}
