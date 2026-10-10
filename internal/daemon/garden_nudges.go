package daemon

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var errRemoteGardenTender = errors.New("garden notifications are home-only")

func (d *Daemon) seedUnblocked(seedID string) ([]garden.Seed, []protocol.Seed) {
	if d.store == nil {
		return nil, nil
	}
	read, err := d.readGardenTo(0)
	if err != nil {
		d.logf("garden bell: reading the graph to see what %s unblocked: %v", seedID, err)
		return nil, nil
	}
	unblocked := garden.Unblocks(read.seeds, seedID)
	return unblocked, read.wire(unblocked)
}

func (d *Daemon) seedTenderMember(seed garden.Seed) (crew.Member, bool, error) {
	text := seed.Tender().Member
	identity, found, err := d.store.CrewKeyed(seed.ProfileID, text)
	if err != nil {
		return crew.Member{}, false, err
	}
	if !found {
		identity, found, err = d.store.CrewNamed(seed.ProfileID, text)
	}
	if err != nil || !found {
		return crew.Member{}, false, err
	}
	member, _, err := d.crewMember(identity.Key)
	return member, err == nil, err
}

func (d *Daemon) localGardenTenderSession(seed garden.Seed) (string, error) {
	tender := seed.Tender()
	sessionID := protocol.TrimID(tender.Session)
	if sessionID == "" && tender.Member != "" {
		member, found, err := d.seedTenderMember(seed)
		if err != nil {
			return "", err
		}
		if found && d.crewBindingLive(member) {
			sessionID = member.BindingSession
		}
	}

	if sessionID == "" {
		return "", nil
	}
	if d.store != nil && (d.store.Get(sessionID) != nil || d.store.DelegationSessionReserved(sessionID)) {
		profileID, err := d.store.GardenSessionProfileID(sessionID)
		if err != nil {
			return "", err
		}
		if profileID != seed.ProfileID {
			return "", nil
		}
		return string(sessionID), nil
	}
	if d.hubManager != nil {
		if endpointID, remote := d.hubManager.EndpointIDForSession(sessionID); remote {
			return "", fmt.Errorf("%w; cannot notify tender session %s on outpost %s", errRemoteGardenTender, sessionID, endpointID)
		}
	}
	return "", nil
}

func (d *Daemon) handleSeedWatch(conn net.Conn, msg *protocol.SeedWatchMessage) {
	verb := "watch"
	watching := !protocol.Deref(msg.Unwatch)
	if !watching {
		verb = "unwatch"
	}
	if err := d.requireHome(garden.Surface); err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	seed, _, err := d.readSeed(msg.SeedID)
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	sessionID := protocol.TrimID(msg.SourceSessionID)
	if sessionID == "" || d.store.Get(sessionID) == nil {
		d.sendGardenError(conn, verb, fmt.Errorf("watching is for a live attn session; pass --session or run it inside one"))
		return
	}
	result, err := d.setSeedWatch(sessionID, seed.ID, watching)
	if err != nil {
		d.sendGardenError(conn, verb, err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedWatchResult: result})
}

func (d *Daemon) setSeedWatch(sessionID protocol.SessionID, seedID string, watching bool) (*protocol.SeedWatchResult, error) {
	d.lockGardenRoles()
	defer d.unlockGardenRoles()
	changed, err := d.store.SetGardenSeedWatch(sessionID, seedID, watching, time.Now())
	if err != nil {
		return nil, err
	}
	if !watching {
		if err := d.discardUncoveredSeedBells(sessionID); err != nil {
			return nil, fmt.Errorf("subscription removed, but queued updates could not be cleared; retry unwatch: %w", err)
		}
	}
	coverage, err := d.seedWatchCoverage(sessionID, seedID)
	if err != nil {
		return nil, err
	}
	return &protocol.SeedWatchResult{SeedID: seedID, Watching: len(coverage) > 0, WatchingVia: coverage, Changed: changed}, nil
}

type gardenSubscriptions struct {
	parents map[string]string
	seeds   map[string]garden.Seed
	watches map[string][]store.GardenSeedWatch
}

func newGardenSubscriptions(seeds []garden.Seed, watches []store.GardenSeedWatch) gardenSubscriptions {
	subscriptions := gardenSubscriptions{
		parents: make(map[string]string, len(seeds)),
		seeds:   make(map[string]garden.Seed, len(seeds)),
		watches: map[string][]store.GardenSeedWatch{},
	}
	for _, seed := range seeds {
		subscriptions.seeds[seed.ID] = seed
		subscriptions.parents[seed.ID] = ""
		for _, edge := range seed.Edges {
			if edge.Kind == garden.EdgePartOf {
				subscriptions.parents[seed.ID] = edge.To
				break
			}
		}
	}
	for _, watch := range watches {
		subscriptions.watches[watch.SeedID] = append(subscriptions.watches[watch.SeedID], watch)
	}
	return subscriptions
}

func (s gardenSubscriptions) coverage(seedID string) map[protocol.SessionID][]string {
	covered, _ := s.coverageChecked(seedID)
	return covered
}

func (s gardenSubscriptions) coverageChecked(seedID string) (map[protocol.SessionID][]string, error) {
	covered := map[protocol.SessionID][]string{}
	seen := map[string]bool{}
	for at := seedID; at != ""; {
		if seen[at] {
			return nil, fmt.Errorf("garden seed ancestry cycle reaches %s while resolving %s", at, seedID)
		}
		parent, known := s.parents[at]
		if !known {
			break
		}
		seen[at] = true
		for _, watch := range s.watches[at] {
			covered[watch.WatcherSessionID] = append(covered[watch.WatcherSessionID], at)
		}
		at = parent
	}
	for _, seeds := range covered {
		sort.Strings(seeds)
	}
	return covered, nil
}

func (d *Daemon) readGardenSubscriptions() (gardenSubscriptions, error) {
	read, err := d.readGardenTo(0)
	if err != nil {
		return gardenSubscriptions{}, err
	}
	watches, err := d.store.GardenSeedWatches()
	if err != nil {
		return gardenSubscriptions{}, err
	}
	eligible := watches[:0]
	for _, watch := range watches {
		seed, ok := read.docs[watch.SeedID]
		if !ok {
			continue
		}
		owner, err := garden.Decode(seed.Body)
		if err != nil {
			return gardenSubscriptions{}, err
		}
		profileID, err := d.store.GardenSessionProfileID(watch.WatcherSessionID)
		if err != nil {
			return gardenSubscriptions{}, err
		}
		if profileID == owner.ProfileID {
			eligible = append(eligible, watch)
		}
	}
	return newGardenSubscriptions(read.seeds, eligible), nil
}

func (d *Daemon) seedWatchCoverage(sessionID protocol.SessionID, seedID string) ([]string, error) {
	if protocol.TrimID(sessionID) == "" {
		return []string{}, nil
	}
	subscriptions, err := d.readGardenSubscriptions()
	if err != nil {
		return nil, err
	}
	coverage := subscriptions.coverage(seedID)[sessionID]
	if coverage == nil {
		coverage = []string{}
	}
	return coverage, nil
}

func (d *Daemon) discardUncoveredSeedBells(sessionID protocol.SessionID) error {
	return d.discardIneligibleGardenSeedBellsLocked(inbox.ToSession(sessionID))
}

func (d *Daemon) consumeSeedBell(sessionID protocol.SessionID, seedID string) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" || d.store == nil {
		return
	}
	d.lockGardenRoles()
	err := d.discardIneligibleGardenSeedBellsLocked(inbox.ToSession(sessionID))
	var consumed []inbox.Address
	if err == nil {
		for _, address := range d.inboxRoleAddresses(sessionID) {
			if err = d.discardIneligibleGardenSeedBellsLocked(address); err != nil {
				break
			}
			var read bool
			read, err = d.store.ReadGardenSeedInboxItems(address, sessionID, seedID, time.Now())
			if read {
				consumed = append(consumed, address)
			}
			if err != nil {
				break
			}
		}
	}
	d.unlockGardenRoles()
	if err != nil {
		d.logf("garden bell: consuming session=%s seed=%s: %v", sessionID, seedID, err)
		return
	}
	if len(consumed) > 0 {
		d.kickInboxAfterCommit(consumed...)
	}
}
