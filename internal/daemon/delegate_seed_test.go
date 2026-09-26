package daemon

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

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

func TestPendingDelegationTenderBlocksAnotherClaim(t *testing.T) {
	d, _, sourceSessionID := newGardenDelegationDaemon(t)
	seed := plantForDelegation(t, d, sourceSessionID, "Reserved work")
	record, _, err := d.store.ClaimDelegationOperation(
		"request-reserved-tender", "operation-reserved-tender", "session-reserved-tender", "", "", `{}`, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	addGardenSession(t, d, record.Operation.SessionID)
	tendAs(t, d, seed.ID, record.Operation.SessionID)
	d.store.Remove(record.Operation.SessionID)
	addGardenSession(t, d, "contender-session")
	if _, _, err := d.applySeedTransition(seed.ID, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: "contender-session"}}); err == nil || !strings.Contains(err.Error(), "being tended") {
		t.Fatalf("contending tend error = %v, want pending successor to hold", err)
	}
	if err := d.store.UpdateDelegationOperation(record.Operation.OperationID, protocol.DelegationOperationStateFailed, "failed", "", "", "", nil, errors.New("failed"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.applySeedTransition(seed.ID, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: "contender-session"}}); err != nil {
		t.Fatalf("claim after failed operation: %v", err)
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
	msg := &resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Migrate the store to X"),
		Agent:           protocol.Ptr("codex"),
	}
	result, err := d.delegateResolved(msg)
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

func TestDelegationOnAnOutpostRefusesBeforeLaunch(t *testing.T) {
	d := newEnrolledDaemon(t, "d-"+strings.Repeat("a", 32))
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	var prompt string
	capturePrompt(t, backend, &prompt)

	_, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Migrate the store to X"),
		Agent:           protocol.Ptr("codex"),
	})
	if err == nil || !strings.Contains(err.Error(), "it is an outpost") {
		t.Fatalf("delegate() on an outpost = %v, want refusal", err)
	}
	if len(d.store.List("")) != 1 || prompt != "" {
		t.Fatalf("outpost refusal launched a worker or prompt: sessions=%d prompt=%q", len(d.store.List("")), prompt)
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
