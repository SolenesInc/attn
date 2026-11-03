package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
	"github.com/victorarias/attn/internal/who"
)

const gatewayGardenAPI = 6

const gatewaySeedChangedAPI = 7

const gatewayClosedSeedShownFor = 24 * time.Hour

func (d *Daemon) gatewaySendGarden(conn *gatewayConn) error {
	if conn.api < gatewayGardenAPI {
		return nil
	}
	read, err := d.readGarden(conn.device.ProfileID)
	if err != nil {
		if docstore.IsUndeclaredCollection(err) {
			return d.gatewaySendGardenWindow(conn, nil, nil)
		}
		return err
	}
	ids := []string{}
	seeds := map[string]gateway.GatewaySeed{}
	shownSince := d.gardenTime().Add(-gatewayClosedSeedShownFor)
	for _, seed := range read.seeds {
		if garden.Closed(seed.Status) && !read.changedSince(seed, shownSince) {
			continue
		}
		ids = append(ids, seed.ID)
		seeds[seed.ID] = read.gatewaySeed(seed)
	}
	return d.gatewaySendGardenWindow(conn, ids, seeds)
}

func (d *Daemon) gatewaySendSeedChanges(conn *gatewayConn) error {
	conn.changedSeedsMu.Lock()
	changed := conn.changedSeeds
	conn.changedSeeds = nil
	conn.changedSeedsMu.Unlock()
	if conn.api < gatewaySeedChangedAPI {
		return nil
	}
	for _, id := range slices.Sorted(maps.Keys(changed)) {
		seed, _, err := d.readSeed(id)
		if err != nil {
			if refusal := protocol.RefusalOf(err); refusal == nil || *refusal != protocol.RefusalNotFound {
				d.logf("gateway: device %s misses a change to seed %s: %v", conn.device.DeviceID, id, err)
			}
			continue
		}
		if seed.ProfileID != conn.device.ProfileID {
			continue
		}
		if err := gatewaySend(conn, gateway.GatewaySeedChanged{Event: "seed_changed", SeedID: id}); err != nil {
			return err
		}
	}
	return nil
}

func (g gardenRead) changedSince(seed garden.Seed, since time.Time) bool {
	changedAt, err := time.Parse(time.RFC3339Nano, seed.StateChangedAt)
	if err != nil {
		changedAt = g.docs[seed.ID].UpdatedAt
	}
	return changedAt.After(since)
}

func (d *Daemon) gatewaySendGardenWindow(conn *gatewayConn, ids []string, seeds map[string]gateway.GatewaySeed) error {
	if ids == nil {
		ids = []string{}
	}
	prints := map[string][]byte{}
	var changed []gateway.GatewaySeed
	for _, id := range ids {
		print, err := json.Marshal(seeds[id])
		if err != nil {
			return err
		}
		prints[id] = print
		if !bytes.Equal(conn.sentSeeds[id], print) {
			changed = append(changed, seeds[id])
		}
	}
	if conn.sentSeedIDs != nil && len(changed) == 0 && slices.Equal(conn.sentSeedIDs, ids) {
		return nil
	}
	if err := gatewaySendPacked(conn, changed); err != nil {
		return err
	}
	if err := gatewaySend(conn, gateway.GatewayGarden{Event: "garden", Ids: ids}); err != nil {
		return err
	}
	conn.sentSeeds, conn.sentSeedIDs = prints, ids
	return nil
}

func gatewaySendPacked(conn *gatewayConn, seeds []gateway.GatewaySeed) error {
	for len(seeds) > 0 {
		count := len(seeds)
		for {
			frame, err := gatewayMarshal(gateway.GatewayGardenSeeds{Event: "garden_seeds", Seeds: seeds[:count]})
			if err != nil {
				return err
			}
			if len(frame) <= cloudlink.MaxMessageBytes || count == 1 {
				if err := conn.sendRaw(frame); err != nil {
					return err
				}
				break
			}
			count /= 2
		}
		seeds = seeds[count:]
	}
	return nil
}

var gatewaySeedStatuses = map[string]gateway.GatewaySeedStatus{
	garden.StatusPlanted:   gateway.GatewaySeedStatusPlanted,
	garden.StatusGrowing:   gateway.GatewaySeedStatusGrowing,
	garden.StatusParked:    gateway.GatewaySeedStatusParked,
	garden.StatusHarvested: gateway.GatewaySeedStatusHarvested,
	garden.StatusWithered:  gateway.GatewaySeedStatusWithered,
}

func (g gardenRead) gatewaySeed(seed garden.Seed) gateway.GatewaySeed {
	wire := g.seed(seed, g.docs[seed.ID])
	status, ok := gatewaySeedStatuses[wire.Status]
	if !ok {
		status = gateway.GatewaySeedStatusUnknown
	}
	changedAt, _ := time.Parse(time.RFC3339Nano, wire.StateChangedAt)
	out := gateway.GatewaySeed{
		ID:             wire.ID,
		Slug:           wire.StepSlug,
		Title:          wire.Title,
		Status:         status,
		Claimed:        wire.Claimed,
		Ready:          wire.Ready,
		WaitsFor:       garden.Blockers(g.seeds, seed.ID),
		StateChangedAt: changedAt.UTC(),
		Reason:         wire.Reason,
	}
	if wire.Tender != nil {
		out.Tender = protocol.Ptr(wire.Tender.Name)
	}
	if plot, ok := garden.ParentOf(seed); ok {
		out.PlotID = protocol.Ptr(plot)
	}
	if p := wire.PlotProgress; p != nil {
		out.Progress = &gateway.GatewayPlotProgress{Total: p.Total, Done: p.Done, Withered: p.Withered, Growing: p.Growing, Parked: p.Parked, Ready: p.Ready, Blocked: p.Blocked}
		if p.Checks != nil {
			out.Checks = &gateway.GatewayCheckTally{Confirmed: p.Checks.Confirmed, Total: p.Checks.Total}
		}
		return out
	}
	tally := gateway.GatewayCheckTally{}
	for _, check := range wire.Checks {
		if check.Removed {
			continue
		}
		tally.Total++
		if check.State == string(gateway.GatewayCheckStateConfirmed) {
			tally.Confirmed++
		}
	}
	if tally.Total > 0 {
		out.Checks = &tally
	}
	return out
}

func (d *Daemon) gatewayProfileSeed(conn *gatewayConn, id string) (garden.Seed, error) {
	seed, _, err := d.readSeed(id)
	if err == nil && seed.ProfileID != conn.device.ProfileID {
		return garden.Seed{}, protocol.Refuse(protocol.RefusalNotFound, "no seed %s is planted here; `attn seed ls` lists the garden", id)
	}
	return seed, err
}

func (d *Daemon) gatewaySeedGet(conn *gatewayConn, command gateway.GatewaySeedGet) error {
	detail, err := d.gatewaySeedDetail(conn, command)
	if err != nil {
		return gatewaySend(conn, gateway.GatewaySeedError{Event: "seed_error", RequestID: command.RequestID, Message: err.Error()})
	}
	frame, err := gatewayDetailFrame(detail)
	if err != nil {
		return gatewaySend(conn, gateway.GatewaySeedError{Event: "seed_error", RequestID: command.RequestID, Message: err.Error()})
	}
	return conn.sendRaw(frame)
}

func (d *Daemon) gatewaySeedDetail(conn *gatewayConn, command gateway.GatewaySeedGet) (gateway.GatewaySeedDetail, error) {
	seed, err := d.gatewayProfileSeed(conn, command.SeedID)
	if err != nil {
		return gateway.GatewaySeedDetail{}, err
	}
	read, err := d.readGarden(seed.ProfileID)
	if err != nil {
		return gateway.GatewaySeedDetail{}, err
	}
	notes, total, err := d.readNotes(seed.ID, "", docstore.MaxLimit)
	if err != nil {
		return gateway.GatewaySeedDetail{}, err
	}
	detail := gateway.GatewaySeedDetail{
		Event:     "seed_detail",
		RequestID: command.RequestID,
		Seed:      read.gatewaySeed(seed),
		Body:      seed.Body,
		Checks:    []gateway.GatewaySeedCheck{},
		Children:  []gateway.GatewaySeed{},
		Log:       make([]gateway.GatewaySeedLogEntry, 0, len(notes)),
		LogTotal:  total,
	}
	for _, check := range read.checks(seed.ID) {
		if check.Removed {
			continue
		}
		state := gateway.GatewayCheckState(check.State)
		if !slices.Contains([]gateway.GatewayCheckState{gateway.GatewayCheckStateOpen, gateway.GatewayCheckStateReported, gateway.GatewayCheckStateConfirmed, gateway.GatewayCheckStateRejected}, state) {
			state = gateway.GatewayCheckStateUnknown
		}
		detail.Checks = append(detail.Checks, gateway.GatewaySeedCheck{ID: check.ID, Text: check.Text, State: state, From: check.From})
	}
	for _, child := range plotChildren(read, seed.ID) {
		detail.Children = append(detail.Children, read.gatewaySeed(child))
	}
	for _, note := range notes {
		createdAt, _ := time.Parse(time.RFC3339Nano, note.CreatedAt)
		detail.Log = append(detail.Log, gateway.GatewaySeedLogEntry{ID: note.ID, Kind: note.Kind, Body: note.Body, Author: note.Author.Name, CreatedAt: createdAt.UTC()})
	}
	return detail, nil
}

func gatewayDetailFrame(detail gateway.GatewaySeedDetail) ([]byte, error) {
	log := detail.Log
	for {
		frame, err := newestLogThatFits(detail, log)
		if err != nil || frame != nil {
			return frame, err
		}
		switch {
		case detail.Body != "":
			detail.BodyTooLargeBytes = protocol.Ptr(len(detail.Body))
			detail.MaxFrameBytes = protocol.Ptr(cloudlink.MaxMessageBytes)
			detail.Body = ""
		default:
			return nil, fmt.Errorf("seed %s does not fit one gateway message of %d bytes even without its body and log; read it on the computer", detail.Seed.ID, cloudlink.MaxMessageBytes)
		}
	}
}

func newestLogThatFits(detail gateway.GatewaySeedDetail, log []gateway.GatewaySeedLogEntry) ([]byte, error) {
	encode := func(n int) ([]byte, error) {
		detail.Log = log[:n]
		return gatewayMarshal(detail)
	}
	var best []byte
	low, high := 0, len(log)
	for low <= high {
		n := (low + high) / 2
		frame, err := encode(n)
		if err != nil {
			return nil, err
		}
		if len(frame) <= cloudlink.MaxMessageBytes {
			best, low = frame, n+1
		} else {
			high = n - 1
		}
	}
	return best, nil
}

func (d *Daemon) gatewaySeedNote(conn *gatewayConn, command gateway.GatewaySeedNoteCommand) error {
	result := gateway.GatewaySeedResult{Event: "seed_result", RequestID: command.RequestID, SeedID: command.SeedID}
	if _, err := d.gatewayProfileSeed(conn, command.SeedID); err != nil {
		result.Error = protocol.Ptr(err.Error())
		return gatewaySend(conn, result)
	}
	if _, err := d.appendSeedNote(command.SeedID, command.Body, who.User(), "", nil, false, ""); err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return gatewaySend(conn, result)
}

var gatewaySeedVerbs = map[gateway.GatewaySeedVerb]garden.Verb{
	gateway.GatewaySeedVerbPark:    garden.VerbPark,
	gateway.GatewaySeedVerbHarvest: garden.VerbHarvest,
	gateway.GatewaySeedVerbWither:  garden.VerbWither,
	gateway.GatewaySeedVerbReplant: garden.VerbReplant,
}

func (d *Daemon) gatewaySeedMove(conn *gatewayConn, command gateway.GatewaySeedMove) error {
	result := gateway.GatewaySeedResult{Event: "seed_result", RequestID: command.RequestID, SeedID: command.SeedID}
	if err := d.gatewayMoveSeed(conn, command); err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return gatewaySend(conn, result)
}

func (d *Daemon) gatewayMoveSeed(conn *gatewayConn, command gateway.GatewaySeedMove) error {
	verb, ok := gatewaySeedVerbs[command.Verb]
	if !ok {
		return protocol.Refuse(protocol.RefusalInvalidInput, "%q is not a move the phone makes; it parks, harvests, withers or replants", command.Verb)
	}
	if _, err := d.gatewayProfileSeed(conn, command.SeedID); err != nil {
		return err
	}
	ask := garden.Ask{Actor: who.User(), Force: true}
	comment := ""
	switch verb {
	case garden.VerbHarvest, garden.VerbWither:
		ask.Reason = command.Text
	case garden.VerbPark:
		comment = command.Text
	case garden.VerbReplant:
		if command.Text != "" {
			if _, err := d.appendSeedNote(command.SeedID, command.Text, who.User(), "", nil, false, ""); err != nil {
				return err
			}
		}
	}
	_, _, _, err := d.applySeedTransitionDetailed(command.SeedID, verb, ask, comment)
	return err
}

func (d *Daemon) gatewayGardenCommand(conn *gatewayConn, cmd string, frame []byte) error {
	malformed := func(err error) error {
		return fmt.Errorf("device %s sent a malformed %s: %w", conn.device.DeviceID, cmd, err)
	}
	switch cmd {
	case "seed_get":
		var command gateway.GatewaySeedGet
		if err := json.Unmarshal(frame, &command); err != nil {
			return malformed(err)
		}
		return d.gatewaySeedGet(conn, command)
	case "seed_note":
		var command gateway.GatewaySeedNoteCommand
		if err := json.Unmarshal(frame, &command); err != nil {
			return malformed(err)
		}
		return d.gatewaySeedNote(conn, command)
	default:
		var command gateway.GatewaySeedMove
		if err := json.Unmarshal(frame, &command); err != nil {
			return malformed(err)
		}
		return d.gatewaySeedMove(conn, command)
	}
}
