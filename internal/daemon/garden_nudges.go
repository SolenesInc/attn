package daemon

import (
	"context"
	"encoding/json"
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

const gardenRingUnblocked = store.GardenSeedEventUnblocked

func (d *Daemon) seedUnblocked(seedID string) ([]garden.Seed, []protocol.Seed) {
	if d.store == nil {
		return nil, nil
	}
	// The whole graph, never the newest snapshot page: a blocker paged out of
	// that window would make a seed it still holds back look freed.
	read, err := d.readGardenTo(0)
	if err != nil {
		d.logf("garden bell: reading the graph to see what %s unblocked: %v", seedID, err)
		return nil, nil
	}
	unblocked := garden.Unblocks(read.seeds, seedID)
	return unblocked, read.wire(unblocked)
}

func (d *Daemon) ringSeedUnblocked(unblocked []garden.Seed, excludedSessionIDs ...string) {
	if d.store == nil {
		return
	}
	d.gardenWatchMu.Lock()
	defer d.gardenWatchMu.Unlock()
	excluded := make(map[string]bool, len(excludedSessionIDs))
	for _, sessionID := range excludedSessionIDs {
		excluded[strings.TrimSpace(sessionID)] = true
	}
	delete(excluded, "")
	for _, seed := range unblocked {
		sessionID, err := d.tenderSession(seed.Tender())
		if err != nil {
			d.logf("garden bell: resolving the tender for unblocked seed %s: %v", seed.ID, err)
			continue
		}
		if sessionID == "" || excluded[sessionID] {
			continue
		}
		d.claimAndDeliverSeedBell(sessionID, seed.ID, gardenRingUnblocked)
	}
}

func (d *Daemon) tenderSession(tender garden.Tender) (string, error) {
	if session := strings.TrimSpace(tender.Session); session != "" {
		if d.sessionExists(session) {
			return session, nil
		}
		return "", nil
	}
	if member := strings.TrimSpace(tender.Member); member != "" {
		return d.crewSessionBoundTo(member)
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
	items, err := d.store.UnreadGardenSeedMailboxItems(sessionID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return d.refreshAgentMailboxUnread(sessionID)
	}
	subscriptions, err := d.readGardenSubscriptions()
	if err != nil {
		return err
	}
	var uncovered []string
	for _, item := range items {
		if len(subscriptions.coverage(item.SeedID)[sessionID]) != 0 {
			continue
		}
		tendered, err := d.unblockedSeedTenderedBy(item, sessionID)
		if err != nil {
			return err
		}
		if tendered {
			continue
		}
		uncovered = append(uncovered, item.SeedID)
	}
	if err := d.store.DiscardGardenSeedMailboxItems(sessionID, uncovered, time.Now()); err != nil {
		return err
	}
	return d.refreshAgentMailboxUnread(sessionID)
}

func (d *Daemon) unblockedSeedTenderedBy(item store.GardenSeedMailboxItem, sessionID string) (bool, error) {
	if item.EventKind != gardenRingUnblocked {
		return false, nil
	}
	seed, _, err := d.readSeed(item.SeedID)
	if err != nil {
		return false, fmt.Errorf("read unblocked seed %s for %s: %w", item.SeedID, sessionID, err)
	}
	tenderSessionID, err := d.tenderSession(seed.Tender())
	if err != nil {
		return false, fmt.Errorf("resolve the tender for unblocked seed %s: %w", item.SeedID, err)
	}
	return tenderSessionID == sessionID, nil
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
	itemID := uuid.NewString()
	if endpointID, remote := d.sessionOwningEndpoint(sessionID); remote {
		msg := protocol.DeliverGardenSeedBellMessage{
			Cmd: protocol.CmdDeliverGardenSeedBell, ItemID: itemID,
			SessionID: sessionID, SeedID: seedID, EventKind: eventKind,
		}
		payload, err := json.Marshal(msg)
		if err != nil {
			d.logf("garden bell: marshal remote delivery session=%s seed=%s: %v", sessionID, seedID, err)
			return
		}
		if err := d.hubManager.ForwardEndpointCommand(context.Background(), endpointID, payload); err != nil {
			d.logf("garden bell: forward session=%s seed=%s endpoint=%s: %v", sessionID, seedID, endpointID, err)
		}
		return
	}
	d.claimAndDeliverLocalSeedBell(sessionID, seedID, eventKind, itemID)
}

func (d *Daemon) handleDeliverGardenSeedBell(client *wsClient, msg *protocol.DeliverGardenSeedBellMessage) {
	if client == nil || client.clientKind != "hub" {
		d.logf("garden bell: refusing remote delivery from a non-hub client")
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	seedID := strings.TrimSpace(msg.SeedID)
	eventKind := strings.TrimSpace(msg.EventKind)
	itemID := strings.TrimSpace(msg.ItemID)
	if sessionID == "" || seedID == "" || eventKind == "" || itemID == "" || d.store.Get(sessionID) == nil {
		d.logf("garden bell: refusing invalid remote delivery session=%s seed=%s", sessionID, seedID)
		return
	}
	d.claimAndDeliverLocalSeedBell(sessionID, seedID, eventKind, itemID)
}

func (d *Daemon) claimAndDeliverLocalSeedBell(sessionID, seedID, eventKind, itemID string) {
	now := time.Now()
	claimed, err := d.store.ClaimGardenSeedMailboxItem(sessionID, seedID, eventKind, itemID, now)
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
