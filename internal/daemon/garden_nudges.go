package daemon

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
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

func (d *Daemon) localGardenTenderSession(tender garden.Tender) (string, error) {
	sessionID := strings.TrimSpace(tender.Session)
	if sessionID == "" {
		if member := strings.TrimSpace(tender.Member); member != "" {
			var err error
			sessionID, err = d.crewSessionBoundTo(member)
			if err != nil {
				return "", err
			}
		}
	}
	if sessionID == "" {
		return "", nil
	}
	if d.store != nil && (d.store.Get(sessionID) != nil || d.store.DelegationSessionReserved(sessionID)) {
		return sessionID, nil
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
	sessionID := strings.TrimSpace(msg.SourceSessionID)
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

func (d *Daemon) setSeedWatch(sessionID, seedID string, watching bool) (*protocol.SeedWatchResult, error) {
	d.gardenWatchMu.Lock()
	defer d.gardenWatchMu.Unlock()
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

func (s gardenSubscriptions) coverage(seedID string) map[string][]string {
	covered, _ := s.coverageChecked(seedID)
	return covered
}

func (s gardenSubscriptions) coverageChecked(seedID string) (map[string][]string, error) {
	covered := map[string][]string{}
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
	return newGardenSubscriptions(read.seeds, watches), nil
}

func (d *Daemon) seedWatchCoverage(sessionID, seedID string) ([]string, error) {
	if strings.TrimSpace(sessionID) == "" {
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

func (d *Daemon) discardUncoveredSeedBells(sessionID string) error {
	return d.discardIneligibleGardenSeedBellsLocked(sessionID)
}

func (d *Daemon) consumeSeedBell(sessionID, seedID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.store == nil {
		return
	}
	d.gardenWatchMu.Lock()
	err := d.discardIneligibleGardenSeedBellsLocked(sessionID)
	var consumed bool
	var remaining int
	if err == nil {
		consumed, remaining, err = d.store.ReadGardenSeedMailboxItems(sessionID, seedID, time.Now())
	}
	d.gardenWatchMu.Unlock()
	if err != nil {
		d.logf("garden bell: consuming session=%s seed=%s: %v", sessionID, seedID, err)
		return
	}
	if consumed {
		d.noteAgentMailboxRead(sessionID, remaining)
	}
}
