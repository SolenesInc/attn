package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

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
	if !protocol.Deref(closed.PendingCompact) {
		d.finishSessionClose(closed.TargetSessionID, closing)
	}
}

func (d *Daemon) seedStandbyRequest(msg *protocol.SeedStandbyMessage) (*protocol.SeedStandbyResult, *protocol.AgentCloseMessage, error) {
	seedID := strings.TrimSpace(msg.SeedID)
	callerID := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	setter := d.actorOfSession(callerID)
	note := strings.TrimSpace(protocol.Deref(msg.Note))
	stay := protocol.Deref(msg.Stay)
	at, in := protocol.Deref(msg.At), protocol.Deref(msg.In)
	if protocol.Deref(msg.Clear) {
		if note != "" || stay || at != "" || in != "" {
			return nil, nil, protocol.Refuse(protocol.RefusalInvalidInput, "--clear takes no note, time or --stay, and leaves the reminder: attn seed standby %s --clear", seedID)
		}
		result, err := d.writeSeedStandby(seedID, nil, nil, setter)
		return result, nil, err
	}
	now := d.clock.Now()
	standby := garden.Standby{Note: note, SetAt: now.UTC(), SetBy: setter}
	if err := garden.ValidateStandby(standby, seedID); err != nil {
		return nil, nil, err
	}
	var reminder *garden.Reminder
	if at != "" || in != "" {
		when, err := reminderTime(seedID, at, in, now)
		if err != nil {
			return nil, nil, err
		}
		reminder = &garden.Reminder{At: when, Message: note, SetAt: now.UTC(), SetBy: setter}
		if err := garden.ValidateReminder(*reminder, seedID); err != nil {
			return nil, nil, err
		}
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, nil, err
	}
	caller := d.store.Get(callerID)
	if caller == nil || seed.Tender.IsZero() || seed.Tender != d.partyOfSession(caller.ID) {
		return nil, nil, protocol.Refuse(protocol.RefusalNotTender, "only %s's tender can put it on standby, from its own session; tend it first: attn seed tend %s", seedID, seedID)
	}
	closeSelf := !stay && d.sessionCloseError(caller.ID) == nil
	if closeSelf {
		if err := d.unreadStandbyMailRefusal(caller, seed.ID); err != nil {
			return nil, nil, err
		}
	}
	result, err := d.writeSeedStandby(seedID, &standby, reminder, setter)
	if err != nil || !closeSelf {
		return result, nil, err
	}
	return result, &protocol.AgentCloseMessage{
		Cmd:             protocol.CmdAgentClose,
		SourceSessionID: caller.ID,
		TargetSessionID: string(caller.ID),
		Reason:          garden.TrimReason(fmt.Sprintf("On standby for %s: %s", seed.ID, note)),
		Compact:         protocol.Ptr(d.compactPathRefusal(caller) == nil),
	}, nil
}

func (d *Daemon) writeSeedStandby(seedID string, standby *garden.Standby, reminder *garden.Reminder, setter who.Actor) (*protocol.SeedStandbyResult, error) {
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
		switch {
		case reminder != nil:
			body = fmt.Sprintf("On standby, with a reminder for %s. Seed mail resumes the tender with this note:\n%s", reminder.At.Format(time.RFC3339), standby.Note)
		case standby != nil:
			body = "On standby. Seed mail resumes the tender with this note:\n" + standby.Note
		}
		cause, _ := d.sessionOfActor(setter)
		entry := seedMoveNote{
			Note:              garden.Note{Seed: seed.ID, Kind: garden.NoteKindNote, Body: body, Author: setter},
			CausedBySessionID: cause,
		}
		d.lockGardenRoles()
		written, _, err := d.writeSeedChange(*schema, seed, doc.Rev, seedChange{author: setter, cause: cause, entries: []seedMoveNote{entry}, standby: standby, endStandby: standby == nil, arm: reminder})
		d.unlockGardenRoles()
		if docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d.unblockTenderResume(seed.ID)
		return &protocol.SeedStandbyResult{Seed: d.seedWire(seed, written), Changed: true}, nil
	}
	return nil, protocol.Refuse(protocol.RefusalConflict,
		"%s was rewritten under all %d attempts to save its standby; read it again with `attn seed show %s`", seedID, attempts, seedID)
}

func (d *Daemon) endStandbyOnceMailRead(addresses ...who.Address) {
	for _, a := range addresses {
		seedID, seedMail := a.TenderOf()
		if !seedMail {
			continue
		}
		attempt, err := d.store.InboxAttempt(a)
		if err != nil {
			d.logf("standby: count the unread mail of %s: %v", seedID, err)
			continue
		}
		if attempt.Unread == 0 {
			d.endStandby(seedID)
		}
	}
}

func (d *Daemon) endStandby(seedID string) {
	schema, err := d.seedsCollection()
	if err != nil {
		d.logf("standby: end %s once its mail was read: %v", seedID, err)
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
			d.logf("standby: end %s once its mail was read: %v", seedID, err)
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
		Note:         standby.Note,
		SetAt:        formatGardenTime(standby.SetAt),
		SetBy:        g.parties.actor(standby.SetBy),
		PullRequests: make([]protocol.SeedWatchedPullRequest, 0, len(g.watches[seed.ID])),
	}
	for _, w := range g.watches[seed.ID] {
		view.PullRequests = append(view.PullRequests, protocol.SeedWatchedPullRequest{
			PRID: w.Watch.PRID, URL: w.Record.URL, State: w.Record.State, ReadinessState: w.Watch.ReadinessState,
		})
	}
	if blocked := g.daemon.tenderResumes.blockedReason(seed.ID); blocked != "" {
		view.ResumeBlocked = protocol.Ptr(blocked)
	}
	return view
}

func (d *Daemon) publishStandbyViewChanged(addresses ...who.Address) {
	for _, a := range addresses {
		if seedID, seedMail := a.TenderOf(); seedMail {
			d.publishFact(FactSeedStandbyViewChanged, seedID, nil)
		}
	}
}

func (d *Daemon) projectStandbyViewChanged(seedID string) {
	row, err := d.store.SeedStandby(seedID)
	if err != nil {
		d.logf("standby: read the standby of %s to refresh the garden: %v", seedID, err)
		return
	}
	if row != nil {
		d.projectGardenSeeds()
	}
}

func (g gardenRead) onStandby(seeds []garden.Seed) []garden.Seed {
	var out []garden.Seed
	for _, seed := range seeds {
		if row, found := g.parties.standby[seed.ID]; found && standbyOf(seed, &row) != nil {
			out = append(out, seed)
		}
	}
	return out
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
