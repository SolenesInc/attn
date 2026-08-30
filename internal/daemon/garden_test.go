package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func gardenCall(t *testing.T, run func(net.Conn)) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		run(server)
		_ = server.Close()
	}()
	defer client.Close()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func newGardenDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	now := string(protocol.TimestampNow())
	// The workspace is left off the stored record on purpose: a fixture that stamps the
	// column hides every reader that never asks the live registry.
	d.store.Add(&protocol.Session{
		ID: "sess-a", Label: "a",
		State: "idle", StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.register("ws-1", "a", "/tmp/a", "a0", false, false)
	d.workspaces.associateSession("sess-a", "ws-1", "a")
	return d
}

func plant(t *testing.T, d *Daemon, msg protocol.SeedPlantMessage) protocol.Seed {
	t.Helper()
	msg.Cmd = protocol.CmdSeedPlant
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlant(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plant %q: %v", msg.Title, protocol.Deref(resp.Error))
	}
	return resp.SeedPlantResult.Seed
}

func editSeed(t *testing.T, d *Daemon, seedID, body string) protocol.Seed {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedEdit(c, &protocol.SeedEditMessage{Cmd: protocol.CmdSeedEdit, SeedID: seedID, Body: body})
	})
	if !resp.Ok {
		t.Fatalf("edit %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedEditResult.Seed
}

func setSeedResume(t *testing.T, d *Daemon, seedID, resumeID, cwd, agent string, clear bool) protocol.Response {
	t.Helper()
	msg := protocol.SeedSetResumeMessage{
		Cmd: protocol.CmdSeedSetResume, SeedID: seedID,
		ResumeSessionID: protocol.Ptr(resumeID), ResumeCwd: protocol.Ptr(cwd), ResumeAgent: protocol.Ptr(agent),
	}
	if clear {
		msg.Clear = protocol.Ptr(true)
		msg.ResumeSessionID, msg.ResumeCwd, msg.ResumeAgent = nil, nil, nil
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedSetResume(c, &msg) })
}

func addGardenSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, State: "idle",
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.associateSession(id, "ws-1", id)
}

func move(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Seed {
	t.Helper()
	resp := transition(t, d, session, seedID, verb, reason, member)
	if !resp.Ok {
		t.Fatalf("%s %s: %v", verb, seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedTransitionResult.Seed
}

func transition(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Response {
	t.Helper()
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: string(verb),
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if reason != "" {
		msg.Reason = protocol.Ptr(reason)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
}

func note(t *testing.T, d *Daemon, session, seedID, body, member string) protocol.SeedNote {
	t.Helper()
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
	if !resp.Ok {
		t.Fatalf("note on %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNoteResult.Note
}

func show(t *testing.T, d *Daemon, seedID string) *protocol.SeedShowResult {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: seedID})
	})
	if !resp.Ok {
		t.Fatalf("show %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedShowResult
}

func TestGarden_FullLifeIsVisibleAtEveryStep(t *testing.T) {
	d := newGardenDaemon(t)
	var pushed [][]protocol.Seed
	d.gardenBroadcastHook = func(seeds []protocol.Seed, _ int) { pushed = append(pushed, seeds) }

	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "live a life"})

	tended := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	if tended.Status != garden.StatusGrowing || tended.TenderMember != "trellis" || tended.TenderSession != "" {
		t.Fatalf("tend did not claim the seed: %+v", tended)
	}

	note(t, d, "sess-a", seed.ID, "found the seam in internal/daemon", "trellis")

	harvested := move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "shipped it", "trellis")
	if harvested.Status != garden.StatusHarvested {
		t.Fatalf("harvest landed in %q", harvested.Status)
	}
	if harvested.Reason == nil || *harvested.Reason != "shipped it" {
		t.Fatalf("harvest did not record why: %+v", harvested)
	}
	if harvested.TenderSession != "" || harvested.TenderMember != "" {
		t.Fatalf("a harvested seed is still claimed: %+v", harvested)
	}

	replanted := move(t, d, "sess-a", seed.ID, garden.VerbReplant, "", "trellis")
	if replanted.Status != garden.StatusPlanted || replanted.Reason != nil {
		t.Fatalf("replant did not reopen the seed cleanly: %+v", replanted)
	}

	withered := move(t, d, "sess-a", seed.ID, garden.VerbWither, "nobody is picking this up", "trellis")
	if withered.Status != garden.StatusWithered {
		t.Fatalf("wither landed in %q", withered.Status)
	}

	if len(pushed) != 6 {
		t.Fatalf("the life produced %d garden pushes, want one per change", len(pushed))
	}
	states := make([]string, 0, len(pushed))
	for _, garden := range pushed {
		if len(garden) != 1 {
			t.Fatalf("a push carried %d seeds, want the one that exists", len(garden))
		}
		states = append(states, garden[0].Status)
	}
	want := []string{"planted", "growing", "growing", "harvested", "planted", "withered"}
	if !slices.Equal(states, want) {
		t.Fatalf("the panel saw %v, want %v", states, want)
	}
}

func TestSeedResumeIdentityPlantsSetsAndClearsAtomically(t *testing.T) {
	d := newGardenDaemon(t)
	cwd := t.TempDir()
	planted := plant(t, d, protocol.SeedPlantMessage{
		Title: "external conversation", ResumeSessionID: protocol.Ptr("native-1"),
		ResumeCwd: protocol.Ptr(cwd), ResumeAgent: protocol.Ptr("claude"),
	})
	if protocol.Deref(planted.ResumeSessionID) != "native-1" || protocol.Deref(planted.ResumeCwd) != cwd || protocol.Deref(planted.ResumeAgent) != "claude" {
		t.Fatalf("planted resume identity = %+v", planted)
	}

	set := setSeedResume(t, d, planted.ID, "native-2", cwd, "copilot", false)
	if !set.Ok || protocol.Deref(set.SeedSetResumeResult.Seed.ResumeSessionID) != "native-2" {
		t.Fatalf("set resume identity = %+v", set)
	}
	cleared := setSeedResume(t, d, planted.ID, "", "", "", true)
	if !cleared.Ok || cleared.SeedSetResumeResult.Seed.ResumeSessionID != nil || cleared.SeedSetResumeResult.Seed.ResumeCwd != nil || cleared.SeedSetResumeResult.Seed.ResumeAgent != nil {
		t.Fatalf("cleared resume identity = %+v", cleared)
	}

	partial := gardenCall(t, func(c net.Conn) {
		d.handleSeedSetResume(c, &protocol.SeedSetResumeMessage{
			Cmd: protocol.CmdSeedSetResume, SeedID: planted.ID, ResumeSessionID: protocol.Ptr("native-3"),
		})
	})
	if partial.Ok || !strings.Contains(protocol.Deref(partial.Error), "--cwd") {
		t.Fatalf("partial identity refusal = %+v", partial)
	}
	still, _, err := d.readSeed(planted.ID)
	if err != nil || still.ResumeSessionID != "" || still.ResumeCwd != "" || still.ResumeAgent != "" {
		t.Fatalf("partial write changed the cleared seed: seed=%+v err=%v", still, err)
	}
}

func TestSeedPlantRefusesPartialResumeIdentity(t *testing.T) {
	d := newGardenDaemon(t)
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{
			Cmd: protocol.CmdSeedPlant, Title: "partial", ResumeSessionID: protocol.Ptr("native-1"),
		})
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "--cwd") {
		t.Fatalf("partial plant refusal = %+v", resp)
	}
}

func TestGarden_ASecondSessionCannotTakeALiveClaim(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "contended"})

	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	editSeed(t, d, seed.ID, "edited body")

	refused := transition(t, d, "sess-b", seed.ID, garden.VerbTend, "", "alder")
	if refused.Ok {
		t.Fatal("a second session took a claim that was already held")
	}
	message := protocol.Deref(refused.Error)
	for _, want := range []string{seed.ID, "Trellis", "attn seed note"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the refusal does not name %q:\n%s", want, message)
		}
	}

	still := show(t, d, seed.ID).Seed
	if still.TenderSession != "" || still.TenderMember != "trellis" {
		t.Fatalf("the refused claim changed the tender: %+v", still)
	}

	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "trellis")
	taken := move(t, d, "sess-b", seed.ID, garden.VerbTend, "", "alder")
	if taken.TenderSession != "" || taken.TenderMember != "alder" {
		t.Fatalf("a parked seed did not hand over: %+v", taken)
	}
}

func TestGarden_ConcurrentClaimsProduceOneTender(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "raced for"})

	type outcome struct {
		resp    protocol.Response
		session string
	}
	results := make(chan outcome, 2)
	var start sync.WaitGroup
	start.Add(1)
	for _, session := range []string{"sess-a", "sess-b"} {
		go func() {
			start.Wait()
			results <- outcome{transition(t, d, session, seed.ID, garden.VerbTend, "", ""), session}
		}()
	}
	start.Done()

	var winners []string
	var refusals []string
	for range 2 {
		got := <-results
		if got.resp.Ok {
			winners = append(winners, got.resp.SeedTransitionResult.Seed.TenderSession)
			continue
		}
		refusals = append(refusals, protocol.Deref(got.resp.Error))
	}
	if len(winners) != 1 || len(refusals) != 1 {
		t.Fatalf("two simultaneous claims produced %d winners and %d refusals, want one of each", len(winners), len(refusals))
	}
	if !strings.Contains(refusals[0], winners[0]) {
		t.Fatalf("the loser was not told who won:\n%s", refusals[0])
	}
	if held := show(t, d, seed.ID).Seed.TenderSession; held != winners[0] {
		t.Fatalf("the stored tender is %q but %q was told it won", held, winners[0])
	}
}

func TestGarden_LogReadsNewestFirstAndSaysWhatItWithheld(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "with a log"})

	bodies := []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh"}
	for _, body := range bodies {
		note(t, d, "sess-a", seed.ID, body, "trellis")
	}

	shown := show(t, d, seed.ID)
	if shown.NotesTotal != len(bodies) {
		t.Fatalf("show reports %d notes, want %d", shown.NotesTotal, len(bodies))
	}
	if len(shown.Notes) != garden.ShowNotes {
		t.Fatalf("show rendered %d notes inline, want %d", len(shown.Notes), garden.ShowNotes)
	}
	if shown.Notes[0].Body != "seventh" {
		t.Fatalf("the log leads with %q, want the newest note", shown.Notes[0].Body)
	}
	if shown.Notes[0].AuthorMember != "trellis" || shown.Notes[0].AuthorSession != "sess-a" {
		t.Fatalf("a note does not record who wrote it: %+v", shown.Notes[0])
	}

	all := gardenCall(t, func(c net.Conn) {
		d.handleSeedNotes(c, &protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: seed.ID})
	})
	if !all.Ok {
		t.Fatalf("notes: %v", protocol.Deref(all.Error))
	}
	if len(all.SeedNotesResult.Notes) != len(bodies) || all.SeedNotesResult.Total != len(bodies) {
		t.Fatalf("the full log is %d of %d, want all %d", len(all.SeedNotesResult.Notes), all.SeedNotesResult.Total, len(bodies))
	}

	elsewhere := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "no log"})
	if got := show(t, d, elsewhere.ID); len(got.Notes) != 0 || got.NotesTotal != 0 {
		t.Fatalf("another seed's log leaked: %+v", got.Notes)
	}
}

func TestGarden_LifecycleRefusalsNameWhatIsWrong(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "refusals"})

	unknown := transition(t, d, "sess-a", seed.ID, "compost", "", "trellis")
	if unknown.Ok || !strings.Contains(protocol.Deref(unknown.Error), "harvest") {
		t.Fatalf("an unknown verb was not answered with the ones that exist: %+v", unknown)
	}

	wordless := transition(t, d, "sess-a", seed.ID, garden.VerbHarvest, "", "trellis")
	if wordless.Ok || !strings.Contains(protocol.Deref(wordless.Error), "-m") {
		t.Fatalf("a wordless harvest was not refused with the flag to fix it: %+v", wordless)
	}

	missing := transition(t, d, "sess-a", "s-zzzzzz", garden.VerbTend, "", "trellis")
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("a move on an unplanted seed was not refused by name: %+v", missing)
	}

	emptyNote := gardenCall(t, func(c net.Conn) {
		d.handleSeedNote(c, &protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seed.ID, Body: "  "})
	})
	if emptyNote.Ok || !strings.Contains(protocol.Deref(emptyNote.Error), "attn seed note") {
		t.Fatalf("an empty note was not refused with the command to fix it: %+v", emptyNote)
	}

	noSeed := gardenCall(t, func(c net.Conn) {
		d.handleSeedNote(c, &protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: "s-zzzzzz", Body: "into the void"})
	})
	if noSeed.Ok || !strings.Contains(protocol.Deref(noSeed.Error), "s-zzzzzz") {
		t.Fatalf("a note on an unplanted seed was written or refused vaguely: %+v", noSeed)
	}
}

func TestGarden_EveryMovePublishesItsOwnFact(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "facts"})

	var seen []string
	unsubscribe := d.eventBus.Subscribe(bus.Filter{"garden.*"}, func(ev bus.Event) {
		if ev.Subject != seed.ID {
			t.Errorf("fact %s names subject %q, want the seed", ev.Name, ev.Subject)
		}
		seen = append(seen, ev.Name)
	})
	defer unsubscribe()

	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	editSeed(t, d, seed.ID, "edited while tended")
	note(t, d, "sess-a", seed.ID, "on the log", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "done", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbReplant, "", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbWither, "", "trellis")

	want := []string{
		FactGardenTended, FactGardenBodyEdited, FactGardenNoted, FactGardenParked,
		FactGardenHarvested, FactGardenReplanted, FactGardenWithered,
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("the bus saw %v, want %v", seen, want)
	}
}

func TestGarden_EditChangesOnlyTheLivingBody(t *testing.T) {
	d := newGardenDaemon(t)
	crown := plant(t, d, protocol.SeedPlantMessage{Title: "Crown"})
	seed := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "Editable", Body: protocol.Ptr("old body"), PartOf: protocol.Ptr(crown.ID),
	})
	before := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")

	after := editSeed(t, d, seed.ID, "# New body\n\nStill the same seed.")

	if after.Body != "# New body\n\nStill the same seed." || after.Rev != before.Rev+1 {
		t.Fatalf("edited body/revision = %q/%d, want new body/rev %d", after.Body, after.Rev, before.Rev+1)
	}
	if after.ID != before.ID || after.Title != before.Title || after.Status != before.Status ||
		after.TenderSession != before.TenderSession || after.TenderMember != before.TenderMember ||
		!reflect.DeepEqual(after.Edges, before.Edges) || !reflect.DeepEqual(after.Vars, before.Vars) {
		t.Fatalf("edit changed seed identity/lifecycle: before=%+v after=%+v", before, after)
	}

	cleared := editSeed(t, d, seed.ID, "")
	if cleared.Body != "" {
		t.Fatalf("explicit empty edit left body %q", cleared.Body)
	}
}

func TestGarden_EditRejectsUnknownSeedAndOversizedBody(t *testing.T) {
	d := newGardenDaemon(t)
	unknown := gardenCall(t, func(c net.Conn) {
		d.handleSeedEdit(c, &protocol.SeedEditMessage{Cmd: protocol.CmdSeedEdit, SeedID: "s-ffffff", Body: "words"})
	})
	if unknown.Ok || !strings.Contains(protocol.Deref(unknown.Error), "s-ffffff") {
		t.Fatalf("unknown edit = %+v, want named seed refusal", unknown)
	}
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Bounded"})
	oversized := gardenCall(t, func(c net.Conn) {
		d.handleSeedEdit(c, &protocol.SeedEditMessage{
			Cmd: protocol.CmdSeedEdit, SeedID: seed.ID, Body: strings.Repeat("x", garden.MaxBodyBytes+1),
		})
	})
	if oversized.Ok || !strings.Contains(protocol.Deref(oversized.Error), "max_body_bytes") {
		t.Fatalf("oversized edit = %+v, want named body limit", oversized)
	}
}

func TestGarden_PlantListShowRoundTrip(t *testing.T) {
	d := newGardenDaemon(t)

	planted := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"),
		Title:           "Plant and see",
		Body:            protocol.Ptr("# slice 1\n\nthe first vertical"),
		Member:          protocol.Ptr("trellis"),
	})
	if err := garden.ValidateID(planted.ID); err != nil {
		t.Fatalf("plant returned an id that is not a seed id: %v", err)
	}
	if planted.Status != garden.StatusPlanted {
		t.Fatalf("a fresh seed is %q, want %q", planted.Status, garden.StatusPlanted)
	}
	if planted.StepSlug != "plant-see" {
		t.Fatalf("step slug = %q, want plant-see", planted.StepSlug)
	}
	if planted.PlanterSession != "sess-a" || planted.PlanterMember != "trellis" {
		t.Fatalf("planter not recorded: %+v", planted)
	}

	listResp := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, SourceSessionID: protocol.Ptr("sess-a")})
	})
	if !listResp.Ok {
		t.Fatalf("ls: %v", protocol.Deref(listResp.Error))
	}
	if got := listResp.SeedListResult; len(got.Seeds) != 1 || got.Seeds[0].ID != planted.ID {
		t.Fatalf("flag-free ls did not return the seed just planted: %+v", got)
	}

	showResp := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: planted.ID})
	})
	if !showResp.Ok {
		t.Fatalf("show: %v", protocol.Deref(showResp.Error))
	}
	shown := showResp.SeedShowResult.Seed
	if shown.Title != "Plant and see" || shown.Body != "# slice 1\n\nthe first vertical" {
		t.Fatalf("show lost the seed: %+v", shown)
	}
	if shown.Rev < 1 || shown.CreatedAt == "" {
		t.Fatalf("show did not carry the document's own revision and stamp: %+v", shown)
	}
	if shown.Edges == nil || shown.Vars == nil || shown.Template || shown.Gate {
		t.Fatalf("the designed schema is not whole on a fresh seed: %+v", shown)
	}
}

func TestGarden_ListReadsTheWholeGarden(t *testing.T) {
	d := newGardenDaemon(t)
	first := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "planted from a session"})
	second := plant(t, d, protocol.SeedPlantMessage{Title: "planted with no session at all"})

	fromSession := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, SourceSessionID: protocol.Ptr("sess-a")})
	}).SeedListResult
	sessionless := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList})
	}).SeedListResult

	for name, got := range map[string]*protocol.SeedListResult{"from a session": fromSession, "with no session": sessionless} {
		if len(got.Seeds) != 2 || got.Total != 2 {
			t.Fatalf("ls %s returned %d of %d, want the whole garden: %+v", name, len(got.Seeds), got.Total, got.Seeds)
		}
		if got.Seeds[0].ID != second.ID || got.Seeds[1].ID != first.ID {
			t.Fatalf("ls %s is not newest first: %+v", name, got.Seeds)
		}
	}
}

func TestGarden_PushCarriesTheWholeGardensCount(t *testing.T) {
	d := newGardenDaemon(t)
	var totals []int
	d.gardenBroadcastHook = func(_ []protocol.Seed, total int) { totals = append(totals, total) }

	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "one"})
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "two"})

	if len(totals) == 0 {
		t.Fatal("planting pushed no garden")
	}
	if got := totals[len(totals)-1]; got != 2 {
		t.Fatalf("push total = %d, want 2", got)
	}
}

func TestGarden_RefusalsNameWhatIsWrong(t *testing.T) {
	d := newGardenDaemon(t)

	empty := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: "   "})
	})
	if empty.Ok || !strings.Contains(protocol.Deref(empty.Error), "attn seed plant") {
		t.Fatalf("an empty title was not refused with the command to fix it: %+v", empty)
	}

	malformed := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: "nope"})
	})
	if malformed.Ok || !strings.Contains(protocol.Deref(malformed.Error), "seed id") {
		t.Fatalf("a malformed id was not refused by shape: %+v", malformed)
	}

	missing := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: "s-zzzzzz"})
	})
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("an unknown seed was not refused by name: %+v", missing)
	}
}

func TestGarden_OutpostRefusesEverySeedCommand(t *testing.T) {
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()

	calls := map[string]func(net.Conn){
		"plant": func(c net.Conn) {
			d.handleSeedPlant(c, &protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: "anything"})
		},
		"ls": func(c net.Conn) {
			d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList})
		},
		"show": func(c net.Conn) {
			d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: "s-7k3f9m"})
		},
		"tend": func(c net.Conn) {
			d.handleSeedTransition(c, &protocol.SeedTransitionMessage{
				Cmd: protocol.CmdSeedTransition, SeedID: "s-7k3f9m", Verb: string(garden.VerbTend),
			})
		},
		"note": func(c net.Conn) {
			d.handleSeedNote(c, &protocol.SeedNoteMessage{
				Cmd: protocol.CmdSeedNote, SeedID: "s-7k3f9m", Body: "anything",
			})
		},
		"notes": func(c net.Conn) {
			d.handleSeedNotes(c, &protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: "s-7k3f9m"})
		},
	}
	for verb, call := range calls {
		resp := gardenCall(t, call)
		if resp.Ok {
			t.Fatalf("seed %s answered on an outpost, want the fence", verb)
		}
		message := protocol.Deref(resp.Error)
		for _, want := range []string{garden.Surface, home, "attn enrollment leave", enrollment.PlanPath} {
			if !strings.Contains(message, want) {
				t.Fatalf("seed %s refusal does not name %q:\n%s", verb, want, message)
			}
		}
	}

	if seeds := initialStateEvent(t, d).Seeds; len(seeds) != 0 {
		t.Fatalf("an outpost's initial_state carries %d seeds, want none", len(seeds))
	}
}

func TestGarden_PlantingPushesTheGardenOnce(t *testing.T) {
	d := newGardenDaemon(t)

	var pushes int
	var last []protocol.Seed
	d.gardenBroadcastHook = func(seeds []protocol.Seed, _ int) {
		pushes++
		last = seeds
	}

	planted := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "see it appear"})

	if pushes != 1 {
		t.Fatalf("one planting produced %d garden pushes, want exactly 1", pushes)
	}
	if len(last) != 1 || last[0].ID != planted.ID {
		t.Fatalf("the pushed garden does not carry the new seed: %+v", last)
	}
}

func TestGarden_BulkPlantingCoalescesToOnePush(t *testing.T) {
	d := newGardenDaemon(t)
	var pushes int
	d.gardenBroadcastHook = func([]protocol.Seed, int) { pushes++ }

	d.coalesceSnapshots(func() {
		for _, title := range []string{"a", "b", "c"} {
			plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: title})
		}
	})
	if pushes != 1 {
		t.Fatalf("three plantings inside one coalesce produced %d pushes, want 1", pushes)
	}
}

func TestGarden_SeedsReachInitialState(t *testing.T) {
	d := newGardenDaemon(t)
	planted := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "on connect"})

	seeds := initialStateEvent(t, d).Seeds
	if len(seeds) != 1 || seeds[0].ID != planted.ID {
		t.Fatalf("initial_state does not carry the garden: %+v", seeds)
	}
}

func TestGarden_PlantingIsCreateOnly(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	seed := garden.Seed{ID: "s-7k3f9m", Title: "first", Status: garden.StatusPlanted}
	if _, err := d.plantSeed(*schema, seed); err != nil {
		t.Fatalf("first planting: %v", err)
	}
	seed.Title = "second"
	if _, err := d.plantSeed(*schema, seed); err == nil {
		t.Fatal("planting over an existing seed was allowed")
	}
}

func TestGarden_PlantingMintsAgainWhenAnIDIsTaken(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	if _, err := d.plantSeed(*schema, garden.Seed{ID: "s-7k3f9m", Title: "already here", Status: garden.StatusPlanted}); err != nil {
		t.Fatalf("seeding the collision: %v", err)
	}
	minted := []string{"s-7k3f9m", "s-7k3f9m", "s-fresh1"}
	d.gardenMintID = func() (string, error) {
		next := minted[0]
		minted = minted[1:]
		return next, nil
	}

	planted := plant(t, d, protocol.SeedPlantMessage{Title: "planted anyway"})
	if planted.ID != "s-fresh1" {
		t.Fatalf("seed id = %q, want the third mint after two taken ones", planted.ID)
	}
	if len(minted) != 0 {
		t.Fatalf("%d mints unused: the retry stopped early", len(minted))
	}

	d.gardenMintID = func() (string, error) { return "s-7k3f9m", nil }
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: "no id left"})
	})
	if resp.Ok {
		t.Fatal("a mint source that never moves was allowed to plant")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "random source") {
		t.Fatalf("refusal = %q, want it to name the mint source", msg)
	}
}

func TestGarden_CollectionsAreDeclaredOnStartup(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	d.ensureGardenCollections()

	for _, collection := range []string{garden.CollectionSeeds, garden.CollectionNotes} {
		if _, err := d.collectionFor(garden.Namespace, collection); err != nil {
			t.Fatalf("%s/%s is not declared after startup: %v", garden.Namespace, collection, err)
		}
	}
}

func TestSeedPlantRecordsDiscoveredFromBeforeMinting(t *testing.T) {
	d := newGardenDaemon(t)
	origin := plant(t, d, protocol.SeedPlantMessage{Title: "the work where this surfaced"})
	found := plant(t, d, protocol.SeedPlantMessage{
		Title: "the follow-up", DiscoveredFrom: protocol.Ptr(origin.ID),
	})
	if len(found.Edges) != 1 || found.Edges[0].Kind != garden.EdgeDiscoveredFrom || found.Edges[0].To != origin.ID {
		t.Fatalf("planted edges = %+v", found.Edges)
	}

	before, err := d.readGarden()
	if err != nil {
		t.Fatalf("read garden before refusal: %v", err)
	}
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{
			Cmd: protocol.CmdSeedPlant, Title: "must not land", DiscoveredFrom: protocol.Ptr("s-miss11"),
		})
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "s-miss11") {
		t.Fatalf("missing origin response = %+v", resp)
	}
	after, err := d.readGarden()
	if err != nil {
		t.Fatalf("read garden after refusal: %v", err)
	}
	if len(after.seeds) != len(before.seeds) {
		t.Fatalf("missing origin planted a seed: before=%d after=%d", len(before.seeds), len(after.seeds))
	}
}

func TestSeedTransitionCarriesForceAndRecordsTheTakeover(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-a")
	addGardenSession(t, d, "sess-b")

	seed := plant(t, d, protocol.SeedPlantMessage{Title: "work somebody is already doing"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")

	refused := transition(t, d, "sess-b", seed.ID, garden.VerbWither, "", "")
	if refused.Ok {
		t.Fatal("another session withered a live claim without forcing it")
	}
	if !strings.Contains(protocol.Deref(refused.Error), "--force") {
		t.Fatalf("the refusal does not say how to go through with it: %v", protocol.Deref(refused.Error))
	}

	msg := protocol.SeedTransitionMessage{
		Cmd:             protocol.CmdSeedTransition,
		SeedID:          seed.ID,
		Verb:            string(garden.VerbWither),
		SourceSessionID: protocol.Ptr("sess-b"),
		Force:           protocol.Ptr(true),
	}
	forced := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if !forced.Ok {
		t.Fatalf("a forced take-over was refused: %v", protocol.Deref(forced.Error))
	}
	if got := forced.SeedTransitionResult.Seed.Status; got != garden.StatusWithered {
		t.Fatalf("status = %q, want withered", got)
	}
	if got := forced.SeedTransitionResult.Seed.TenderSession; got != "" {
		t.Fatalf("tender session = %q, want the claim released", got)
	}
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read forced-move note: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "sess-b forced") || !strings.Contains(notes[0].Body, "sess-a held") {
		t.Fatalf("forced-move notes = %+v", notes)
	}
}

func TestSeedParkCommitsItsCommentAndKeepsContinuation(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "work to put down"})
	claimed := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	executionID := protocol.Deref(claimed.LastExecutionID)
	if executionID == "" {
		t.Fatal("tending did not preserve an execution")
	}

	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbPark),
		SourceSessionID: protocol.Ptr("sess-a"), Comment: protocol.Ptr("Waiting for the upstream API."),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if !resp.Ok {
		t.Fatalf("park with comment: %v", protocol.Deref(resp.Error))
	}
	parked := resp.SeedTransitionResult.Seed
	if parked.Status != garden.StatusDormant || parked.TenderSession != "" || protocol.Deref(parked.LastExecutionID) != executionID {
		t.Fatalf("parked seed = %+v, want dormant, unclaimed, execution %s", parked, executionID)
	}
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Body != "Waiting for the upstream API." || notes[0].AuthorSession != "sess-a" {
		t.Fatalf("park notes = %+v", notes)
	}
}

func TestSeedParkRollsBackWhenItsCommentCannotLand(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "work to keep claimed"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")

	d.gardenMintNoteID = func() (string, error) { return "n-000000", nil }
	note(t, d, "sess-a", seed.ID, "this id is already taken", "")
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbPark),
		SourceSessionID: protocol.Ptr("sess-a"), Comment: protocol.Ptr("must land atomically"),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "every one was taken") {
		t.Fatalf("park response = %+v", resp)
	}
	got, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != garden.StatusGrowing || got.TenderSession != "sess-a" {
		t.Fatalf("seed moved without its comment: %+v", got)
	}
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Body != "this id is already taken" {
		t.Fatalf("failed Park left a partial comment: %+v", notes)
	}
}

func TestSeedTransitionExplicitMemberOutlivesItsSourceSession(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "member work"})
	claimed := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "alder")
	if claimed.TenderMember != "alder" || claimed.TenderSession != "" {
		t.Fatalf("explicit member claim = member %q session %q", claimed.TenderMember, claimed.TenderSession)
	}

	d.store.Remove("sess-a")
	refused := transition(t, d, "sess-b", seed.ID, garden.VerbTend, "", "")
	if refused.Ok || !strings.Contains(protocol.Deref(refused.Error), "Alder") {
		t.Fatalf("second session after source ended = %+v, want member-held refusal", refused)
	}

	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbTend),
		SourceSessionID: protocol.Ptr("sess-b"), Force: protocol.Ptr(true),
	}
	forced := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if !forced.Ok {
		t.Fatalf("forced member takeover: %v", protocol.Deref(forced.Error))
	}
	if got := forced.SeedTransitionResult.Seed; got.TenderSession != "sess-b" || got.TenderMember != "" {
		t.Fatalf("forced claim = member %q session %q", got.TenderMember, got.TenderSession)
	}
}

func TestForcedSeedMoveRollsBackWhenItsAuditNoteCannotLand(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-a")
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "held work"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")

	d.gardenMintNoteID = func() (string, error) { return "n-000000", nil }
	note(t, d, "sess-a", seed.ID, "the note that already owns this id", "")
	beforeFacts, err := d.store.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("read facts before forced move: %v", err)
	}

	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbWither),
		SourceSessionID: protocol.Ptr("sess-b"), Force: protocol.Ptr(true),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "every one was taken") {
		t.Fatalf("forced move response = %+v", resp)
	}

	got, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read seed after refused batch: %v", err)
	}
	if got.Status != garden.StatusGrowing || got.TenderSession != "sess-a" {
		t.Fatalf("seed moved without its audit: %+v", got)
	}
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read notes after refused batch: %v", err)
	}
	if len(notes) != 1 || notes[0].Body != "the note that already owns this id" {
		t.Fatalf("failed batch left a false audit: %+v", notes)
	}
	afterFacts, err := d.store.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("read facts after forced move: %v", err)
	}
	if len(afterFacts) != len(beforeFacts) {
		t.Fatalf("failed batch announced facts: before=%d after=%d", len(beforeFacts), len(afterFacts))
	}
}

func TestEveryForcedSeedMoveRecordsWhoForcedAndWhoHeld(t *testing.T) {
	for _, verb := range garden.Verbs {
		t.Run(string(verb), func(t *testing.T) {
			d := newGardenDaemon(t)
			addGardenSession(t, d, "sess-a")
			addGardenSession(t, d, "sess-b")
			seed := plant(t, d, protocol.SeedPlantMessage{Title: "held work"})
			move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
			msg := protocol.SeedTransitionMessage{
				Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(verb),
				SourceSessionID: protocol.Ptr("sess-b"), Force: protocol.Ptr(true),
			}
			if verb == garden.VerbHarvest || verb == garden.VerbWither {
				msg.Reason = protocol.Ptr("done")
			}
			resp := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
			if !resp.Ok {
				t.Fatalf("forced %s: %v", verb, protocol.Deref(resp.Error))
			}
			notes, _, err := d.readNotes(seed.ID, 10)
			if err != nil {
				t.Fatalf("read notes: %v", err)
			}
			if len(notes) != 1 || !strings.Contains(notes[0].Body, "sess-b forced") || !strings.Contains(notes[0].Body, "sess-a held") {
				t.Fatalf("%s notes = %+v", verb, notes)
			}
		})
	}
}
