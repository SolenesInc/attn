package daemon

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/protocol"
)

func newAgentMailboxDoorbellDaemon(t *testing.T, state protocol.SessionState) (*Daemon, *recordingDoorbell) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "agent-mailbox-doorbell.sock"))
	doorbell := &recordingDoorbell{}
	d.ptyBackend = doorbell.backend()
	addCharacterizationSession(t, d, "mailbox-target", protocol.SessionAgentCodex, state)
	addCharacterizationSession(t, d, "mailbox-sender", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	t.Cleanup(func() {
		d.stopAgentMailboxDoorbells()
		d.sessionInputs().stopRetries()
		_ = d.store.Close()
	})
	return d, doorbell
}

func enqueueMaintenanceDoorbellItem(t *testing.T, d *Daemon, id, body string, at time.Time) agentmailbox.Delivery {
	t.Helper()
	delivery, err := d.store.EnqueueMaintenancePrompt(id, "mailbox-target", body, at)
	if err != nil {
		t.Fatalf("enqueue maintenance item %s: %v", id, err)
	}
	return delivery
}

func readAgentMailboxBatch(t *testing.T, d *Daemon, recipient string, limit int) *protocol.AgentInboxBatchResult {
	t.Helper()
	resp := callHandler(t, func(conn net.Conn) {
		d.handleAgentInbox(conn, &protocol.AgentInboxMessage{
			Cmd: protocol.CmdAgentInbox, RecipientSessionID: recipient, Limit: protocol.Ptr(limit),
		})
	})
	if !resp.Ok || resp.AgentInboxBatchResult == nil {
		t.Fatalf("agent inbox batch = %+v", resp)
	}
	return resp.AgentInboxBatchResult
}

func mailboxLaneCounts(d *Daemon, sessionID string) (attempts, pending int) {
	m := d.sessionInputs()
	m.mu.Lock()
	lane := m.lanes[sessionID]
	m.mu.Unlock()
	if lane == nil {
		return 0, 0
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return len(lane.attempts), len(lane.pending)
}

func recordedDoorbellWrites(doorbell *recordingDoorbell) []string {
	doorbell.mu.Lock()
	defer doorbell.mu.Unlock()
	return append([]string(nil), doorbell.writes...)
}

func TestAgentMailboxDoorbellForgetsSuccessfulPlacementWithoutPromptSubmit(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateWaitingInput)

	first := enqueueMaintenanceDoorbellItem(t, d, "maintenance-first", "first durable body", time.Now())
	if err := d.deliverAgentMailboxItem(first); err != nil {
		t.Fatalf("place first doorbell: %v", err)
	}
	if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
		t.Fatalf("first doorbell = %q, want one generic prompt", got)
	}
	if attempts, pending := mailboxLaneCounts(d, "mailbox-target"); attempts != 0 || pending != 0 {
		t.Fatalf("successful doorbell retained lane state: attempts=%d pending=%d", attempts, pending)
	}

	batch := readAgentMailboxBatch(t, d, "mailbox-target", 1)
	if len(batch.Items) != 1 || batch.Items[0].ItemID != "maintenance-first" || batch.Items[0].Content != "first durable body" || batch.Remaining != 0 {
		t.Fatalf("first inbox read = %+v", batch)
	}

	second := enqueueMaintenanceDoorbellItem(t, d, "maintenance-second", "second durable body", time.Now().Add(time.Second))
	if err := d.deliverAgentMailboxItem(second); err != nil {
		t.Fatalf("place later doorbell without a prompt-submit receipt: %v", err)
	}
	if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText, agentMailboxDoorbellText}) {
		t.Fatalf("doorbells after inbox read = %q, want two generic prompts", got)
	}
}

func TestAgentMailboxReadDuringDeliveryRearmsForAConcurrentItem(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	d.agentMailboxCooldownOverride = time.Second
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce sync.Once
	d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		doorbell.mu.Lock()
		doorbell.writes = append(doorbell.writes, string(data))
		doorbell.mu.Unlock()
		if string(data) == "\r" {
			enterOnce.Do(func() {
				close(entered)
				<-release
			})
		}
	}}

	synctest.Test(t, func(t *testing.T) {
		first := enqueueMaintenanceDoorbellItem(t, d, "delivery-race-first", "first", time.Now())
		delivered := make(chan error, 1)
		go func() { delivered <- d.deliverAgentMailboxItem(first) }()
		<-entered

		items, remaining, err := d.store.ReadAgentMailbox("mailbox-target", 1, time.Now())
		if err != nil || len(items) != 1 || remaining != 0 {
			t.Fatalf("read first item: items=%v remaining=%d err=%v", items, remaining, err)
		}
		second := enqueueMaintenanceDoorbellItem(t, d, "delivery-race-second", "second", time.Now().Add(time.Millisecond))
		d.noteQueuedAgentMailboxItem("mailbox-target")
		if err := d.deliverAgentMailboxDoorbell("mailbox-target"); !errors.Is(err, errAgentMailboxDoorbellInFlight) {
			t.Fatalf("concurrent producer delivery = %v, want in-flight", err)
		}
		d.noteAgentMailboxRead("mailbox-target", remaining)
		close(release)
		if err := <-delivered; err != nil {
			t.Fatalf("finish first doorbell: %v", err)
		}
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
			t.Fatalf("doorbells before cooldown = %q", got)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText, agentMailboxDoorbellText}) {
			t.Fatalf("doorbells after cooldown = %q, want a wake for the concurrent item", got)
		}
		unread, err := d.store.UnreadAgentMailboxDeliveries(second.Item.RecipientSessionID)
		if err != nil || len(unread) != 1 || unread[0].Item.ID != second.Item.ID {
			t.Fatalf("unread after second doorbell = %+v err=%v", unread, err)
		}
	})
}

func TestAgentMailboxDoorbellCannotPasteAfterInputLanesStop(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	delivery := enqueueMaintenanceDoorbellItem(t, d, "after-stop", "durable after stop", time.Now())
	d.sessionInputs().stopRetries()

	err := d.deliverAgentMailboxItem(delivery)
	if !errors.Is(err, errSessionInputLaneClosed) {
		t.Fatalf("delivery after lane shutdown = %v, want closed lane", err)
	}
	if got := doorbell.pasted(); len(got) != 0 {
		t.Fatalf("delivery pasted after lane shutdown: %q", got)
	}
}

func TestAgentMailboxDoorbellReportsARecipientRemovedAfterEnqueue(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	delivery := enqueueMaintenanceDoorbellItem(t, d, "removed-recipient", "still durable", time.Now())
	d.store.Remove("mailbox-target")

	err := d.deliverAgentMailboxItem(delivery)
	if !errors.Is(err, errAgentMailboxRecipientGone) {
		t.Fatalf("delivery to removed recipient = %v, want gone", err)
	}
	if got := doorbell.pasted(); len(got) != 0 {
		t.Fatalf("delivery pasted for a removed recipient: %q", got)
	}
	unread, err := d.store.UnreadAgentMailboxDeliveries("mailbox-target")
	if err != nil || len(unread) != 1 || unread[0].Item.NotifiedAt != "" {
		t.Fatalf("removed recipient delivery = %+v err=%v, want durable and unnotified", unread, err)
	}
}

func TestAgentMailboxDoorbellQueuedWhileWorkingWakesOnIdle(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateWorking)
	d.agentMailboxCooldownOverride = time.Hour

	synctest.Test(t, func(t *testing.T) {
		delivery := enqueueMaintenanceDoorbellItem(t, d, "maintenance-busy", "wait until idle", time.Now())
		if err := d.deliverAgentMailboxItem(delivery); err == nil {
			t.Fatal("doorbell unexpectedly landed while the session was working")
		}
		if got := doorbell.pasted(); len(got) != 0 {
			t.Fatalf("working session received %q", got)
		}

		drained := make(chan int, 1)
		d.agentMailboxDrainHook = func(sessionID string, delivered int) {
			if sessionID == "mailbox-target" {
				drained <- delivered
			}
		}
		if !d.applyState(sessionStateChange{
			sessionID: "mailbox-target",
			state:     protocol.StateIdle,
			cause:     liveSignal{},
		}) {
			t.Fatal("idle transition was not applied")
		}
		synctest.Wait()
		select {
		case delivered := <-drained:
			if delivered != 1 {
				t.Fatalf("idle drain delivered %d doorbells, want 1", delivered)
			}
		default:
			t.Fatal("idle transition did not drain the unread inbox")
		}
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
			t.Fatalf("doorbells after idle = %q, want one generic prompt", got)
		}
	})
}

func TestAgentMailboxDoorbellPreservesRecentUserDraftUntilQuiet(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateWaitingInput)

	synctest.Test(t, func(t *testing.T) {
		const draft = "an unfinished user draft"
		if err := d.writeSessionPTY("mailbox-target", []byte(draft), "user"); err != nil {
			t.Fatalf("write user draft: %v", err)
		}
		delivery := enqueueMaintenanceDoorbellItem(t, d, "maintenance-after-draft", "durable background work", time.Now())
		err := d.deliverAgentMailboxItem(delivery)
		if !errors.Is(err, errSessionInputComposerDirty) {
			t.Fatalf("doorbell beside a recent draft = %v, want composer deferral", err)
		}
		if got := doorbell.pasted(); len(got) != 0 {
			t.Fatalf("doorbell touched the recent draft: %q", got)
		}
		if writes := recordedDoorbellWrites(doorbell); !reflect.DeepEqual(writes, []string{draft}) {
			t.Fatalf("writes before quiet window = %q, want the untouched draft", writes)
		}

		time.Sleep(sessionInputQuietWindow)
		synctest.Wait()
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
			t.Fatalf("doorbells after quiet window = %q", got)
		}
		writes := recordedDoorbellWrites(doorbell)
		if len(writes) != 3 || writes[0] != draft {
			t.Fatalf("writes after quiet window = %q, want draft then paste and Enter", writes)
		}
	})
}

func TestAgentMailboxDoorbellCoalescesMixedBurstAndBatchReadsEachBodyOnce(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	base := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)

	maintenance := enqueueMaintenanceDoorbellItem(t, d, "maintenance-burst", "maintenance body", base)
	if err := d.deliverAgentMailboxItem(maintenance); err != nil {
		t.Fatalf("place burst doorbell: %v", err)
	}
	expectedIDs := []string{"maintenance-burst"}
	expectedContent := map[string]bool{"maintenance body": true}
	for i := 1; i <= 9; i++ {
		seedID := fmt.Sprintf("s-burst-%02d", i)
		itemID := fmt.Sprintf("garden-burst-%02d", i)
		claimed, err := d.store.ClaimGardenSeedMailboxItem(
			"mailbox-target", seedID, "note", itemID, base.Add(time.Duration(i)*time.Second),
		)
		if err != nil || !claimed {
			t.Fatalf("claim Garden item %s: claimed=%v err=%v", itemID, claimed, err)
		}
		err = d.deliverAgentMailboxItem(agentmailbox.Delivery{Item: agentmailbox.Item{
			ID: itemID, RecipientSessionID: "mailbox-target", Kind: agentmailbox.KindGardenSeed,
		}})
		if !errors.Is(err, errAgentMailboxDoorbellOutstanding) {
			t.Fatalf("Garden item %s delivery = %v, want coalesced outstanding doorbell", itemID, err)
		}
		expectedIDs = append(expectedIDs, itemID)
		expectedContent[fmt.Sprintf("%s moved: note — read it with `attn seed show %s`.", seedID, seedID)] = true
	}
	peer, err := d.store.EnqueuePeerMessage(agentmailbox.PeerMessage{
		ID: "peer-burst", SenderSessionID: "mailbox-sender", Body: "peer body",
		CreatedAt: base.Add(10 * time.Second).Format(time.RFC3339Nano),
	}, "mailbox-target")
	if err != nil {
		t.Fatalf("enqueue peer item: %v", err)
	}
	if err := d.deliverAgentMailboxItem(peer); !errors.Is(err, errAgentMailboxDoorbellOutstanding) {
		t.Fatalf("peer delivery = %v, want coalesced outstanding doorbell", err)
	}
	expectedIDs = append(expectedIDs, "peer-burst")
	expectedContent["peer body"] = true

	if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
		t.Fatalf("mixed burst doorbells = %q, want one generic prompt", got)
	}
	for body := range expectedContent {
		if strings.Contains(doorbell.pasted()[0], body) {
			t.Fatalf("generic doorbell leaked durable content %q", body)
		}
	}

	batch := readAgentMailboxBatch(t, d, "mailbox-target", 50)
	if batch.Remaining != 0 || len(batch.Items) != len(expectedIDs) {
		t.Fatalf("mixed inbox batch = %+v, want %d items and no remainder", batch, len(expectedIDs))
	}
	seenContent := make(map[string]bool, len(batch.Items))
	for i, item := range batch.Items {
		if item.ItemID != expectedIDs[i] {
			t.Fatalf("batch item %d id = %q, want FIFO id %q", i, item.ItemID, expectedIDs[i])
		}
		if !expectedContent[item.Content] {
			t.Fatalf("batch item %q has unexpected content %q", item.ItemID, item.Content)
		}
		if item.NotifiedAt == "" || item.ReadAt == "" {
			t.Fatalf("batch item %q lacks per-item receipts: %+v", item.ItemID, item)
		}
		if seenContent[item.Content] {
			t.Fatalf("batch duplicated content %q", item.Content)
		}
		seenContent[item.Content] = true
	}
	if len(seenContent) != len(expectedContent) {
		t.Fatalf("batch content = %v, want every durable body once", seenContent)
	}
	again := readAgentMailboxBatch(t, d, "mailbox-target", 50)
	if len(again.Items) != 0 || again.Remaining != 0 {
		t.Fatalf("second inbox read duplicated content: %+v", again)
	}
}
