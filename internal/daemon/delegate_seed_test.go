package daemon

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func newGardenDelegationDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, string) {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	return d, backend, sourceSessionID
}

func capturePrompt(t *testing.T, backend *fakeSpawnBackend, prompt *string) {
	t.Helper()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		raw, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Errorf("read initial prompt: %v", err)
			return
		}
		*prompt = string(raw)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Errorf("remove initial prompt: %v", err)
		}
	}
}

func TestDelegationPlantsASeedTendedByItsDelegate(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	var prompt string
	capturePrompt(t, backend, &prompt)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Label:           protocol.Ptr("Store migration"),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}

	seedID, bound := d.gardenDispatchCrown(result.SessionID)
	if !bound {
		t.Fatal("the delegation bound no seed to its session")
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read the bound seed: %v", err)
	}
	if seed.Body != "Migrate the store to X" {
		t.Fatalf("seed body = %q, want the brief", seed.Body)
	}
	if seed.Title != "Store migration" {
		t.Fatalf("seed title = %q, want the delegation's name", seed.Title)
	}
	if seed.TenderSession != result.SessionID {
		t.Fatalf("tender = %q, want the delegate session %q", seed.TenderSession, result.SessionID)
	}
	if seed.Status != garden.StatusGrowing {
		t.Fatalf("status = %q, want growing — a tended seed is not still planted", seed.Status)
	}
	if seed.PlanterSession != sourceSessionID {
		t.Fatalf("planter = %q, want the delegating session %q", seed.PlanterSession, sourceSessionID)
	}
	if !strings.Contains(prompt, seedID) {
		t.Fatalf("the delegate's prompt never names its seed %s:\n%s", seedID, prompt)
	}
	for _, verb := range []string{"attn seed show", "attn seed note", "attn seed harvest"} {
		if !strings.Contains(prompt, verb) {
			t.Fatalf("the delegate's prompt omits %q", verb)
		}
	}
	for _, removed := range []string{"attn seed attach", "attn seed detach", "attn seed link", "attn seed wither", "attn ticket"} {
		if strings.Contains(prompt, removed) {
			t.Fatalf("the delegate's prompt kept standing garden copy %q", removed)
		}
	}
	ticket, err := d.store.ActiveTicketForSession(result.SessionID)
	if err != nil {
		t.Fatalf("ActiveTicketForSession: %v", err)
	}
	if ticket != nil {
		t.Fatalf("the delegation created a ticket: %+v", ticket)
	}
}

func TestDelegationAtACrownBindsItWithoutPlanting(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	crown := plantForDelegation(t, d, sourceSessionID, "The epic")

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "work the plot",
		Plot:            protocol.Ptr(crown.ID),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}

	bound, ok := d.gardenDispatchCrown(result.SessionID)
	if !ok || bound != crown.ID {
		t.Fatalf("bound seed = %q, want the crown %q", bound, crown.ID)
	}
	read, err := d.readGarden()
	if err != nil {
		t.Fatalf("readGarden: %v", err)
	}
	if len(read.seeds) != 1 {
		t.Fatalf("the garden holds %d seeds, want only the crown", len(read.seeds))
	}
	seed, _, err := d.readSeed(crown.ID)
	if err != nil {
		t.Fatalf("read the crown: %v", err)
	}
	if seed.TenderSession != result.SessionID {
		t.Fatalf("the crown is tended by %q, want the delegate %q", seed.TenderSession, result.SessionID)
	}
	if seed.Status != garden.StatusGrowing {
		t.Fatalf("the crown is %s, want growing once a delegate holds it", seed.Status)
	}
}

func TestDelegationAtASeedHeldByADeadSessionRebindsIt(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	seed := plantForDelegation(t, d, sourceSessionID, "Held by a ghost")
	addGardenSession(t, d, "gone-session")
	tendAs(t, d, seed.ID, "gone-session")
	d.store.Remove("gone-session")

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "take it over",
		Plot:            protocol.Ptr(seed.ID),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	read, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	if read.TenderSession != result.SessionID {
		t.Fatalf("tender = %q, want the new delegate %q", read.TenderSession, result.SessionID)
	}
}

func TestDelegationAtASeedHeldByALiveSessionRefusesBeforeAnythingIsCreated(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	seed := plantForDelegation(t, d, sourceSessionID, "Somebody else's work")
	first, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "work it",
		Plot:            protocol.Ptr(seed.ID),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("first delegate(): %v", err)
	}
	holder := first.SessionID

	sessionsBefore := len(d.store.List(""))
	_, err = d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "take it over",
		Plot:            protocol.Ptr(seed.ID),
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil {
		t.Fatalf("the dispatch took a seed held by a live session")
	}
	if !strings.Contains(err.Error(), seed.ID) || !strings.Contains(err.Error(), holder) {
		t.Fatalf("the refusal names neither the seed nor its tender: %v", err)
	}
	if got := len(d.store.List("")); got != sessionsBefore {
		t.Fatalf("the refused dispatch created %d session(s)", got-sessionsBefore)
	}
	read, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	if read.TenderSession != holder {
		t.Fatalf("tender = %q, want the live holder %q", read.TenderSession, holder)
	}
}

func TestDelegationAtASeedTheDelegatorHoldsHandsItOver(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	seed := plantForDelegation(t, d, sourceSessionID, "Mine until now")
	tendAs(t, d, seed.ID, sourceSessionID)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "here, you take it",
		Plot:            protocol.Ptr(seed.ID),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	read, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	if read.TenderSession != result.SessionID {
		t.Fatalf("tender = %q, want the delegate %q", read.TenderSession, result.SessionID)
	}
}

func TestDelegationAtAClosedSeedRefuses(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	seed := plantForDelegation(t, d, sourceSessionID, "Already done")
	if _, _, err := d.applySeedTransition(seed.ID, garden.VerbHarvest, garden.Ask{Actor: garden.Tender{Session: sourceSessionID}, Reason: "done"}); err != nil {
		t.Fatalf("harvest: %v", err)
	}

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "work it",
		Plot:            protocol.Ptr(seed.ID),
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil || !strings.Contains(err.Error(), "replant") {
		t.Fatalf("dispatch at a harvested seed: %v, want a refusal naming replant", err)
	}
}

func tendAs(t *testing.T, d *Daemon, seedID, sessionID string) {
	t.Helper()
	if _, _, err := d.applySeedTransition(seedID, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: sessionID}}); err != nil {
		t.Fatalf("tend %s as %s: %v", seedID, sessionID, err)
	}
}

func TestDelegationRecoveryRebindsTheSameSeed(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	msg := &protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	}
	result, err := d.delegate(msg)
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	first, _ := d.gardenDispatchCrown(result.SessionID)

	again, err := d.bindDelegationSeed(result.SessionID, sourceSessionID, "Migrate the store to X", "Store migration", "", "", "", false)
	if err != nil {
		t.Fatalf("re-bind: %v", err)
	}
	if again != first {
		t.Fatalf("re-bind produced %q, want the already-bound %q", again, first)
	}
	read, err := d.readGarden()
	if err != nil {
		t.Fatalf("readGarden: %v", err)
	}
	if len(read.seeds) != 1 {
		t.Fatalf("the garden holds %d seeds; a re-bind planted a second one", len(read.seeds))
	}
}

func TestDelegationOnAnOutpostBindsNoSeedAndStillLaunches(t *testing.T) {
	d := newEnrolledDaemon(t, "d-"+strings.Repeat("a", 32))
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	var prompt string
	capturePrompt(t, backend, &prompt)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() on an outpost: %v", err)
	}
	if bound, ok := d.gardenDispatchCrown(result.SessionID); ok {
		t.Fatalf("an outpost bound seed %q", bound)
	}
	if strings.Contains(prompt, "attn seed note") {
		t.Fatalf("the delegate was pointed at a garden that is not here:\n%s", prompt)
	}
	if session := d.store.Get(result.SessionID); session == nil {
		t.Fatalf("the delegation did not launch on an outpost")
	}
}

func plantForDelegation(t *testing.T, d *Daemon, sessionID, title string) protocol.Seed {
	t.Helper()
	msg := protocol.SeedPlantMessage{
		Cmd: protocol.CmdSeedPlant, Title: title, SourceSessionID: protocol.Ptr(sessionID),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlant(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plant %q: %v", title, protocol.Deref(resp.Error))
	}
	return resp.SeedPlantResult.Seed
}

func TestStatusReportsLandOnTheBoundSeedsLog(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	seedID, _ := d.gardenDispatchCrown(result.SessionID)
	bindLegacyTicket(t, d, result.SessionID, sourceSessionID)

	awaitSeedNotes(t, d, seedID, 2, func() {
		for _, report := range []struct{ state, comment string }{
			{string(protocol.DispatchWorkStateInProgress), "reading the store layer"},
			{string(protocol.DispatchWorkStateReadyForReview), "PR #1 is up"},
		} {
			resp := callSetTicketStatus(t, d, result.SessionID, report.state, report.comment)
			if resp.TicketStatusResult == nil || !resp.TicketStatusResult.Applied {
				t.Fatalf("report %s was not applied: %+v", report.state, resp.TicketStatusResult)
			}
		}
	})

	log := show(t, d, seedID)
	if log.NotesTotal != 2 {
		t.Fatalf("the seed's log holds %d entries, want one per report", log.NotesTotal)
	}
	// Matched by content rather than position: two writes inside one clock tick share a stamp, and the log's tiebreaker is the note id.
	for _, want := range []struct{ state, comment string }{
		{"in_progress", "reading the store layer"},
		{"ready_for_review", "PR #1 is up"},
	} {
		found := false
		for _, note := range log.Notes {
			if strings.Contains(note.Body, want.state) && strings.Contains(note.Body, want.comment) {
				found = true
				if note.AuthorSession != result.SessionID {
					t.Fatalf("author of the %s note = %q, want the reporting delegate", want.state, note.AuthorSession)
				}
			}
		}
		if !found {
			t.Fatalf("no note reported %s with its comment; the log holds %+v", want.state, log.Notes)
		}
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	if seed.Status != garden.StatusGrowing {
		t.Fatalf("status = %q; a status report must not move the seed's lifecycle", seed.Status)
	}
}

func TestCompletedReportDoesNotHarvestTheSeed(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Migrate the store to X", Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	seedID, _ := d.gardenDispatchCrown(result.SessionID)
	bindLegacyTicket(t, d, result.SessionID, sourceSessionID)

	awaitSeedNotes(t, d, seedID, 1, func() {
		callSetTicketStatus(t, d, result.SessionID, string(protocol.DispatchWorkStateCompleted), "merged")
	})

	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	if garden.Closed(seed.Status) {
		t.Fatalf("status = %q; the seed closed on a report nobody accepted yet", seed.Status)
	}
	if seed.TenderSession != result.SessionID {
		t.Fatalf("tender = %q; a report must not release the claim", seed.TenderSession)
	}
}

func TestNudgingSomebodyElsesTicketMirrorsNothing(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	worker, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Migrate the store to X", Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	peer, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Something else entirely", Label: protocol.Ptr("Peer work"), Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	workerSeed, _ := d.gardenDispatchCrown(worker.SessionID)
	peerSeed, _ := d.gardenDispatchCrown(peer.SessionID)
	workerTicketID := bindLegacyTicket(t, d, worker.SessionID, sourceSessionID)

	awaitStatusHandled(t, d, &protocol.SetTicketStatusMessage{
		Cmd:             protocol.CmdSetTicketStatus,
		SourceSessionID: peer.SessionID,
		WorkState:       protocol.DispatchWorkStateNeedsInput,
		Comment:         protocol.Ptr("waiting on you"),
		TicketID:        protocol.Ptr(workerTicketID),
	})

	if total := show(t, d, workerSeed).NotesTotal; total != 0 {
		t.Fatalf("a peer's nudge wrote %d notes onto the worker's log", total)
	}
	if total := show(t, d, peerSeed).NotesTotal; total != 0 {
		t.Fatalf("a peer's nudge wrote %d notes onto its own log", total)
	}
}

func TestAgentMsgToASeedReachesItsTender(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Migrate the store to X", Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	seedID, _ := d.gardenDispatchCrown(result.SessionID)

	tender, err := d.seedTenderSession(seedID)
	if err != nil {
		t.Fatalf("seedTenderSession(%s): %v", seedID, err)
	}
	if tender != result.SessionID {
		t.Fatalf("resolved %q, want the delegate session %q", tender, result.SessionID)
	}
}

func TestAgentMsgToAnUntendedSeedRefusesByName(t *testing.T) {
	d, _, sourceSessionID := newGardenDelegationDaemon(t)
	seed := plantForDelegation(t, d, sourceSessionID, "Nobody has this")

	_, err := d.seedTenderSession(seed.ID)
	if err == nil {
		t.Fatal("an untended seed resolved to somebody")
	}
	for _, want := range []string{"nobody is tending", seed.ID, "attn seed note"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// Waits on the note fact itself: the status reply is written before the mirror runs, so reading the log straight after the reply would race the write.
func awaitSeedNotes(t *testing.T, d *Daemon, seedID string, want int, work func()) {
	t.Helper()
	landed := make(chan struct{}, want+4)
	unsubscribe := d.eventBus.Subscribe(bus.Filter{FactGardenNoted}, func(ev bus.Event) {
		if ev.Subject == seedID {
			landed <- struct{}{}
		}
	})
	defer unsubscribe()
	work()
	for i := 0; i < want; i++ {
		select {
		case <-landed:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d notes reached %s", i, want, seedID)
		}
	}
}

// Returns when the handler has finished, not when it replied — the mirror runs after the reply. The signal is the server side of the pipe closing.
func awaitStatusHandled(t *testing.T, d *Daemon, msg *protocol.SetTicketStatusMessage) {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		d.handleSetTicketStatus(server, msg)
		_ = server.Close()
	}()
	defer client.Close()
	if _, err := io.ReadAll(client); err != nil {
		t.Fatalf("read the status handler to completion: %v", err)
	}
}

func TestBroadcastSessionCarriesTheSeedItReportsTo(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Report on a seed",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	seedID, bound := d.gardenDispatchCrown(result.SessionID)
	if !bound {
		t.Fatal("the delegation bound no seed")
	}

	delegated := d.sessionForBroadcast(d.store.Get(result.SessionID))
	if protocol.Deref(delegated.SeedID) != seedID {
		t.Fatalf("delegated session seed_id = %v, want %s", delegated.SeedID, seedID)
	}
	source := d.sessionForBroadcast(d.store.Get(sourceSessionID))
	if source.SeedID != nil {
		t.Fatalf("dispatching session seed_id = %v, want unset", source.SeedID)
	}
}

func TestSeedDecorationSeesADispatchRecordedAfterTheFirstBroadcast(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	if s := d.sessionForBroadcast(d.store.Get(sourceSessionID)); s.SeedID != nil {
		t.Fatalf("seed_id = %v before any dispatch, want unset", s.SeedID)
	}
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Report on a seed",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	seedID, _ := d.gardenDispatchCrown(result.SessionID)
	if got := d.sessionForBroadcast(d.store.Get(result.SessionID)); protocol.Deref(got.SeedID) != seedID {
		t.Fatalf("seed_id = %v after dispatch, want %s", got.SeedID, seedID)
	}
}

func TestDelegationFromADelegateNestsItsSeedUnderTheCallers(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)

	first, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "scout the flaky test",
		Label:           protocol.Ptr("the scout"),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("first delegate(): %v", err)
	}
	firstSeedID, ok := d.gardenDispatchCrown(first.SessionID)
	if !ok {
		t.Fatal("the first delegation bound no seed")
	}
	firstSeed, _, err := d.readSeed(firstSeedID)
	if err != nil {
		t.Fatalf("read the first seed: %v", err)
	}
	for _, edge := range firstSeed.Edges {
		if edge.Kind == garden.EdgePartOf {
			t.Fatalf("the first hop nests under %q; a caller without a seed nests under nothing", edge.To)
		}
	}

	second, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: first.SessionID,
		Brief:           "fix what the scout found",
		Label:           protocol.Ptr("the fix"),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("second delegate(): %v", err)
	}
	secondSeedID, ok := d.gardenDispatchCrown(second.SessionID)
	if !ok {
		t.Fatal("the second delegation bound no seed")
	}
	secondSeed, _, err := d.readSeed(secondSeedID)
	if err != nil {
		t.Fatalf("read the second seed: %v", err)
	}
	var nestedUnder string
	for _, edge := range secondSeed.Edges {
		if edge.Kind == garden.EdgePartOf {
			nestedUnder = edge.To
		}
	}
	if nestedUnder != firstSeedID {
		t.Fatalf("the delegate's delegation nests under %q, want its caller's seed %q", nestedUnder, firstSeedID)
	}

	wire, err := json.Marshal([]protocol.Session{
		*d.sessionForBroadcast(d.store.Get(first.SessionID)),
		*d.sessionForBroadcast(d.store.Get(second.SessionID)),
	})
	if err != nil {
		t.Fatalf("encode broadcast sessions: %v", err)
	}
	var roundTripped []protocol.Session
	if err := json.Unmarshal(wire, &roundTripped); err != nil {
		t.Fatalf("decode broadcast sessions: %v", err)
	}
	if len(roundTripped) != 2 {
		t.Fatalf("round-tripped sessions = %d, want 2", len(roundTripped))
	}
	if got := protocol.Deref(roundTripped[0].SeedID); got != firstSeedID {
		t.Fatalf("first delegate seed_id = %q, want %q", got, firstSeedID)
	}
	if got := protocol.Deref(roundTripped[0].DispatcherSessionID); got != sourceSessionID {
		t.Fatalf("first delegate dispatcher_session_id = %q, want %q", got, sourceSessionID)
	}
	if got := protocol.Deref(roundTripped[1].SeedID); got != secondSeedID {
		t.Fatalf("second delegate seed_id = %q, want %q", got, secondSeedID)
	}
	if got := protocol.Deref(roundTripped[1].DispatcherSessionID); got != first.SessionID {
		t.Fatalf("second delegate dispatcher_session_id = %q, want %q", got, first.SessionID)
	}
}
