package daemon

import (
	"fmt"
	"net"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

type errRetiredMember struct{ name string }

func (e errRetiredMember) Error() string {
	return fmt.Sprintf("%s is retired; `attn crew restore %s` brings them back", e.name, e.name)
}

func retiredMemberError(member crew.Member) error { return errRetiredMember{name: member.Name} }

func (d *Daemon) retiredCrewMember(key string) (crew.Member, bool) {
	if key == "" {
		return crew.Member{}, false
	}
	member, _, err := d.crewMember(key)
	return member, err == nil && member.Retired
}

func (d *Daemon) retireCrewMember(member crew.Member) (*protocol.CrewRetireResult, error) {
	d.crewWakeMu.Lock()
	retirement, err := d.store.RetireCrewMember(member.Key, time.Now())
	d.crewWakeMu.Unlock()
	if err != nil {
		return nil, err
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	d.announceClosedAsks(retirement.ClosedAsks)
	result := &protocol.CrewRetireResult{AlreadyRetired: retirement.AlreadyRetired, ClosedAsks: append([]string{}, retirement.ClosedAsks...)}
	stopped, err := d.stopMemberPullRequestWatches(member.Key)
	if err != nil {
		return nil, err
	}
	result.RemovedWatches = retirement.RemovedWatches + stopped
	if result.ReleasedSeeds, err = d.releaseRetiredClaims(member); err != nil {
		return nil, err
	}
	sleep, err := d.crewSleep(crewAsker{profileID: member.ProfileID}, crew.KeyRefPrefix+member.ID)
	if err != nil {
		return nil, err
	}
	if !sleep.AlreadyAsleep {
		result.Sleep = sleep
	}
	attempt, err := d.store.InboxAttempt(who.Member(member.Key).Address())
	if err != nil {
		return nil, err
	}
	result.Unread = attempt.Unread
	retired, doc, err := d.crewMember(member.ID)
	if err != nil {
		return nil, err
	}
	result.Member = d.crewMemberWire(retired, doc.Rev)
	return result, nil
}

func (d *Daemon) stopMemberPullRequestWatches(key who.MemberKey) (int, error) {
	address := who.Member(key).Address()
	stopped := 0
	for _, watch := range d.store.PullRequestWatches() {
		if watch.To != address {
			continue
		}
		changed, err := d.store.StopPullRequestWatch(address, watch.PRID)
		if err != nil {
			return stopped, fmt.Errorf("stop watching pull request %s: %w", watch.PRID, err)
		}
		if changed {
			stopped++
			d.publishSessionPullRequestMembershipChanged(watch.PRID, string(watch.SessionID))
		}
	}
	return stopped, nil
}

func (d *Daemon) releaseRetiredClaims(member crew.Member) ([]string, error) {
	read, err := d.readGarden(member.ProfileID)
	if err != nil {
		return nil, err
	}
	released := []string{}
	for _, seed := range garden.Held(read.seeds, member.Key) {
		if seed.Status != garden.StatusGrowing {
			continue
		}
		if _, _, err := d.applySeedTransition(seed.ID, garden.VerbReplant, garden.Ask{Actor: who.Attn(), Force: true}); err != nil {
			return released, fmt.Errorf("release %s: %w", seed.ID, err)
		}
		if _, err := d.appendSeedNote(seed.ID, fmt.Sprintf("%s was retired, so attn released their claim.", member.Name), who.Attn(), "", nil, false, ""); err != nil {
			return released, fmt.Errorf("note the release of %s: %w", seed.ID, err)
		}
		released = append(released, seed.ID)
	}
	return released, nil
}

func (d *Daemon) handleCrewRetire(conn net.Conn, msg *protocol.CrewRetireMessage) {
	member, err := d.crewAskedMember(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		d.sendCrewError(conn, "retire", err)
		return
	}
	result, err := d.retireCrewMember(member)
	if err != nil {
		d.sendCrewError(conn, "retire", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRetireResult: result})
}

func (d *Daemon) handleCrewRestore(conn net.Conn, msg *protocol.CrewRestoreMessage) {
	member, err := d.crewAskedMember(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		d.sendCrewError(conn, "restore", err)
		return
	}
	d.crewWakeMu.Lock()
	restored, err := d.store.RestoreCrewMember(member.ID)
	d.crewWakeMu.Unlock()
	if err != nil {
		d.sendCrewError(conn, "restore", err)
		return
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	d.kickInbox(who.Member(member.Key).Address())
	member, doc, err := d.crewMember(member.ID)
	if err != nil {
		d.sendCrewError(conn, "restore", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewRestoreResult: &protocol.CrewRestoreResult{Member: d.crewMemberWire(member, doc.Rev), AlreadyActive: !restored}})
}
