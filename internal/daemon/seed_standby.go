package daemon

import (
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleSeedStandby(conn net.Conn, msg *protocol.SeedStandbyMessage) {
	result, closeSelf, err := d.seedStandbyRequest(msg)
	if err != nil {
		d.sendGardenError(conn, "standby", err)
		return
	}
	if closeSelf == nil {
		d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedStandbyResult: result})
		return
	}
	closed, closing, refusal := d.beginAgentClose(closeSelf)
	if refusal != nil {
		d.replyAgentMsgError(conn, refusal.code, fmt.Sprintf("%s is on standby, but the session did not close: %s", result.Seed.ID, refusal.message))
		return
	}
	result.Close = closed
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedStandbyResult: result})
	d.finishSessionClose(closed.TargetSessionID, closing)
}

func (d *Daemon) seedStandbyRequest(msg *protocol.SeedStandbyMessage) (*protocol.SeedStandbyResult, *protocol.AgentCloseMessage, error) {
	seedID := strings.TrimSpace(msg.SeedID)
	callerID := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	setter := d.actorOfSession(callerID)
	note := strings.TrimSpace(protocol.Deref(msg.Note))
	closeSelf := protocol.Deref(msg.Close)
	if protocol.Deref(msg.Clear) {
		if note != "" || closeSelf {
			return nil, nil, protocol.Refuse(protocol.RefusalInvalidInput, "--clear takes no note and no --close: attn seed standby %s --clear", seedID)
		}
		result, err := d.writeSeedStandby(seedID, nil, setter)
		return result, nil, err
	}
	standby := garden.Standby{Note: note, SetAt: d.clock.Now().UTC(), SetBy: setter}
	if err := garden.ValidateStandby(standby, seedID); err != nil {
		return nil, nil, err
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, nil, err
	}
	caller := d.store.Get(callerID)
	if caller == nil || seed.Tender.IsZero() || seed.Tender != d.partyOfSession(caller.ID) {
		return nil, nil, protocol.Refuse(protocol.RefusalNotTender, "only %s's tender can put it on standby, from its own session; tend it first: attn seed tend %s", seedID, seedID)
	}
	if err := d.standbyDeliveryRefusal(caller, seed); err != nil {
		return nil, nil, err
	}
	if closeSelf {
		if err := d.unreadStandbyMailRefusal(caller, seed.ID); err != nil {
			return nil, nil, err
		}
	}
	result, err := d.writeSeedStandby(seedID, &standby, setter)
	if err != nil || !closeSelf {
		return result, nil, err
	}
	return result, &protocol.AgentCloseMessage{
		Cmd:             protocol.CmdAgentClose,
		SourceSessionID: caller.ID,
		TargetSessionID: string(caller.ID),
		Reason:          garden.TrimReason(fmt.Sprintf("On standby for %s: %s", seed.ID, note)),
	}, nil
}

func (d *Daemon) standbyDeliveryRefusal(caller *protocol.Session, seed garden.Seed) error {
	if _, session := seed.Tender.Session(); !session || caller.Agent != string(protocol.SessionAgentClaude) {
		return nil
	}
	if d.claudeMod().reachable(d.primaryTerminal(caller.ID)) {
		return nil
	}
	return protocol.Refuse(protocol.RefusalWrongState,
		"this Claude session runs without attn's Claude Code mod, so attn could not deliver %s's mail when it resumes the session; the user turns on %s (attn settings set %s true), and sessions launched after that can go on standby",
		seed.ID, SettingClaudeModEnabled, SettingClaudeModEnabled)
}

func (d *Daemon) writeSeedStandby(seedID string, standby *garden.Standby, setter who.Actor) (*protocol.SeedStandbyResult, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return nil, err
		}
		current, err := d.seedStandby(seed)
		if err != nil {
			return nil, err
		}
		if standby == nil && current == nil {
			return &protocol.SeedStandbyResult{Seed: d.seedWire(seed, doc)}, nil
		}
		if standby != nil && seed.Status != garden.StatusGrowing {
			return nil, protocol.Refuse(protocol.RefusalWrongState,
				"%s is %s; only a growing seed goes on standby, because standby keeps its tender's claim", seed.ID, seed.Status)
		}
		if standby == nil {
			if err := d.checkSeedStanding(seed, setter, "clear its standby"); err != nil {
				return nil, err
			}
		}
		body := "Cleared standby."
		if standby != nil {
			body = "On standby. Seed mail wakes the tender with this note:\n" + standby.Note
		}
		cause, _ := d.sessionOfActor(setter)
		entry := seedMoveNote{
			Note:              garden.Note{Seed: seed.ID, Kind: garden.NoteKindNote, Body: body, Author: setter},
			CausedBySessionID: cause,
		}
		d.lockGardenRoles()
		written, _, err := d.writeSeedChange(*schema, seed, doc.Rev, seedChange{author: setter, cause: cause, entries: []seedMoveNote{entry}, standby: standby, endStandby: standby == nil})
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d.tenderWakes.unblock(seed.ID)
		return &protocol.SeedStandbyResult{Seed: d.seedWire(seed, written), Changed: true}, nil
	}
	return nil, protocol.Refuse(protocol.RefusalConflict,
		"%s was rewritten under all %d attempts to save its standby; read it again with `attn seed show %s`", seedID, attempts, seedID)
}

func (d *Daemon) endStandbyOnDelivery(seedID string) {
	schema, err := d.seedsCollection()
	if err != nil {
		d.logf("standby: end %s after delivery: %v", seedID, err)
		return
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return
		}
		row, err := d.store.SeedStandby(seedID)
		if err != nil || row == nil {
			return
		}
		d.lockGardenRoles()
		_, _, err = d.writeSeedChange(*schema, seed, doc.Rev, seedChange{author: who.Attn(), endStandby: true})
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			d.logf("standby: end %s after delivery: %v", seedID, err)
		}
		return
	}
}

func (d *Daemon) unreadStandbyMailRefusal(session *protocol.Session, goingOnStandby string) error {
	onStandby, err := d.store.SeedsOnStandbyTendedBy(session.ProfileID, d.partyOfSession(session.ID))
	if err != nil {
		return err
	}
	for _, seedID := range append(onStandby, goingOnStandby) {
		attempt, err := d.store.InboxAttempt(who.ToTenderOf(seedID))
		if err != nil {
			return err
		}
		if attempt.Unread > 0 {
			return protocol.Refuse(protocol.RefusalWrongState,
				"%s goes on standby with %d unread item(s) in its mailbox, and a session on standby never closes on mail it has not read; read them with `attn agent inbox`, then close", seedID, attempt.Unread)
		}
	}
	return nil
}

func (g gardenRead) standbyView(seed garden.Seed) *protocol.SeedStandbyView {
	row, found := g.parties.standby[seed.ID]
	if !found {
		return nil
	}
	standby := standbyOf(seed, &row)
	if standby == nil {
		return nil
	}
	view := &protocol.SeedStandbyView{
		Note:  standby.Note,
		SetAt: formatGardenTime(standby.SetAt),
		SetBy: g.parties.actor(standby.SetBy),
	}
	if blocked := g.daemon.tenderWakes.blockedReason(seed.ID); blocked != "" {
		view.WakeBlocked = protocol.Ptr(blocked)
	}
	return view
}

func standbyOf(seed garden.Seed, row *store.SeedStandby) *garden.Standby {
	if row == nil || seed.Status != garden.StatusGrowing || row.Tender != seed.Tender {
		return nil
	}
	return &row.Standby
}

func (d *Daemon) seedStandby(seed garden.Seed) (*garden.Standby, error) {
	row, err := d.store.SeedStandby(seed.ID)
	if err != nil {
		return nil, err
	}
	return standbyOf(seed, row), nil
}
