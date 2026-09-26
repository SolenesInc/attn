package daemon

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
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

func TestAgentMailboxDoorbellRemindsUntilTheInboxIsRead(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	d.agentMailboxCooldownOverride = time.Second

	synctest.Test(t, func(t *testing.T) {
		first := enqueueMaintenanceDoorbellItem(t, d, "reminder-first", "first", time.Now())
		if err := d.deliverAgentMailboxItem(first); err != nil {
			t.Fatalf("first doorbell: %v", err)
		}
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
			t.Fatalf("initial doorbells = %q", got)
		}

		time.Sleep(time.Second - time.Nanosecond)
		synctest.Wait()
		if got := doorbell.pasted(); len(got) != 1 {
			t.Fatalf("doorbells before cooldown = %q, want one", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText, agentMailboxDoorbellText}) {
			t.Fatalf("doorbells after cooldown = %q, want one reminder", got)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		if got := doorbell.pasted(); len(got) != 3 {
			t.Fatalf("doorbells after a second cooldown = %q, want another reminder", got)
		}
		if attempts, pending := mailboxLaneCounts(d, "mailbox-target"); attempts != 0 || pending != 0 {
			t.Fatalf("reminders retained lane state: attempts=%d pending=%d", attempts, pending)
		}

		batch := readAgentMailboxBatch(t, d, "mailbox-target", 50)
		if len(batch.Items) != 1 || batch.Items[0].ItemID != "reminder-first" || batch.Remaining != 0 {
			t.Fatalf("inbox read = %+v", batch)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := doorbell.pasted(); len(got) != 3 {
			t.Fatalf("doorbell continued after the inbox was read: %q", got)
		}
	})
}

func TestAgentMailboxDoorbellCoalescesNewArrivalsAfterReminder(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	d.agentMailboxCooldownOverride = time.Second

	synctest.Test(t, func(t *testing.T) {
		first := enqueueMaintenanceDoorbellItem(t, d, "arrival-first", "first", time.Now())
		if err := d.deliverAgentMailboxItem(first); err != nil {
			t.Fatalf("first doorbell: %v", err)
		}
		initial, err := d.store.UnreadAgentMailboxDeliveries("mailbox-target")
		if err != nil || len(initial) != 1 || initial[0].Item.NotifiedAt == "" {
			t.Fatalf("initial unread delivery = %+v err=%v", initial, err)
		}
		firstNotifiedAt := initial[0].Item.NotifiedAt
		time.Sleep(time.Second)
		synctest.Wait()
		if got := doorbell.pasted(); len(got) != 2 {
			t.Fatalf("doorbells after first expiry = %q, want one reminder", got)
		}

		for i := 2; i <= 4; i++ {
			id := fmt.Sprintf("arrival-%d", i)
			delivery := enqueueMaintenanceDoorbellItem(t, d, id, id, time.Now().Add(time.Duration(i)*time.Nanosecond))
			if err := d.deliverAgentMailboxItem(delivery); !errors.Is(err, errAgentMailboxDoorbellOutstanding) {
				t.Fatalf("delivery %s = %v, want coalesced outstanding doorbell", id, err)
			}
		}
		if got := doorbell.pasted(); len(got) != 2 {
			t.Fatalf("new-arrival burst bypassed cooldown: %q", got)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		if got := doorbell.pasted(); len(got) != 3 {
			t.Fatalf("doorbells after new-arrival cooldown = %q, want one coalesced reminder", got)
		}
		unread, err := d.store.UnreadAgentMailboxDeliveries("mailbox-target")
		if err != nil || len(unread) != 4 {
			t.Fatalf("unread deliveries after reminder = %+v err=%v", unread, err)
		}
		if unread[0].Item.NotifiedAt != firstNotifiedAt {
			t.Fatalf("first notified receipt changed from %q to %q", firstNotifiedAt, unread[0].Item.NotifiedAt)
		}
		for _, delivery := range unread {
			if delivery.Item.NotifiedAt == "" {
				t.Fatalf("reminder left %q without a notified receipt", delivery.Item.ID)
			}
		}
		batch := readAgentMailboxBatch(t, d, "mailbox-target", 50)
		if batch.Remaining != 0 || len(batch.Items) != 4 {
			t.Fatalf("new-arrival inbox = %+v", batch)
		}
	})
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
		defer d.stopAgentMailboxDoorbells()
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

func TestAgentMailboxDoorbellCoalescesArrivalDuringPartialReadCooldown(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	d.agentMailboxCooldownOverride = time.Second

	synctest.Test(t, func(t *testing.T) {
		first := enqueueMaintenanceDoorbellItem(t, d, "partial-first", "first", time.Now())
		if err := d.deliverAgentMailboxItem(first); err != nil {
			t.Fatalf("first doorbell: %v", err)
		}
		second := enqueueMaintenanceDoorbellItem(t, d, "partial-second", "second", time.Now().Add(time.Nanosecond))
		if err := d.deliverAgentMailboxItem(second); !errors.Is(err, errAgentMailboxDoorbellOutstanding) {
			t.Fatalf("second delivery = %v, want coalesced outstanding doorbell", err)
		}

		batch := readAgentMailboxBatch(t, d, "mailbox-target", 1)
		if len(batch.Items) != 1 || batch.Items[0].ItemID != "partial-first" || batch.Remaining != 1 {
			t.Fatalf("partial read = %+v", batch)
		}
		third := enqueueMaintenanceDoorbellItem(t, d, "partial-third", "third", time.Now().Add(2*time.Nanosecond))
		if err := d.deliverAgentMailboxItem(third); !errors.Is(err, errAgentMailboxDoorbellOutstanding) {
			t.Fatalf("delivery during partial-read cooldown = %v, want coalesced outstanding doorbell", err)
		}
		if got := doorbell.pasted(); len(got) != 1 {
			t.Fatalf("arrival bypassed the partial-read cooldown: %q", got)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		if got := doorbell.pasted(); len(got) != 2 {
			t.Fatalf("doorbells after partial-read cooldown = %q, want one reminder", got)
		}
		remaining := readAgentMailboxBatch(t, d, "mailbox-target", 50)
		if remaining.Remaining != 0 || len(remaining.Items) != 2 ||
			remaining.Items[0].ItemID != "partial-second" || remaining.Items[1].ItemID != "partial-third" {
			t.Fatalf("remaining inbox = %+v", remaining)
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

func TestAgentMailboxDoorbellPreservesRecentUserDraftUntilQuiet(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateWaitingInput)

	synctest.Test(t, func(t *testing.T) {
		defer d.stopAgentMailboxDoorbells()
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

func TestAgentMailboxReminderPreservesAUserDraftUntilQuiet(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateWaitingInput)
	d.agentMailboxCooldownOverride = time.Second

	synctest.Test(t, func(t *testing.T) {
		defer d.stopAgentMailboxDoorbells()
		delivery := enqueueMaintenanceDoorbellItem(t, d, "reminder-after-draft", "durable background work", time.Now())
		if err := d.deliverAgentMailboxItem(delivery); err != nil {
			t.Fatalf("first doorbell: %v", err)
		}
		time.Sleep(time.Second / 2)
		const draft = "an unfinished draft between doorbells"
		if err := d.writeSessionPTY("mailbox-target", []byte(draft), "user"); err != nil {
			t.Fatalf("write user draft: %v", err)
		}

		time.Sleep(time.Second / 2)
		synctest.Wait()
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
			t.Fatalf("reminder touched the recent draft: %q", got)
		}
		writes := recordedDoorbellWrites(doorbell)
		if len(writes) != 3 || writes[2] != draft {
			t.Fatalf("writes at reminder cooldown = %q, want first doorbell then untouched draft", writes)
		}

		time.Sleep(sessionInputQuietWindow - time.Second/2)
		synctest.Wait()
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText, agentMailboxDoorbellText}) {
			t.Fatalf("doorbells after draft quieted = %q, want the deferred reminder", got)
		}
	})
}

func TestAgentMailboxDoorbellSessionCleanupStopsReminder(t *testing.T) {
	d, doorbell := newAgentMailboxDoorbellDaemon(t, protocol.SessionStateIdle)
	d.agentMailboxCooldownOverride = time.Second

	synctest.Test(t, func(t *testing.T) {
		delivery := enqueueMaintenanceDoorbellItem(t, d, "cleanup", "unread after cleanup", time.Now())
		if err := d.deliverAgentMailboxItem(delivery); err != nil {
			t.Fatalf("first doorbell: %v", err)
		}
		d.forgetAgentMailboxDoorbell("mailbox-target")
		time.Sleep(time.Second)
		synctest.Wait()
		if got := doorbell.pasted(); !reflect.DeepEqual(got, []string{agentMailboxDoorbellText}) {
			t.Fatalf("doorbell fired after session cleanup: %q", got)
		}
	})
}
