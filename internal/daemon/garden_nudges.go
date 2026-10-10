package daemon

import (
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

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
	b, err := d.bindings()
	if err != nil {
		return nil, err
	}
	r, err := d.requestFromSession(sessionID, b)
	if err != nil {
		return nil, err
	}
	party, _ := r.Party()
	if watching {
		if key, member := party.Member(); member {
			identity, err := d.store.CrewIdentity(key)
			if err != nil {
				return nil, err
			}
			if identity.Retired {
				return nil, fmt.Errorf("%s is retired; restore them with attn crew restore %s before watching work", identity.Name, identity.Name)
			}
		}
	}
	profile, err := d.store.PartyProfile(party)
	if err != nil {
		return nil, err
	}
	if err := d.requireSeedInProfile(seedID, profile, false); err != nil {
		return nil, err
	}
	changed, err := d.store.SetGardenSeedWatch(party, seedID, watching, time.Now())
	if err != nil {
		return nil, err
	}
	if !watching {
		if err := d.discardIneligibleGardenSeedBellsLocked(party.Address()); err != nil {
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
	watches map[string][]store.GardenPartyWatch
}

func newGardenSubscriptions(seeds []garden.Seed, watches []store.GardenPartyWatch) gardenSubscriptions {
	subscriptions := gardenSubscriptions{
		parents: make(map[string]string, len(seeds)),
		seeds:   make(map[string]garden.Seed, len(seeds)),
		watches: map[string][]store.GardenPartyWatch{},
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

func (s gardenSubscriptions) coverage(seedID string) map[who.Party][]string {
	covered, _ := s.coverageChecked(seedID)
	return covered
}

func (s gardenSubscriptions) coverageChecked(seedID string) (map[who.Party][]string, error) {
	covered := map[who.Party][]string{}
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
			covered[watch.Watcher] = append(covered[watch.Watcher], at)
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
	return newGardenSubscriptions(read.seeds, watches), nil
}

func (d *Daemon) seedWatchCoverage(sessionID protocol.SessionID, seedID string) ([]string, error) {
	if protocol.TrimID(sessionID) == "" {
		return []string{}, nil
	}
	subscriptions, err := d.readGardenSubscriptions()
	if err != nil {
		return nil, err
	}
	b, err := d.bindings()
	if err != nil {
		return nil, err
	}
	r, err := d.requestFromSession(sessionID, b)
	if err != nil {
		return nil, err
	}
	party, _ := r.Party()
	coverage := subscriptions.coverage(seedID)[party]
	if coverage == nil {
		coverage = []string{}
	}
	return coverage, nil
}

func (d *Daemon) consumeSeedBell(sessionID protocol.SessionID, seedID string) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" || d.store == nil {
		return
	}
	d.lockGardenRoles()
	b, err := d.bindings()
	if err == nil {
		err = b.CheckSession(sessionID)
	}
	if err == nil {
		err = d.discardIneligibleGardenSeedBellsLocked(who.ToSession(sessionID))
	}
	var consumed []who.Address
	if err == nil {
		for _, address := range b.AddressesOf(sessionID) {
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
