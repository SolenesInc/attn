package daemon

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/victorarias/attn/internal/protocol"
)

type gardenHeld struct {
	order  []string
	prints map[string][sha256.Size]byte
	total  int
}

type gardenWindow struct {
	profileID string
	seeds     []protocol.Seed
	held      gardenHeld
}

func (d *Daemon) gardenWindow(profileID string) gardenWindow {
	seeds := []protocol.Seed{}
	held := gardenHeld{prints: map[string][sha256.Size]byte{}}
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
	return gardenWindow{profileID: profileID, seeds: seeds, held: held}
}

func (w gardenWindow) full() *protocol.GardenSeedsUpdatedMessage {
	return &protocol.GardenSeedsUpdatedMessage{
		Event: protocol.EventGardenSeedsUpdated, ProfileID: w.profileID,
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
	return &protocol.GardenSeedsChangedMessage{
		Event: protocol.EventGardenSeedsChanged, ProfileID: w.profileID,
		Order: w.held.order, Seeds: changed, Total: w.held.total,
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
	if client.selectedProfile() != window.profileID {
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
