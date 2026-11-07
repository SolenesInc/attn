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
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/who"
)

const homeGatewayGardenAPI = 6

const homeGatewaySeedChangedAPI = 7

const homeGatewayClosedSeedShownFor = 24 * time.Hour

func (d *Daemon) homeGatewaySendGarden(conn *homeGatewayConn) error {
	if conn.api < homeGatewayGardenAPI {
		return nil
	}
	read, err := d.readGarden(conn.device.ProfileID)
	if err != nil {
		if docstore.IsUndeclaredCollection(err) {
			return d.homeGatewaySendGardenWindow(conn, nil, nil)
		}
		return err
	}
	ids := []string{}
	seeds := map[string]homegateway.HomeGatewaySeed{}
	shownSince := d.gardenTime().Add(-homeGatewayClosedSeedShownFor)
	for _, seed := range read.seeds {
		if garden.Closed(seed.Status) && !read.changedSince(seed, shownSince) {
			continue
		}
		ids = append(ids, seed.ID)
		seeds[seed.ID] = read.homeGatewaySeed(seed)
	}
	return d.homeGatewaySendGardenWindow(conn, ids, seeds)
}

func (d *Daemon) homeGatewaySendSeedChanges(conn *homeGatewayConn) error {
	conn.changedSeedsMu.Lock()
	changed := conn.changedSeeds
	conn.changedSeeds = nil
	conn.changedSeedsMu.Unlock()
	if conn.api < homeGatewaySeedChangedAPI {
		return nil
	}
	for _, id := range slices.Sorted(maps.Keys(changed)) {
		seed, _, err := d.readSeed(id)
		if err != nil {
			if refusal := protocol.RefusalOf(err); refusal == nil || *refusal != protocol.RefusalNotFound {
				d.logf("Home Gateway: device %s misses a change to seed %s: %v", conn.device.DeviceID, id, err)
			}
			continue
		}
		if seed.ProfileID != conn.device.ProfileID {
			continue
		}
		if err := homeGatewaySend(conn, homegateway.HomeGatewaySeedChanged{Event: "seed_changed", SeedID: id}); err != nil {
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

func (d *Daemon) homeGatewaySendGardenWindow(conn *homeGatewayConn, ids []string, seeds map[string]homegateway.HomeGatewaySeed) error {
	if ids == nil {
		ids = []string{}
	}
	prints := map[string][]byte{}
	var changed []homegateway.HomeGatewaySeed
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
	if err := homeGatewaySendPacked(conn, changed); err != nil {
		return err
	}
	if err := homeGatewaySend(conn, homegateway.HomeGatewayGarden{Event: "garden", Ids: ids}); err != nil {
		return err
	}
	conn.sentSeeds, conn.sentSeedIDs = prints, ids
	return nil
}

func homeGatewaySendPacked(conn *homeGatewayConn, seeds []homegateway.HomeGatewaySeed) error {
	for len(seeds) > 0 {
		count := len(seeds)
		for {
			frame, err := homeGatewayMarshal(homegateway.HomeGatewayGardenSeeds{Event: "garden_seeds", Seeds: seeds[:count]})
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

var homeGatewaySeedStatuses = map[string]homegateway.HomeGatewaySeedStatus{
	garden.StatusPlanted:   homegateway.HomeGatewaySeedStatusPlanted,
	garden.StatusGrowing:   homegateway.HomeGatewaySeedStatusGrowing,
	garden.StatusParked:    homegateway.HomeGatewaySeedStatusParked,
	garden.StatusHarvested: homegateway.HomeGatewaySeedStatusHarvested,
	garden.StatusWithered:  homegateway.HomeGatewaySeedStatusWithered,
}

func (g gardenRead) homeGatewaySeed(seed garden.Seed) homegateway.HomeGatewaySeed {
	wire := g.seed(seed, g.docs[seed.ID])
	status, ok := homeGatewaySeedStatuses[wire.Status]
	if !ok {
		status = homegateway.HomeGatewaySeedStatusUnknown
	}
	changedAt, _ := time.Parse(time.RFC3339Nano, wire.StateChangedAt)
	out := homegateway.HomeGatewaySeed{
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
		out.Progress = &homegateway.HomeGatewayPlotProgress{Total: p.Total, Done: p.Done, Withered: p.Withered, Growing: p.Growing, Parked: p.Parked, Ready: p.Ready, Blocked: p.Blocked}
		if p.Checks != nil {
			out.Checks = &homegateway.HomeGatewayCheckTally{Confirmed: p.Checks.Confirmed, Total: p.Checks.Total}
		}
		return out
	}
	tally := homegateway.HomeGatewayCheckTally{}
	for _, check := range wire.Checks {
		if check.Removed {
			continue
		}
		tally.Total++
		if check.State == string(homegateway.HomeGatewayCheckStateConfirmed) {
			tally.Confirmed++
		}
	}
	if tally.Total > 0 {
		out.Checks = &tally
	}
	return out
}

func (d *Daemon) homeGatewayProfileSeed(conn *homeGatewayConn, id string) (garden.Seed, error) {
	seed, _, err := d.readSeed(id)
	if err == nil && seed.ProfileID != conn.device.ProfileID {
		return garden.Seed{}, protocol.Refuse(protocol.RefusalNotFound, "no seed %s is planted here; `attn seed ls` lists the garden", id)
	}
	return seed, err
}

func (d *Daemon) homeGatewaySeedGet(conn *homeGatewayConn, command homegateway.HomeGatewaySeedGet) error {
	detail, err := d.homeGatewaySeedDetail(conn, command)
	if err != nil {
		return homeGatewaySend(conn, homegateway.HomeGatewaySeedError{Event: "seed_error", RequestID: command.RequestID, Message: err.Error()})
	}
	frame, err := homeGatewayDetailFrame(detail)
	if err != nil {
		return homeGatewaySend(conn, homegateway.HomeGatewaySeedError{Event: "seed_error", RequestID: command.RequestID, Message: err.Error()})
	}
	return conn.sendRaw(frame)
}

func (d *Daemon) homeGatewaySeedDetail(conn *homeGatewayConn, command homegateway.HomeGatewaySeedGet) (homegateway.HomeGatewaySeedDetail, error) {
	seed, err := d.homeGatewayProfileSeed(conn, command.SeedID)
	if err != nil {
		return homegateway.HomeGatewaySeedDetail{}, err
	}
	read, err := d.readGarden(seed.ProfileID)
	if err != nil {
		return homegateway.HomeGatewaySeedDetail{}, err
	}
	notes, total, err := d.readNotes(seed.ID, "", docstore.MaxLimit)
	if err != nil {
		return homegateway.HomeGatewaySeedDetail{}, err
	}
	detail := homegateway.HomeGatewaySeedDetail{
		Event:     "seed_detail",
		RequestID: command.RequestID,
		Seed:      read.homeGatewaySeed(seed),
		Body:      seed.Body,
		Checks:    []homegateway.HomeGatewaySeedCheck{},
		Children:  []homegateway.HomeGatewaySeed{},
		Log:       make([]homegateway.HomeGatewaySeedLogEntry, 0, len(notes)),
		LogTotal:  total,
	}
	for _, check := range read.checks(seed.ID) {
		if check.Removed {
			continue
		}
		state := homegateway.HomeGatewayCheckState(check.State)
		if !slices.Contains([]homegateway.HomeGatewayCheckState{homegateway.HomeGatewayCheckStateOpen, homegateway.HomeGatewayCheckStateReported, homegateway.HomeGatewayCheckStateConfirmed, homegateway.HomeGatewayCheckStateRejected}, state) {
			state = homegateway.HomeGatewayCheckStateUnknown
		}
		detail.Checks = append(detail.Checks, homegateway.HomeGatewaySeedCheck{ID: check.ID, Text: check.Text, State: state, From: check.From})
	}
	for _, child := range plotChildren(read, seed.ID) {
		detail.Children = append(detail.Children, read.homeGatewaySeed(child))
	}
	for _, note := range notes {
		createdAt, _ := time.Parse(time.RFC3339Nano, note.CreatedAt)
		detail.Log = append(detail.Log, homegateway.HomeGatewaySeedLogEntry{ID: note.ID, Kind: note.Kind, Body: note.Body, Author: note.Author.Name, CreatedAt: createdAt.UTC()})
	}
	return detail, nil
}

func homeGatewayDetailFrame(detail homegateway.HomeGatewaySeedDetail) ([]byte, error) {
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

func newestLogThatFits(detail homegateway.HomeGatewaySeedDetail, log []homegateway.HomeGatewaySeedLogEntry) ([]byte, error) {
	encode := func(n int) ([]byte, error) {
		detail.Log = log[:n]
		return homeGatewayMarshal(detail)
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

func (d *Daemon) homeGatewaySeedNote(conn *homeGatewayConn, command homegateway.HomeGatewaySeedNoteCommand) error {
	result := homegateway.HomeGatewaySeedResult{Event: "seed_result", RequestID: command.RequestID, SeedID: command.SeedID}
	if _, err := d.homeGatewayProfileSeed(conn, command.SeedID); err != nil {
		result.Error = protocol.Ptr(err.Error())
		return homeGatewaySend(conn, result)
	}
	if _, err := d.appendSeedNote(command.SeedID, command.Body, who.User(), "", nil, false, ""); err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return homeGatewaySend(conn, result)
}

var homeGatewaySeedVerbs = map[homegateway.HomeGatewaySeedVerb]garden.Verb{
	homegateway.HomeGatewaySeedVerbPark:    garden.VerbPark,
	homegateway.HomeGatewaySeedVerbHarvest: garden.VerbHarvest,
	homegateway.HomeGatewaySeedVerbWither:  garden.VerbWither,
	homegateway.HomeGatewaySeedVerbReplant: garden.VerbReplant,
}

func (d *Daemon) homeGatewaySeedMove(conn *homeGatewayConn, command homegateway.HomeGatewaySeedMove) error {
	result := homegateway.HomeGatewaySeedResult{Event: "seed_result", RequestID: command.RequestID, SeedID: command.SeedID}
	if err := d.homeGatewayMoveSeed(conn, command); err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return homeGatewaySend(conn, result)
}

func (d *Daemon) homeGatewayMoveSeed(conn *homeGatewayConn, command homegateway.HomeGatewaySeedMove) error {
	verb, ok := homeGatewaySeedVerbs[command.Verb]
	if !ok {
		return protocol.Refuse(protocol.RefusalInvalidInput, "%q is not a move the phone makes; it parks, harvests, withers or replants", command.Verb)
	}
	if _, err := d.homeGatewayProfileSeed(conn, command.SeedID); err != nil {
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

func (d *Daemon) homeGatewayGardenCommand(conn *homeGatewayConn, cmd string, frame []byte) error {
	malformed := func(err error) error {
		return fmt.Errorf("device %s sent a malformed %s: %w", conn.device.DeviceID, cmd, err)
	}
	switch cmd {
	case "seed_get":
		var command homegateway.HomeGatewaySeedGet
		if err := json.Unmarshal(frame, &command); err != nil {
			return malformed(err)
		}
		return d.homeGatewaySeedGet(conn, command)
	case "seed_note":
		var command homegateway.HomeGatewaySeedNoteCommand
		if err := json.Unmarshal(frame, &command); err != nil {
			return malformed(err)
		}
		return d.homeGatewaySeedNote(conn, command)
	default:
		var command homegateway.HomeGatewaySeedMove
		if err := json.Unmarshal(frame, &command); err != nil {
			return malformed(err)
		}
		return d.homeGatewaySeedMove(conn, command)
	}
}
