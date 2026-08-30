package daemon

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedSendToChiefTransfersOwnershipAndPreservesExecution(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "chief")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}
	seedWire := plant(t, d, protocol.SeedPlantMessage{Title: "Place this work"})
	move(t, d, "sess-a", seedWire.ID, garden.VerbTend, "", "")
	seed, doc, err := d.readSeed(seedWire.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldExecutionID := seed.LastExecutionID

	result, err := d.sendSeedToChief(&protocol.SeedSendToChiefMessage{
		Cmd: protocol.CmdSeedSendToChief, SeedID: seed.ID,
		ExpectedRev: int(doc.Rev), ExpectedTenderSession: seed.TenderSession,
		ExpectedTenderMember: seed.TenderMember, SourceSessionID: protocol.Ptr("sess-a"),
		Guidance: protocol.Ptr("Use branch feature/special in /tmp/special."),
	})
	if err != nil {
		t.Fatalf("sendSeedToChief: %v", err)
	}
	after, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TenderSession != "chief" || after.LastExecutionID != oldExecutionID {
		t.Fatalf("seed after Send to Chief = %+v, old execution %q", after, oldExecutionID)
	}
	if result.ChiefSessionID != "chief" || result.Seed.TenderSession != "chief" {
		t.Fatalf("result = %+v", result)
	}
	shown := show(t, d, seed.ID)
	if len(shown.Notes) == 0 || !strings.Contains(shown.Notes[0].Body, "feature/special") ||
		!strings.Contains(shown.Notes[0].Body, "Sent to Chief") {
		t.Fatalf("Chief note = %+v", shown.Notes)
	}
}

func TestSeedSendToChiefRefusesAChangedSeed(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "chief")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Changing work"})
	result, err := d.sendSeedToChief(&protocol.SeedSendToChiefMessage{
		Cmd: protocol.CmdSeedSendToChief, SeedID: seed.ID,
		ExpectedRev: seed.Rev - 1, ExpectedTenderSession: seed.TenderSession,
		ExpectedTenderMember: seed.TenderMember,
	})
	if err == nil || result != nil || !strings.Contains(err.Error(), "changed since you opened it") {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}
