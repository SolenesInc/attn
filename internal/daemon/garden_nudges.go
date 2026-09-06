package daemon

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var gardenRingEvents = map[garden.Verb]string{
	garden.VerbTend: "tended", garden.VerbPark: "parked", garden.VerbHarvest: "harvested",
	garden.VerbWither: "withered", garden.VerbReplant: "replanted",
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
	watches map[string][]store.GardenSeedWatch
}

func newGardenSubscriptions(seeds []garden.Seed, watches []store.GardenSeedWatch) gardenSubscriptions {
	subscriptions := gardenSubscriptions{parents: make(map[string]string, len(seeds)), watches: map[string][]store.GardenSeedWatch{}}
	for _, seed := range seeds {
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

// Coverage names the ordinary subscriptions covering this seed for each session.
func (s gardenSubscriptions) coverage(seedID string) map[string][]string {
	covered := map[string][]string{}
	seen := map[string]bool{}
	for at := seedID; !seen[at]; {
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
	return covered
}

func (d *Daemon) readGardenSubscriptions() (gardenSubscriptions, error) {
	read, err := d.readGarden()
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

// Caller holds gardenWatchMu through the dependent enqueue or inbox read.
func (d *Daemon) discardUncoveredSeedBells(sessionID string) error {
	seeds, err := d.store.UnreadGardenSeedMailboxSeeds(sessionID)
	if err != nil {
		return err
	}
	if len(seeds) == 0 {
		return nil
	}
	subscriptions, err := d.readGardenSubscriptions()
	if err != nil {
		return err
	}
	var uncovered []string
	for _, seedID := range seeds {
		if len(subscriptions.coverage(seedID)[sessionID]) != 0 {
			continue
		}
		uncovered = append(uncovered, seedID)
	}
	if err := d.store.DiscardGardenSeedMailboxItems(sessionID, uncovered, time.Now()); err != nil {
		return err
	}
	return d.refreshAgentMailboxUnread(sessionID)
}

func (d *Daemon) consumeSeedBell(sessionID, seedID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || d.store == nil {
		return
	}
	consumed, remaining, err := d.store.ReadGardenSeedMailboxItems(sessionID, seedID, time.Now())
	if err != nil {
		d.logf("garden bell: consuming session=%s seed=%s: %v", sessionID, seedID, err)
		return
	}
	if consumed {
		d.noteAgentMailboxRead(sessionID, remaining)
	}
}

// Watching an ancestor covers descendants planted later, because no subscription
// is copied down the tree.
func (d *Daemon) ringSeedActivity(seedID, eventKind string, excludedSessionIDs ...string) {
	if d.store == nil {
		return
	}
	d.gardenWatchMu.Lock()
	defer d.gardenWatchMu.Unlock()
	subscriptions, err := d.readGardenSubscriptions()
	if err != nil {
		d.logf("garden bell: reading subscriptions for %s: %v", seedID, err)
		return
	}
	targets := subscriptions.coverage(seedID)

	for _, sessionID := range excludedSessionIDs {
		delete(targets, strings.TrimSpace(sessionID))
	}
	for sessionID := range targets {
		if d.store.Get(sessionID) == nil {
			continue
		}
		d.claimAndDeliverSeedBell(sessionID, seedID, eventKind)
	}
}

func (d *Daemon) claimAndDeliverSeedBell(sessionID, seedID, eventKind string) {
	now := time.Now()
	claimed, err := d.store.ClaimGardenSeedMailboxItem(sessionID, seedID, eventKind, uuid.NewString(), now)
	if err != nil {
		d.logf("garden bell: claiming session=%s seed=%s: %v", sessionID, seedID, err)
		return
	}
	if !claimed {
		return
	}
	d.noteQueuedAgentMailboxItem(sessionID)
	go d.drainQueuedAgentMailboxItems(sessionID)
}
