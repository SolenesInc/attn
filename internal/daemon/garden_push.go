package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

type gardenHeld struct {
	profileID string
	order     []string
	prints    map[string][sha256.Size]byte
	total     int
}

type gardenWindow struct {
	seeds []protocol.Seed
	held  gardenHeld
}

func (d *Daemon) gardenWindow(profileID string) gardenWindow {
	seeds := []protocol.Seed{}
	held := gardenHeld{profileID: profileID, prints: map[string][sha256.Size]byte{}}
	if profileID != "" {
		seeds = d.seedsForBroadcast(profileID)
		held.total = d.countSeedsForBroadcast(profileID)
	}
	for _, seed := range seeds {
		data, err := json.Marshal(seed)
		if err != nil {
			d.logf("garden push: encoding seed %s: %v", seed.ID, err)
		}
		held.order = append(held.order, seed.ID)
		held.prints[seed.ID] = sha256.Sum256(data)
	}
	return gardenWindow{seeds: seeds, held: held}
}

func (w gardenWindow) full() *protocol.GardenSeedsUpdatedMessage {
	return &protocol.GardenSeedsUpdatedMessage{
		Event: protocol.EventGardenSeedsUpdated, ProfileID: w.held.profileID,
		Seeds: w.seeds, Total: w.held.total,
	}
}

func (w gardenWindow) changedSince(held *gardenHeld) *protocol.GardenSeedsChangedMessage {
	changed := []protocol.Seed{}
	for _, seed := range w.seeds {
		if print, ok := held.prints[seed.ID]; !ok || print != w.held.prints[seed.ID] {
			changed = append(changed, seed)
		}
	}
	message := w.held.unchanged()
	message.Seeds = changed
	return message
}

func (h *gardenHeld) unchanged() *protocol.GardenSeedsChangedMessage {
	return &protocol.GardenSeedsChangedMessage{
		Event: protocol.EventGardenSeedsChanged, ProfileID: h.profileID,
		Order: h.order, Seeds: []protocol.Seed{}, Total: h.total,
	}
}

func (d *Daemon) sendGardenFull(client *wsClient) {
	client.gardenMu.Lock()
	defer client.gardenMu.Unlock()
	window := d.gardenWindow(client.selectedProfile())
	if d.sendToClient(client, window.full()) {
		client.gardenHeld = &window.held
	}
}

func (d *Daemon) sendGardenChanges(client *wsClient, window gardenWindow) {
	client.gardenMu.Lock()
	defer client.gardenMu.Unlock()
	if client.selectedProfile() != window.held.profileID {
		return
	}
	var message any = window.full()
	if client.gardenHeld != nil {
		message = window.changedSince(client.gardenHeld)
	}
	if d.sendToClient(client, message) {
		client.gardenHeld = &window.held
	}
}

func (d *Daemon) sendGardenUnchanged(client *wsClient) {
	client.gardenMu.Lock()
	defer client.gardenMu.Unlock()
	if client.gardenHeld != nil {
		d.sendToClient(client, client.gardenHeld.unchanged())
	}
}

func isGardenSeedsFact(subject string) bool {
	seeds := docstore.Target(garden.Namespace, garden.CollectionSeeds)
	return subject == seeds || strings.HasPrefix(subject, seeds+"/")
}
