package daemon

import (
	"errors"
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
