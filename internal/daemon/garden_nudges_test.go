package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedNudges_InjectionLeavesTheBellUnreadUntilShow(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	doorbell := &recordingDoorbell{}
	fixture.d.ptyBackend = doorbell.backend()
	drains := observeAgentMailboxDrains(t, fixture.d)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "look now", true)
	if delivered := drains.next(); delivered != 1 {
		t.Fatalf("drain delivered %d bells, want 1", delivered)
	}
	prompts := doorbell.pasted()
	if len(prompts) != 1 || prompts[0] != agentMailboxDoorbellText {
		t.Fatalf("doorbells = %q, want one generic inbox notification", prompts)
	}
	if unread := queuedSeedBells(t, fixture.d, "sess-b"); len(unread) != 1 {
		t.Fatalf("injected bell is not durably unread: %q", unread)
	}

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "still unread", true)
	if prompts := doorbell.pasted(); len(prompts) != 1 {
		t.Fatalf("unread seed rang %d times, want one: %q", len(prompts), prompts)
	}

	resp := gardenCall(t, func(c net.Conn) {
		fixture.d.handleSeedShow(c, &protocol.SeedShowMessage{
			Cmd: protocol.CmdSeedShow, SeedID: fixture.leaf.ID, SourceSessionID: protocol.Ptr("sess-b"),
		})
	})
	if !resp.Ok {
		t.Fatalf("show: %v", protocol.Deref(resp.Error))
	}
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "after read", true)
	if delivered := drains.next(); delivered != 1 {
		t.Fatalf("post-read drain delivered %d bells, want 1", delivered)
	}
	if prompts := doorbell.pasted(); len(prompts) != 2 {
		t.Fatalf("read did not re-arm the bell: %q", prompts)
	}
}

func TestSeedNudges_FailedShowDoesNotReadTheBell(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "first", true)
	first, err := fixture.d.store.UnreadAgentMailboxDeliveries("sess-b")
	if err != nil || len(first) != 1 {
		t.Fatalf("first bell = %+v err=%v", first, err)
	}

	root := t.TempDir()
	fixture.d.store.SetSetting(SettingNotebookRoot, root)
	if err := os.Mkdir(filepath.Join(root, "seeds"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "seeds", fixture.leaf.ID)); err != nil {
		t.Fatal(err)
	}
	resp := gardenCall(t, func(c net.Conn) {
		fixture.d.handleSeedShow(c, &protocol.SeedShowMessage{
			Cmd: protocol.CmdSeedShow, SeedID: fixture.leaf.ID, SourceSessionID: protocol.Ptr("sess-b"),
		})
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "not a real directory") {
		t.Fatalf("show through invalid artifact directory = %+v", resp)
	}

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "second", true)
	remaining, err := fixture.d.store.UnreadAgentMailboxDeliveries("sess-b")
	if err != nil || len(remaining) != 1 || remaining[0].Item.ID != first[0].Item.ID {
		t.Fatalf("failed show consumed the bell: before=%+v after=%+v err=%v", first, remaining, err)
	}
}

type seededNudgeGarden struct {
	d                  *Daemon
	crown, child, leaf protocol.Seed
}

// newSeededNudgeGarden is a copy-shaped fixture: several live sessions and a
// real two-level plot already exist before a test starts exercising bells.
func newSeededNudgeGarden(t *testing.T) seededNudgeGarden {
	t.Helper()
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	addGardenSession(t, d, "sess-c")
	addGardenSession(t, d, "sess-d")
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "ship seed nudges"})
	child := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "daemon mechanics", PartOf: protocol.Ptr(crown.ID),
	})
	leaf := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "delivery proof", PartOf: protocol.Ptr(child.ID),
	})
	return seededNudgeGarden{d: d, crown: crown, child: child, leaf: leaf}
}

func watchSeed(t *testing.T, d *Daemon, sessionID, seedID string, unwatch bool) *protocol.SeedWatchResult {
	t.Helper()
	msg := protocol.SeedWatchMessage{
		Cmd: protocol.CmdSeedWatch, SourceSessionID: sessionID, SeedID: seedID,
	}
	if unwatch {
		msg.Unwatch = protocol.Ptr(true)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedWatch(c, &msg) })
	if !resp.Ok {
		t.Fatalf("watch %s from %s: %v", seedID, sessionID, protocol.Deref(resp.Error))
	}
	return resp.SeedWatchResult
}

func ringingNote(t *testing.T, d *Daemon, sessionID, seedID, body string, ring bool) protocol.SeedNote {
	t.Helper()
	msg := protocol.SeedNoteMessage{
		Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body,
		SourceSessionID: protocol.Ptr(sessionID),
	}
	if ring {
		msg.Ring = protocol.Ptr(true)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
	if !resp.Ok {
		t.Fatalf("note on %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNoteResult.Note
}

func queuedSeedBells(t *testing.T, d *Daemon, sessionID string) []string {
	t.Helper()
	messages, err := d.store.UnreadAgentMailboxDeliveries(sessionID)
	if err != nil {
		t.Fatalf("queued bells for %s: %v", sessionID, err)
	}
	contents := make([]string, 0, len(messages))
	for _, delivery := range messages {
		contents = append(contents, mailboxItemContent(delivery))
	}
	return contents
}

func assertOneSeedBell(t *testing.T, d *Daemon, sessionID, seedID, event string) {
	t.Helper()
	queued := queuedSeedBells(t, d, sessionID)
	if len(queued) != 1 || !strings.Contains(queued[0], seedID+" moved: "+event) {
		t.Fatalf("queued bells for %s = %q, want one %s/%s doorbell", sessionID, queued, seedID, event)
	}
}

func TestSeedNudges_DispatcherHearsTheDelegatesHarvest(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	d := fixture.d
	move(t, d, "sess-b", fixture.leaf.ID, garden.VerbTend, "", "")
	if err := d.recordGardenDispatch("sess-b", fixture.leaf.ID, "sess-a", "/tmp/a", "codex", false); err != nil {
		t.Fatalf("record dispatch: %v", err)
	}

	move(t, d, "sess-b", fixture.leaf.ID, garden.VerbHarvest, "proof complete", "")
	assertOneSeedBell(t, d, "sess-a", fixture.leaf.ID, "harvested")
}

func TestSeedNudges_UnblockedBellLivesWithItsRemoteTender(t *testing.T) {
	fixture, outpost, remoteSeed := remoteTenderNudgeFixture(t)

	fixture.d.ringSeedUnblocked([]garden.Seed{remoteSeed})

	waitFor(t, "the outpost to persist the remote tender's bell", func() bool {
		items, err := outpost.store.UnreadGardenSeedMailboxItems("remote-tender")
		return err == nil && len(items) == 1 && items[0].SeedID == remoteSeed.ID && items[0].EventKind == gardenRingUnblocked
	})
	if items, err := fixture.d.store.UnreadGardenSeedMailboxItems("remote-tender"); err != nil || len(items) != 0 {
		t.Fatalf("home mailbox items = %+v err=%v, want none for an outpost-owned session", items, err)
	}
	outpost.clearRemoteGardenBellAuthorization()
	resp := callAgentInboxBatch(t, outpost, "remote-tender", 10)
	if resp.Ok {
		t.Fatalf("outpost inbox read without fresh home authorization = %+v, want refusal", resp)
	}
	if items, err := outpost.store.UnreadGardenSeedMailboxItems("remote-tender"); err != nil || len(items) != 1 {
		t.Fatalf("outpost items while disconnected = %+v err=%v, want the unread bell preserved", items, err)
	}
	fixture.d.reconcileRemoteGardenSeedBells()
	waitFor(t, "the reconnected home to authorize the remote tender's bell", func() bool {
		outpost.remoteGardenBellMu.RLock()
		defer outpost.remoteGardenBellMu.RUnlock()
		return outpost.remoteGardenBellAllowed["remote-tender"][remoteSeed.ID]
	})
	resp = callAgentInboxBatch(t, outpost, "remote-tender", 10)
	if !resp.Ok || resp.AgentInboxBatchResult == nil || len(resp.AgentInboxBatchResult.Items) != 1 {
		t.Fatalf("outpost inbox = %+v error=%q, want the forwarded bell", resp, protocol.Deref(resp.Error))
	}
	if got := resp.AgentInboxBatchResult.Items[0].Content; !strings.Contains(got, remoteSeed.ID+" moved: "+gardenRingUnblocked) {
		t.Fatalf("outpost inbox content = %q, want the unblocked seed", got)
	}
	if items, err := outpost.store.UnreadGardenSeedMailboxItems("remote-tender"); err != nil || len(items) != 0 {
		t.Fatalf("outpost unread items after inbox = %+v err=%v, want none", items, err)
	}
}

func TestSeedNudges_RemoteBellExpiresWhenTheTenderMoves(t *testing.T) {
	fixture, outpost, remoteSeed := remoteTenderNudgeFixture(t)
	fixture.d.ringSeedUnblocked([]garden.Seed{remoteSeed})
	waitFor(t, "the outpost to persist the remote tender's bell", func() bool {
		items, err := outpost.store.UnreadGardenSeedMailboxItems("remote-tender")
		return err == nil && len(items) == 1
	})

	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: remoteSeed.ID, Verb: string(garden.VerbTend),
		SourceSessionID: protocol.Ptr("sess-b"), Force: protocol.Ptr(true),
	}
	resp := gardenCall(t, func(c net.Conn) { fixture.d.handleSeedTransition(c, &msg) })
	if !resp.Ok {
		t.Fatalf("retender %s: %v", remoteSeed.ID, protocol.Deref(resp.Error))
	}
	waitFor(t, "the outpost to expire the old tender's bell", func() bool {
		items, err := outpost.store.UnreadGardenSeedMailboxItems("remote-tender")
		return err == nil && len(items) == 0
	})
	resp = callAgentInboxBatch(t, outpost, "remote-tender", 10)
	if !resp.Ok || resp.AgentInboxBatchResult == nil || len(resp.AgentInboxBatchResult.Items) != 0 {
		t.Fatalf("old tender inbox = %+v error=%q, want no stale bell", resp, protocol.Deref(resp.Error))
	}
}

func remoteTenderNudgeFixture(t *testing.T) (seededNudgeGarden, *Daemon, garden.Seed) {
	t.Helper()
	fixture := newSeededNudgeGarden(t)
	outpost := startAgentCloseOutpost(t, fixture.d, remoteAgentCloseSession("remote-tender", "Remote tender"))
	if _, err := enrollment.Enroll(outpost.dataRoot, fixture.d.daemonInstanceID); err != nil {
		t.Fatalf("enroll the remote daemon as an outpost: %v", err)
	}
	planted := plant(t, fixture.d, protocol.SeedPlantMessage{Title: "remote tender bell"})
	move(t, fixture.d, "remote-tender", planted.ID, garden.VerbTend, "", "")
	seed, _, err := fixture.d.readSeed(planted.ID)
	if err != nil {
		t.Fatalf("read remote-tendered seed: %v", err)
	}
	return fixture, outpost, seed
}

func TestSeedNudges_NotesRingOnlyByChoice(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "ordinary progress", false)
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("plain note rang: %q", queued)
	}
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "please look", true)
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")
}

func TestSeedNudges_CrownWatchBubblesFromAGrandchild(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.crown.ID, false)

	move(t, fixture.d, "sess-c", fixture.leaf.ID, garden.VerbTend, "", "")
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "tended")
}

func TestSeedNudges_CoalesceUntilShowAndThenRingAgain(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "first", true)
	move(t, fixture.d, "sess-c", fixture.leaf.ID, garden.VerbTend, "", "")
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")

	resp := gardenCall(t, func(c net.Conn) {
		fixture.d.handleSeedShow(c, &protocol.SeedShowMessage{
			Cmd: protocol.CmdSeedShow, SeedID: fixture.leaf.ID, SourceSessionID: protocol.Ptr("sess-b"),
		})
	})
	if !resp.Ok || !resp.SeedShowResult.Watching {
		t.Fatalf("show after watch = %+v", resp)
	}
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("show left a queued bell: %q", queued)
	}

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "after the read", true)
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")
}

func TestSeedNudges_NotesReadResetsTheBell(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "first", true)

	resp := gardenCall(t, func(c net.Conn) {
		fixture.d.handleSeedNotes(c, &protocol.SeedNotesMessage{
			Cmd: protocol.CmdSeedNotes, SeedID: fixture.leaf.ID, SourceSessionID: protocol.Ptr("sess-b"),
		})
	})
	if !resp.Ok {
		t.Fatalf("notes: %v", protocol.Deref(resp.Error))
	}
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "second", true)
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")
}

func TestSeedNudges_NeverRingTheWriter(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	move(t, fixture.d, "sess-b", fixture.leaf.ID, garden.VerbTend, "", "")
	ringingNote(t, fixture.d, "sess-b", fixture.leaf.ID, "my own words", true)
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("writer rang itself: %q", queued)
	}
}

func TestSeedNudges_UnwatchStopsThePlot(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.crown.ID, false)
	result := watchSeed(t, fixture.d, "sess-b", fixture.crown.ID, true)
	if result.Watching || !result.Changed {
		t.Fatalf("unwatch result = %+v", result)
	}

	move(t, fixture.d, "sess-c", fixture.leaf.ID, garden.VerbTend, "", "")
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("unwatched session rang: %q", queued)
	}
}
