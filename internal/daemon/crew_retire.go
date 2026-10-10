package daemon

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type crewRetirement struct {
	Member         store.CrewIdentity
	AlreadyRetired bool
	ReleasedSeeds  []string
	RemovedWatches int
	Unread         int
	Sleep          *protocol.CrewSleepResult
}

func (d *Daemon) retireCrewMember(m store.CrewIdentity) (crewRetirement, error) {
	result := crewRetirement{Member: m, AlreadyRetired: m.Retired, ReleasedSeeds: []string{}}
	d.crewWakeMu.Lock()
	removed, err := d.store.RetireCrewMember(m.Key, time.Now())
	d.crewWakeMu.Unlock()
	if err != nil {
		return result, err
	}
	result.Member.Retired = true
	result.RemovedWatches = removed
	d.publishFact(FactCrewUpdated, m.Key.String(), nil)
	result.ReleasedSeeds, err = d.releaseRetiredClaims(m)
	if err != nil {
		return result, err
	}
	sleep, err := d.crewSleep(requestFromApp(m.ProfileID), "member:"+m.Key.String())
	if err != nil {
		return result, err
	}
	if !sleep.AlreadyAsleep {
		result.Sleep = sleep
	}
	attempt, err := d.store.InboxAttempt(who.Member(m.Key).Address())
	result.Unread = attempt.Unread
	return result, err
}

func (d *Daemon) releaseRetiredClaims(m store.CrewIdentity) ([]string, error) {
	released := []string{}
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var err error
		released, err = d.releaseRetiredClaimsProtected(protection, m)
		return err
	})
	return released, err
}

func (d *Daemon) releaseRetiredClaimsProtected(protection foregroundCleanupProtection, m store.CrewIdentity) ([]string, error) {
	released := []string{}
	schema, err := d.seedsCollection()
	if err != nil {
		return released, err
	}
	d.lockGardenRoles()
	defer d.unlockGardenRoles()
	read, err := d.readGardenTo(0, m.ProfileID)
	if err != nil {
		return released, err
	}
	for _, held := range garden.Held(read.seeds, m.Key.String()) {
		seed, doc, err := d.readSeed(held.ID)
		if err != nil {
			return released, err
		}
		if seed.Tender().Member != m.Key.String() || seed.Status != garden.StatusGrowing {
			continue
		}
		next, err := garden.Transition(seed, garden.VerbReplant, garden.Ask{Actor: garden.Tender{Member: crew.DaemonID}, Force: true}, d.sessionExists)
		if err != nil {
			return released, err
		}
		next.StateChangedAt = formatGardenTime(d.gardenTime())
		occurrence, err := gardenSeedLifecycleOccurrence(garden.VerbReplant, next.ID, "", "")
		if err != nil {
			return released, err
		}
		_, _, err = d.writeSeedMoveWithNotesProtected(protection, *schema, next, doc.Rev, []seedEvents.Occurrence{occurrence}, []garden.Note{{Seed: seed.ID, Kind: garden.NoteKindNote, AuthorMember: crew.DaemonID, Body: fmt.Sprintf("%s was retired; attn released its claim.", m.Name)}})
		if err != nil {
			return released, err
		}
		released = append(released, seed.ID)
	}
	return released, nil
}

func (d *Daemon) restoreCrewMember(m store.CrewIdentity) (bool, error) {
	d.crewWakeMu.Lock()
	restored, err := d.store.RestoreCrewMember(m.Key)
	d.crewWakeMu.Unlock()
	if err == nil {
		d.publishFact(FactCrewUpdated, m.Key.String(), nil)
		d.kickInbox(who.Member(m.Key).Address())
	}
	return restored, err
}

func (d *Daemon) handleCrewRetire(conn net.Conn, msg *protocol.CrewRetireMessage) {
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID)
	if err != nil {
		d.sendCrewError(conn, "retire", err)
		return
	}
	m, err := d.resolveMember(r, msg.Member)
	if err != nil {
		d.sendCrewError(conn, "retire", err)
		return
	}
	retired, err := d.retireCrewMember(m)
	if err != nil {
		d.sendCrewError(conn, "retire", err)
		return
	}
	member, doc, err := d.crewMember(m.Key)
	if err != nil {
		d.sendCrewError(conn, "retire", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRetireResult: &protocol.CrewRetireResult{Member: d.crewMemberWire(member, doc.Rev), AlreadyRetired: retired.AlreadyRetired, ReleasedSeeds: retired.ReleasedSeeds, RemovedWatches: retired.RemovedWatches, Unread: retired.Unread, Sleep: retired.Sleep}})
}

func (d *Daemon) handleCrewRestore(conn net.Conn, msg *protocol.CrewRestoreMessage) {
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID)
	if err != nil {
		d.sendCrewError(conn, "restore", err)
		return
	}
	m, err := d.resolveMember(r, msg.Member)
	if err != nil {
		d.sendCrewError(conn, "restore", err)
		return
	}
	restored, err := d.restoreCrewMember(m)
	if err != nil {
		d.sendCrewError(conn, "restore", err)
		return
	}
	member, doc, err := d.crewMember(m.Key)
	if err != nil {
		d.sendCrewError(conn, "restore", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRestoreResult: &protocol.CrewRestoreResult{Member: d.crewMemberWire(member, doc.Rev), AlreadyActive: !restored}})
}
