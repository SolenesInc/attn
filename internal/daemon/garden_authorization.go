package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type authorizationChange struct {
	seed        garden.Seed
	doc         docstore.Document
	changed     bool
	callerTends bool
	typed       *inbox.Item
	withdrawn   *store.InboxKey
}

func (d *Daemon) handleSeedAuthorization(conn net.Conn, msg *protocol.SeedAuthorizationMessage) {
	result, err := d.seedAuthorizationRequest(msg)
	if err != nil {
		d.sendGardenError(conn, "authorization", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedAuthorizationResult: result})
}

func (d *Daemon) handleSeedAuthorizationWS(client *wsClient, msg *protocol.SeedAuthorizationMessage) {
	reply := protocol.SeedAuthorizationResultMessage{
		Event:     protocol.EventSeedAuthorizationResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	result, err := d.seedAuthorizationRequest(msg)
	if err != nil {
		reply.Error = protocol.Ptr(err.Error())
	} else {
		reply.Success, reply.Result = true, result
	}
	d.sendToClient(client, reply)
}

func (d *Daemon) seedAuthorizationRequest(msg *protocol.SeedAuthorizationMessage) (*protocol.SeedAuthorizationResult, error) {
	text := strings.TrimSpace(protocol.Deref(msg.Text))
	clear := protocol.Deref(msg.Clear)
	if text != "" && clear {
		return nil, fmt.Errorf("pass the new words or --clear, not both: attn seed authorization %s \"…\" | --clear", msg.SeedID)
	}
	if text == "" && !clear {
		seed, doc, err := d.readSeed(msg.SeedID)
		if err != nil {
			return nil, err
		}
		wire := d.seedWire(seed, doc)
		result := &protocol.SeedAuthorizationResult{Seed: wire}
		if wire.Authorization != nil {
			result.Update = wire.Authorization.Update
		}
		return result, nil
	}
	source := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	savedAt := time.Now()
	change, err := d.writeSeedAuthorization(msg.SeedID, text, d.actorOfSession(source), source, true)
	if err != nil {
		return nil, err
	}
	result := &protocol.SeedAuthorizationResult{Changed: change.changed}
	if change.callerTends {
		result.CallerTends = protocol.Ptr(true)
	}
	if change.withdrawn != nil {
		d.kickInbox(change.withdrawn.To)
	}
	if change.typed != nil {
		d.deliverSavedInbox(change.typed.To, change.typed.ID, savedAt)
		result.Update = d.authorizationUpdateView(d.partiesNow(), change.typed.ID)
	}
	result.Seed = d.seedWire(change.seed, change.doc)
	if !change.changed && result.Seed.Authorization != nil {
		result.Update = result.Seed.Authorization.Update
	}
	return result, nil
}

func (d *Daemon) saveSeedAuthorization(seedID, text string, setter who.Actor, cause protocol.SessionID) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	_, err := d.writeSeedAuthorization(seedID, text, setter, cause, false)
	return err
}

func (d *Daemon) writeSeedAuthorization(seedID, text string, setter who.Actor, cause protocol.SessionID, typeIntoTender bool) (authorizationChange, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return authorizationChange{}, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return authorizationChange{}, err
		}
		previous := seed.Authorization
		if previous.Words() == text {
			return authorizationChange{seed: seed, doc: doc}, nil
		}
		change := authorizationChange{changed: true}
		next := seed
		next.Authorization = &garden.Authorization{Text: text, SetBy: setter, SetAt: formatGardenTime(d.gardenTime())}
		entryBody := text
		if text == "" {
			entryBody = fmt.Sprintf("Cleared the authorization. These words no longer apply: %q", previous.Text)
		}
		if typeIntoTender {
			if err := d.checkSeedStanding(seed, setter, "set its authorization"); err != nil {
				return authorizationChange{}, err
			}
			standing := gardenStanding{daemon: d, profileID: seed.ProfileID, live: d.sessionActive}
			tender := seed.Tender
			switch {
			case !seed.ClaimHolds(standing.Holds):
			case tender.Actor() == setter:
				change.callerTends = true
				change.withdrawn = &store.InboxKey{To: tender.Address(), Kind: inbox.Authorization, Key: seed.ID}
			default:
				received := d.wordsTenderLastReceived(tender.Address(), previous)
				replaced := received
				if text == "" && replaced == "" {
					replaced = previous.Text
				}
				item := inbox.Item{
					ID: uuid.NewString(), To: tender.Address(), Kind: inbox.Authorization,
					Source: seed.ID, Key: seed.ID, Text: d.authorizationUpdateText(seed, replaced, text, setter),
				}
				change.typed = &item
				next.Authorization.TypedUpdate = item.ID
				next.Authorization.TenderLastReceived = received
			}
		}
		entry := seedMoveNote{
			Note:              garden.Note{Seed: seed.ID, Kind: garden.NoteKindAuthorization, Body: entryBody, Author: setter},
			CausedBySessionID: cause,
		}
		d.lockGardenRoles()
		written, _, err := d.writeSeedChange(*schema, next, doc.Rev, seedChange{author: setter, cause: cause, entries: []seedMoveNote{entry}, typed: change.typed, withdrawn: change.withdrawn})
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return authorizationChange{}, err
		}
		change.seed, change.doc = next, written
		return change, nil
	}
	return authorizationChange{}, fmt.Errorf("%s was rewritten under all %d attempts to save its authorization; try again", seedID, attempts)
}

func (d *Daemon) wordsTenderLastReceived(tender who.Address, current *garden.Authorization) string {
	if current == nil {
		return ""
	}
	if current.TypedUpdate != "" {
		item, found, err := d.store.InboxItem(current.TypedUpdate)
		if err == nil && found && item.ReadAt == "" && item.To == tender {
			return current.TenderLastReceived
		}
	}
	return current.Text
}

func (d *Daemon) authorizationUpdateText(seed garden.Seed, replaced, text string, setter who.Actor) string {
	values := prompts.Values{
		"seed_id": seed.ID, "seed_title": seed.Title, "previous": replaced,
		"set_by": d.authorizationSetterName(garden.Authorization{SetBy: setter}),
	}
	if text == "" {
		return prompts.RenderText("delegation", "authorization-cleared", values)
	}
	values["authorization"] = text
	return prompts.RenderText("delegation", "authorization-update", values)
}

func (d *Daemon) authorizationUpdateView(parties parties, itemID string) *protocol.SeedAuthorizationUpdate {
	if itemID == "" {
		return nil
	}
	item, found, err := d.store.InboxItem(itemID)
	if err != nil || !found {
		return nil
	}
	tender, ok := partyAt(item.To)
	if !ok {
		return nil
	}
	view := &protocol.SeedAuthorizationUpdate{Recipient: parties.view(tender)}
	if item.ReadAt != "" {
		view.TypedAt = protocol.Ptr(item.ReadAt)
		if item.ReadBy != "" {
			view.Recipient.SessionID = protocol.Ptr(protocol.SessionID(item.ReadBy))
		}
		return view
	}
	if holder := d.inboxHolder(item.To); holder != nil {
		view.Recipient.SessionID = protocol.Ptr(holder.ID)
		if holder.State == protocol.SessionStatePendingApproval {
			view.NeedsApproval = protocol.Ptr(true)
		}
	}
	return view
}

func partyAt(a who.Address) (who.Party, bool) {
	if id, ok := a.Session(); ok {
		return who.Session(id), true
	}
	if key, ok := a.Member(); ok {
		return who.Member(key), true
	}
	return who.Party{}, false
}

func (d *Daemon) checkSeedStanding(seed garden.Seed, setter who.Actor, action string) error {
	if _, isParty := setter.Party(); !isParty {
		return nil
	}
	standing := gardenStanding{daemon: d, profileID: seed.ProfileID, live: d.sessionActive}
	held := seed.Tender
	if !seed.ClaimHolds(standing.Holds) || held.Actor() == setter || standing.Oversees(setter, held) {
		return nil
	}
	tender := standing.Name(held)
	return fmt.Errorf("%s is tended by %s; only %s, the sessions above %s in the delegation chain, the chief or the user can %s", seed.ID, tender, tender, tender, action)
}

func (d *Daemon) seedAuthorizationView(parties parties, a *garden.Authorization) *protocol.SeedAuthorization {
	if a == nil {
		return nil
	}
	return &protocol.SeedAuthorization{Text: a.Text, SetBy: parties.actor(a.SetBy), SetAt: a.SetAt, Update: d.authorizationUpdateView(parties, a.TypedUpdate)}
}

func (d *Daemon) authorizationSetterName(a garden.Authorization) string {
	if _, isParty := a.SetBy.Party(); !isParty {
		return ""
	}
	return d.actorView(a.SetBy).Name
}
