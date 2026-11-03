package daemon_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
)

func (p *testPhone) showsGarden(match func([]gateway.GatewaySeed) bool) []gateway.GatewaySeed {
	p.t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for {
		if p.seedIDs != nil {
			seeds := []gateway.GatewaySeed{}
			for _, id := range p.seedIDs {
				seeds = append(seeds, p.seeds[id])
			}
			if match(seeds) {
				return seeds
			}
		}
		p.receive(guard, "a matching garden")
	}
}

func (p *testPhone) request(message any, requestID string) []byte {
	p.t.Helper()
	p.send(message)
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	for {
		frame := p.receive(guard, "the answer to "+requestID)
		var head struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(frame, &head)
		if head.RequestID == requestID {
			return frame
		}
	}
}

func (p *testPhone) readSeed(seedID string) (gateway.GatewaySeedDetail, string) {
	p.t.Helper()
	requestID := uuid.NewString()
	frame := p.request(gateway.GatewaySeedGet{Cmd: "seed_get", RequestID: requestID, SeedID: seedID}, requestID)
	var detail gateway.GatewaySeedDetail
	var failed gateway.GatewaySeedError
	_ = json.Unmarshal(frame, &detail)
	if detail.Event == "seed_error" {
		_ = json.Unmarshal(frame, &failed)
		return detail, failed.Message
	}
	if detail.Event != "seed_detail" {
		p.t.Fatalf("the home answered seed_get with %s", frame)
	}
	return detail, ""
}

func (p *testPhone) seedCommand(message any, requestID string) string {
	p.t.Helper()
	var result gateway.GatewaySeedResult
	if err := json.Unmarshal(p.request(message, requestID), &result); err != nil || result.Event != "seed_result" {
		p.t.Fatalf("the home answered %+v (%v), want seed_result", result, err)
	}
	return protocol.Deref(result.Error)
}

func (p *testPhone) note(seedID, body string) string {
	p.t.Helper()
	requestID := uuid.NewString()
	return p.seedCommand(gateway.GatewaySeedNoteCommand{Cmd: "seed_note", RequestID: requestID, SeedID: seedID, Body: body}, requestID)
}

func (p *testPhone) move(seedID string, verb gateway.GatewaySeedVerb, text string) string {
	p.t.Helper()
	requestID := uuid.NewString()
	return p.seedCommand(gateway.GatewaySeedMove{Cmd: "seed_move", RequestID: requestID, SeedID: seedID, Verb: verb, Text: text}, requestID)
}

func gardenByID(seeds []gateway.GatewaySeed) map[string]gateway.GatewaySeed {
	out := map[string]gateway.GatewaySeed{}
	for _, seed := range seeds {
		out[seed.ID] = seed
	}
	return out
}

func TestGatewayShowsThePairedProfilesGardenAndTendsItAsTheDesktopDoes(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	personal := app.SelectedProfile()
	work := createProfile(app, "Work")
	for _, s := range []struct{ id, profile string }{{"mine", personal}, {"theirs", work.ID}} {
		if err := w.InjectSession(s.id, s.id+" label", w.Path(s.id), protocol.SessionAgentClaude, s.profile); err != nil {
			t.Fatal(err)
		}
	}
	plant := func(session, title, body, partOf string) string {
		t.Helper()
		planted, err := cli.SeedPlant(protocol.SessionID(session), title, body, partOf, "")
		if err != nil {
			t.Fatalf("planting %s: %v", title, err)
		}
		return planted.Seed.ID
	}
	planted, err := cli.SeedPlot("mine", protocol.SeedPlotMessage{
		Title: "Ship the phone", Body: protocol.Ptr("# Plan\n\nThe phone reads the **Garden**."),
		Children: []protocol.SeedPlotChild{
			{Title: "Read seeds", Body: protocol.Ptr("List and read.")},
			{Title: "Move seeds", Body: protocol.Ptr("Park and harvest."), After: []string{"read-seeds"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plot, first, second := planted.Crown.ID, planted.Children[0].ID, planted.Children[1].ID
	if _, err := cli.SeedTransition("mine", first, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	elsewhere := plant("theirs", "Work only", "Not for the personal phone.", "")

	phone := connectPhone(t, w, "phone", personal)
	phone.welcomed()
	seeds := gardenByID(phone.showsGarden(func(seeds []gateway.GatewaySeed) bool { return len(seeds) == 3 }))
	if _, leaked := seeds[elsewhere]; leaked || seeds[plot].ID == "" || seeds[first].ID == "" || seeds[second].ID == "" {
		t.Fatalf("the personal phone's garden is %v, want the plot and both children, without %s", phone.seedIDs, elsewhere)
	}
	if got := seeds[plot]; got.Progress == nil || got.Progress.Total != 2 || got.Progress.Growing != 1 || got.Progress.Blocked != 1 || got.Ready {
		t.Fatalf("the plot reads %+v, want two children, one growing and one blocked", got)
	}
	if got := seeds[first]; got.Status != gateway.GatewaySeedStatusGrowing || !got.Claimed || protocol.Deref(got.Tender) == "" || protocol.Deref(got.PlotID) != plot {
		t.Fatalf("the tended child reads %+v, want growing, claimed by mine, in the plot", got)
	}
	if got := seeds[second]; !slices.Equal(got.WaitsFor, []string{first}) || got.Ready {
		t.Fatalf("the blocked child reads %+v, want it waiting for %s", got, first)
	}

	detail, failure := phone.readSeed(plot)
	if children := gardenByID(detail.Children); failure != "" || !strings.Contains(detail.Body, "The phone reads the **Garden**.") || len(children) != 2 || children[first].ID == "" || children[second].ID == "" {
		t.Fatalf("reading the plot gave %+v (%s), want its body and both children", detail, failure)
	}
	if _, failure := phone.readSeed(elsewhere); !strings.Contains(failure, "no seed "+elsewhere+" is planted here") {
		t.Fatalf("reading another profile's seed failed with %q, want it to read as absent", failure)
	}

	if failure := phone.note(first, "Checked from the phone."); failure != "" {
		t.Fatalf("noting: %s", failure)
	}
	detail, _ = phone.readSeed(first)
	if len(detail.Log) == 0 || detail.Log[0].Body != "Checked from the phone." || detail.Log[0].Author != "the user" || detail.LogTotal != len(detail.Log) {
		t.Fatalf("the log after the note reads %+v, want the user's note newest", detail.Log)
	}
	if failure := phone.note(elsewhere, "Not mine to note."); !strings.Contains(failure, "no seed "+elsewhere) {
		t.Fatalf("noting another profile's seed failed with %q, want it to read as absent", failure)
	}
	if theirs, err := cli.SeedNotes("theirs", elsewhere, client.SeedNotesQuery{}); err != nil || theirs.Total != 0 {
		t.Fatalf("the work seed's log holds %+v (%v) after the refused note, want nothing", theirs, err)
	}

	if failure := phone.move(first, gateway.GatewaySeedVerbPark, "Waiting on the phone build."); failure != "" {
		t.Fatalf("parking the tended seed: %s", failure)
	}
	seeds = gardenByID(phone.showsGarden(func(seeds []gateway.GatewaySeed) bool {
		return gardenByID(seeds)[first].Status == gateway.GatewaySeedStatusParked
	}))
	if got := seeds[first]; got.Claimed || got.Tender != nil {
		t.Fatalf("the parked seed reads %+v, want its claim released", got)
	}
	detail, _ = phone.readSeed(first)
	if !slices.ContainsFunc(detail.Log, func(entry gateway.GatewaySeedLogEntry) bool {
		return strings.Contains(entry.Body, "Waiting on the phone build.")
	}) {
		t.Fatalf("the parked seed's log %+v, want the park's comment", detail.Log)
	}

	if failure := phone.move(second, gateway.GatewaySeedVerbHarvest, ""); !strings.Contains(failure, "records what got done") {
		t.Fatalf("harvesting without a reason failed with %q, want the reason asked for", failure)
	}
	if failure := phone.move(second, gateway.GatewaySeedVerbHarvest, "Moves work from the phone."); failure != "" {
		t.Fatalf("harvesting: %s", failure)
	}
	seeds = gardenByID(phone.showsGarden(func(seeds []gateway.GatewaySeed) bool {
		return gardenByID(seeds)[second].Status == gateway.GatewaySeedStatusHarvested
	}))
	if got := seeds[plot].Progress; got == nil || got.Done != 1 || got.Parked != 1 {
		t.Fatalf("the plot after the harvest reads %+v, want one done and one parked", got)
	}
	if failure := phone.move(second, gateway.GatewaySeedVerbReplant, "Harvested by mistake."); failure != "" {
		t.Fatalf("replanting the seed closed a moment ago: %s", failure)
	}
	phone.showsGarden(func(seeds []gateway.GatewaySeed) bool {
		return gardenByID(seeds)[second].Status == gateway.GatewaySeedStatusPlanted
	})
	if failure := phone.move(second, gateway.GatewaySeedVerbHarvest, "Moves work from the phone."); failure != "" {
		t.Fatalf("harvesting again: %s", failure)
	}
	detail, _ = phone.readSeed(plot)
	if harvested := gardenByID(detail.Children)[second]; harvested.Status != gateway.GatewaySeedStatusHarvested || protocol.Deref(harvested.Reason) != "Moves work from the phone." {
		t.Fatalf("the plot's children read %+v, want the harvested one with its reason", detail.Children)
	}

	if failure := phone.move(elsewhere, gateway.GatewaySeedVerbWither, "Not mine to close."); !strings.Contains(failure, "no seed "+elsewhere) {
		t.Fatalf("withering another profile's seed failed with %q, want it to read as absent", failure)
	}
	shown, err := cli.SeedShow("theirs", elsewhere)
	if err != nil || shown.Seed.Status != "planted" {
		t.Fatalf("the work seed after the refused move: %+v, %v", shown, err)
	}
}

func TestGatewayKeepsTheGardenFromPhonesThatDoNotKnowIt(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	if err := w.InjectSession("mine", "mine", w.Path("mine"), protocol.SessionAgentClaude, app.SelectedProfile()); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedPlant("mine", "Hidden from api 5", "", "", ""); err != nil {
		t.Fatal(err)
	}
	phone := connectPhone(t, w, "installed", app.SelectedProfile())
	phone.hello(5)
	var welcome gateway.GatewayWelcome
	phone.next("welcome", &welcome)
	phone.send(gateway.GatewayPing{Cmd: "ping"})
	var pong gateway.GatewayPong
	phone.next("pong", &pong)
	if phone.seedIDs != nil || len(phone.seeds) != 0 {
		t.Fatalf("a phone on api 5 received the garden %v", phone.seedIDs)
	}
}

func TestGatewaySendsASeedsNewestLogThatFitsAFrameAndSaysWhenItsBodyCannot(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	if err := w.InjectSession("mine", "mine", w.Path("mine"), protocol.SessionAgentClaude, app.SelectedProfile()); err != nil {
		t.Fatal(err)
	}
	busy, err := cli.SeedPlant("mine", "A busy seed", "Short body.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		if _, err := cli.SeedNote("mine", busy.Seed.ID, fmt.Sprintf("entry %d %s", i, strings.Repeat("x", 25_000)), "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	vast, err := cli.SeedPlant("mine", "A vast plan", strings.Repeat("plan ", 20_000), "", "")
	if err != nil {
		t.Fatal(err)
	}

	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.welcomed()
	detail, failure := phone.readSeed(busy.Seed.ID)
	if failure != "" || detail.LogTotal != 6 || len(detail.Log) != 2 || !strings.HasPrefix(detail.Log[0].Body, "entry 5 ") || !strings.HasPrefix(detail.Log[1].Body, "entry 4 ") || detail.Body != "Short body." {
		t.Fatalf("the busy seed arrived with %d of %d entries (%s), want its body and the newest two of six 25 KB entries", len(detail.Log), detail.LogTotal, failure)
	}
	detail, failure = phone.readSeed(vast.Seed.ID)
	if failure != "" || detail.Body != "" || protocol.Deref(detail.BodyTooLargeBytes) != 100_000 || protocol.Deref(detail.MaxFrameBytes) != cloudlink.MaxMessageBytes {
		t.Fatalf("the vast seed arrived as body %d bytes, too large %v of max %v (%s), want no body and both sizes", len(detail.Body), detail.BodyTooLargeBytes, detail.MaxFrameBytes, failure)
	}
}

func (p *testPhone) seedChangesUntil(seedID string) []string {
	p.t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	var changed []string
	for {
		frame := p.receive(guard, "seed_changed for "+seedID)
		var message gateway.GatewaySeedChanged
		if json.Unmarshal(frame, &message) != nil || message.Event != "seed_changed" {
			continue
		}
		changed = append(changed, message.SeedID)
		if message.SeedID == seedID {
			return changed
		}
	}
}

func TestGatewayTellsThePhoneWhenASeedsNotesOrBodyChange(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "phone_access_enabled", "true")
	personal := app.SelectedProfile()
	work := createProfile(app, "Work")
	for _, s := range []struct{ id, profile string }{{"mine", personal}, {"theirs", work.ID}} {
		if err := w.InjectSession(s.id, s.id+" label", w.Path(s.id), protocol.SessionAgentClaude, s.profile); err != nil {
			t.Fatal(err)
		}
	}
	open, err := cli.SeedPlant("mine", "Open on the phone", "The first plan.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := cli.SeedPlant("theirs", "Work only", "Not for the personal phone.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	phone := connectPhone(t, w, "phone", personal)
	phone.welcomed()
	phone.readSeed(open.Seed.ID)

	if _, err := cli.SeedNote("theirs", elsewhere.Seed.ID, "Work goes on.", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedNote("mine", open.Seed.ID, "Handing off: read the new plan.", "handoff", false, nil); err != nil {
		t.Fatal(err)
	}
	changed := phone.seedChangesUntil(open.Seed.ID)
	detail, _ := phone.readSeed(open.Seed.ID)
	if len(detail.Log) == 0 || detail.Log[0].Body != "Handing off: read the new plan." {
		t.Fatalf("after seed_changed the seed's log reads %+v, want the handoff newest", detail.Log)
	}

	if _, err := cli.WithGardenProfile("", "mine").SeedEdit(open.Seed.ID, "The second plan.", garden.BodySHA256("The first plan."), ""); err != nil {
		t.Fatal(err)
	}
	changed = append(changed, phone.seedChangesUntil(open.Seed.ID)...)
	detail, _ = phone.readSeed(open.Seed.ID)
	if detail.Body != "The second plan." {
		t.Fatalf("after seed_changed the seed's body reads %q, want the edit", detail.Body)
	}
	if slices.Contains(changed, elsewhere.Seed.ID) {
		t.Fatalf("the personal phone heard seed_changed for %v, which includes the work seed %s", changed, elsewhere.Seed.ID)
	}
}
