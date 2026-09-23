package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func closedBrokerSession(t *testing.T, d *Daemon, id string) reopenKey {
	t.Helper()
	addLedgerTestSession(t, d, id, t.TempDir())
	closedAt := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	if _, err := d.store.CloseSession(id, store.SessionClose{By: store.SessionClosedByUser}, closedAt); err != nil {
		t.Fatal(err)
	}
	entry := d.store.SessionLedgerEntry(id)
	return reopenKey{SessionID: id, ClosedAt: protocol.Deref(entry.ClosedAt)}
}

func brokerVerdict(key reopenKey) sessionReopenVerdict {
	return sessionReopenVerdict{
		SessionID:      key.SessionID,
		Reopenable:     true,
		Actions:        []protocol.SessionReopenAction{protocol.SessionReopenActionReopen},
		DirectoryState: directoryPresent,
		WorkspaceID:    "ws-1",
		WorkspacePlan:  reopenPlaceReuse,
		PanePlan:       reopenPlaceAdd,
	}
}

func installTestReopenBroker(t *testing.T, d *Daemon, workers int) *sessionReopenBroker {
	t.Helper()
	broker := newSessionReopenBroker(d, workers)
	d.reopenBrokerMu.Lock()
	d.reopenBrokerInstance = broker
	d.reopenBrokerMu.Unlock()
	t.Cleanup(broker.Stop)
	return broker
}

func nextReopenResolution(t *testing.T, client *wsClient) protocol.SessionReopenResolvedMessage {
	t.Helper()
	message := <-client.send
	var resolved protocol.SessionReopenResolvedMessage
	if err := json.Unmarshal(message.payload, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Event != protocol.EventSessionReopenResolved {
		t.Fatalf("event=%q, want %q", resolved.Event, protocol.EventSessionReopenResolved)
	}
	return resolved
}

func TestStreamedSessionListQueuesThePageBeforeBlockedResolution(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	key := closedBrokerSession(t, d, "blocked")
	broker := installTestReopenBroker(t, d, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	broker.resolve = func(ctx context.Context, got reopenKey) (sessionReopenVerdict, error) {
		if got != key {
			t.Fatalf("key=%+v, want %+v", got, key)
		}
		close(started)
		select {
		case <-release:
			return brokerVerdict(got), nil
		case <-ctx.Done():
			return sessionReopenVerdict{}, ctx.Err()
		}
	}
	client := newWorkspaceProtocolTestClient()
	msg := &protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("page"), Closed: protocol.Ptr(true),
		Reopen: protocol.Ptr(true),
	}
	intent := broker.BeginPage(client, false)
	d.sendSessionListWSResult(client, msg, &intent)

	page := onlySessionListResult(t, client)
	if page.Result == nil || len(page.Result.Entries) != 1 || page.Result.Reopen != nil {
		t.Fatalf("page=%+v, want the stored row without inline verdicts", page.Result)
	}
	<-started
	select {
	case message := <-client.send:
		t.Fatalf("resolution arrived while its barrier was closed: %s", message.payload)
	default:
	}
	close(release)
	resolved := nextReopenResolution(t, client)
	if !resolved.Success || resolved.SessionID != key.SessionID || resolved.ClosedAt != key.ClosedAt {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestReopenBrokerRowsSettleIndependentlyAndOutOfOrder(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	broker := installTestReopenBroker(t, d, 2)
	client := newWorkspaceProtocolTestClient()
	keys := []reopenKey{{SessionID: "one", ClosedAt: "close-one"}, {SessionID: "two", ClosedAt: "close-two"}}
	started := make(chan reopenKey, 2)
	releases := map[reopenKey]chan struct{}{keys[0]: make(chan struct{}), keys[1]: make(chan struct{})}
	broker.resolve = func(ctx context.Context, key reopenKey) (sessionReopenVerdict, error) {
		started <- key
		select {
		case <-releases[key]:
			return brokerVerdict(key), nil
		case <-ctx.Done():
			return sessionReopenVerdict{}, ctx.Err()
		}
	}
	intent := broker.BeginPage(client, false)
	broker.CommitPage(intent, keys)
	seen := map[reopenKey]bool{<-started: true, <-started: true}
	if !seen[keys[0]] || !seen[keys[1]] {
		t.Fatalf("started=%v", seen)
	}
	close(releases[keys[1]])
	if got := nextReopenResolution(t, client).SessionID; got != "two" {
		t.Fatalf("first settled=%q, want two", got)
	}
	close(releases[keys[0]])
	if got := nextReopenResolution(t, client).SessionID; got != "one" {
		t.Fatalf("second settled=%q, want one", got)
	}
}

func TestReopenBrokerPageEpochsReplaceAndAppendInterest(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	broker := installTestReopenBroker(t, d, 1)
	broker.resolve = func(ctx context.Context, _ reopenKey) (sessionReopenVerdict, error) {
		<-ctx.Done()
		return sessionReopenVerdict{}, ctx.Err()
	}
	client := newWorkspaceProtocolTestClient()
	oldKey := reopenKey{SessionID: "old", ClosedAt: "old-close"}
	newKey := reopenKey{SessionID: "new", ClosedAt: "new-close"}
	appendKey := reopenKey{SessionID: "append", ClosedAt: "append-close"}
	first := broker.BeginPage(client, false)
	newer := broker.BeginPage(client, false)
	broker.CommitPage(first, []reopenKey{oldKey})
	broker.CommitPage(newer, []reopenKey{newKey})
	appendIntent := broker.BeginPage(client, true)
	broker.CommitPage(appendIntent, []reopenKey{appendKey})

	broker.mu.Lock()
	defer broker.mu.Unlock()
	state := broker.clients[client]
	if state == nil || len(state.keys) != 2 {
		t.Fatalf("interest=%+v, want replacement plus appended page", state)
	}
	if _, ok := state.keys[oldKey]; ok {
		t.Fatal("superseded page replaced newer interest")
	}
	if _, ok := state.keys[newKey]; !ok {
		t.Fatal("new initial page interest missing")
	}
	if _, ok := state.keys[appendKey]; !ok {
		t.Fatal("load-more interest missing")
	}
}

func TestReopenBrokerCoalescesAndCancelsAfterTheFinalInterest(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	broker := installTestReopenBroker(t, d, 1)
	one := newWorkspaceProtocolTestClient()
	two := newWorkspaceProtocolTestClient()
	key := reopenKey{SessionID: "shared", ClosedAt: "shared-close"}
	started := make(chan struct{})
	canceled := make(chan struct{})
	var calls atomic.Int32
	broker.resolve = func(ctx context.Context, _ reopenKey) (sessionReopenVerdict, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-ctx.Done()
		close(canceled)
		return sessionReopenVerdict{}, ctx.Err()
	}
	broker.CommitPage(broker.BeginPage(one, false), []reopenKey{key})
	broker.CommitPage(broker.BeginPage(two, false), []reopenKey{key})
	<-started
	broker.RemoveClient(one)
	select {
	case <-canceled:
		t.Fatal("one remaining client lost its shared job")
	default:
	}
	broker.RemoveClient(two)
	<-canceled
	if calls.Load() != 1 {
		t.Fatalf("resolver calls=%d, want one shared generation", calls.Load())
	}
}

func TestReopenBrokerFailuresAndStaleGenerationsSettle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	synctest.Test(t, func(t *testing.T) {
		broker := installTestReopenBroker(t, d, 2)
		client := newWorkspaceProtocolTestClient()
		failed := reopenKey{SessionID: "failed", ClosedAt: "failed-close"}
		stale := reopenKey{SessionID: "stale", ClosedAt: "stale-close"}
		broker.resolve = func(_ context.Context, key reopenKey) (sessionReopenVerdict, error) {
			if key == stale {
				return sessionReopenVerdict{}, staleReopenGenerationError(key)
			}
			return sessionReopenVerdict{}, errors.New("git unavailable")
		}
		broker.CommitPage(broker.BeginPage(client, false), []reopenKey{failed, stale})
		synctest.Wait()
		settled := map[string]protocol.SessionReopenResolvedMessage{}
		for len(client.send) > 0 {
			resolved := nextReopenResolution(t, client)
			settled[resolved.SessionID] = resolved
		}
		if got := settled["failed"]; got.Success || protocol.Deref(got.Error) != "git unavailable" {
			t.Fatalf("failed=%+v, want terminal failure", got)
		}
		if got, ok := settled["stale"]; !ok || got.Success || got.ClosedAt != stale.ClosedAt || !strings.Contains(protocol.Deref(got.Error), "reload") {
			t.Fatalf("stale=%+v, want a terminal failure for the listed generation", got)
		}
	})
}

func TestASessionListBufferedAtDisconnectLeavesNoReopenInterest(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	closedBrokerSession(t, d, "late")
	broker := installTestReopenBroker(t, d, 1)
	broker.resolve = func(ctx context.Context, _ reopenKey) (sessionReopenVerdict, error) {
		<-ctx.Done()
		return sessionReopenVerdict{}, ctx.Err()
	}
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})
	list, err := json.Marshal(protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("buffered"),
		Closed: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	client.recv = make(chan []byte, 1)
	client.recv <- list
	close(client.recv)

	d.wsMsgPump(client)

	select {
	case <-client.send:
	case <-time.After(5 * time.Second):
		t.Fatal("the buffered session_list was never answered")
	}
	broker.mu.Lock()
	_, registered := broker.clients[client]
	jobs := len(broker.jobs)
	broker.mu.Unlock()
	if registered || jobs != 0 {
		t.Fatalf("disconnected client registered=%v with %d jobs; a list buffered at disconnect leaks it", registered, jobs)
	}
}

func sendLedgerCommand(t *testing.T, d *Daemon, client *wsClient, command any) {
	t.Helper()
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	d.handleClientMessage(client, raw)
}

func openLedgerPage(t *testing.T, d *Daemon, client *wsClient) {
	t.Helper()
	sendLedgerCommand(t, d, client, protocol.SessionListMessage{
		Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("page"), All: protocol.Ptr(true), Reopen: protocol.Ptr(true),
	})
	var page protocol.SessionListResultMessage
	if err := json.Unmarshal((<-client.send).payload, &page); err != nil || page.Event != protocol.EventSessionListResult || !page.Success {
		t.Fatalf("ledger page = %+v (err %v), want a successful session_list_result", page, err)
	}
}

func ledgerClient(d *Daemon) *wsClient {
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("app", "test", []string{protocol.CapabilityWorkspaceSessions})
	d.wsHub.add(client)
	return client
}

func TestSessionCloseProjectsTheRowBeforeStartingResolution(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "closing", t.TempDir())
	broker := installTestReopenBroker(t, d, 1)
	openLedgerPage(t, d, ledgerClient(d))
	var projected atomic.Bool
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event.Event == protocol.EventSessionClosed {
			projected.Store(true)
		}
	}
	resolved := make(chan struct{})
	broker.resolve = func(_ context.Context, key reopenKey) (sessionReopenVerdict, error) {
		if !projected.Load() {
			t.Error("reopen resolution started before session_closed was projected")
		}
		close(resolved)
		return brokerVerdict(key), nil
	}
	d.closeSession("closing", store.SessionClose{By: store.SessionClosedByUser})
	<-resolved
}

func TestSessionCloseStartsNoResolutionWhileNoLedgerIsOpen(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "unwatched", t.TempDir())
	ledgerClient(d)

	d.closeSession("unwatched", store.SessionClose{By: store.SessionClosedByUser})

	if broker := d.existingSessionReopenBroker(); broker != nil {
		t.Fatal("a close with no open ledger initialized the reopen broker")
	}
}

func TestSessionCloseStartsNoResolutionAfterTheLedgerDropsReopenInterest(t *testing.T) {
	for name, dropInterest := range map[string]func(*testing.T, *Daemon, *wsClient){
		"unsubscribe": func(t *testing.T, d *Daemon, client *wsClient) {
			sendLedgerCommand(t, d, client, protocol.SessionReopenUnsubscribeMessage{Cmd: protocol.CmdSessionReopenUnsubscribe})
		},
		"page without reopen": func(t *testing.T, d *Daemon, client *wsClient) {
			sendLedgerCommand(t, d, client, protocol.SessionListMessage{Cmd: protocol.CmdSessionList, RequestID: protocol.Ptr("live")})
			<-client.send
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
			addLedgerTestSession(t, d, "after-interest", t.TempDir())
			broker := installTestReopenBroker(t, d, 1)
			client := ledgerClient(d)
			openLedgerPage(t, d, client)

			dropInterest(t, d, client)
			d.closeSession("after-interest", store.SessionClose{By: store.SessionClosedByUser})

			broker.mu.Lock()
			jobs := len(broker.jobs)
			broker.mu.Unlock()
			if jobs != 0 {
				t.Fatalf("close after the ledger dropped reopen interest queued %d reopen jobs, want none", jobs)
			}
		})
	}
}

func TestReopenBrokerWorkerReceiptForFiftyRows(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	broker := installTestReopenBroker(t, d, productionSessionReopenWorkers)
	client := newWorkspaceProtocolTestClient()
	keys := make([]reopenKey, 50)
	release := make(chan struct{})
	started := make(chan struct{}, len(keys))
	var active atomic.Int32
	var peak atomic.Int32
	broker.resolve = func(ctx context.Context, key reopenKey) (sessionReopenVerdict, error) {
		current := active.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			active.Add(-1)
			return brokerVerdict(key), nil
		case <-ctx.Done():
			active.Add(-1)
			return sessionReopenVerdict{}, ctx.Err()
		}
	}
	for i := range keys {
		keys[i] = reopenKey{SessionID: fmt.Sprintf("session-%02d", i), ClosedAt: "closed"}
	}
	broker.CommitPage(broker.BeginPage(client, false), keys)
	for range productionSessionReopenWorkers {
		<-started
	}
	if got := peak.Load(); got != productionSessionReopenWorkers {
		t.Fatalf("peak workers=%d, want %d for the 50-row receipt", got, productionSessionReopenWorkers)
	}
	broker.mu.Lock()
	queued := len(broker.queue)
	broker.mu.Unlock()
	if queued != len(keys)-productionSessionReopenWorkers {
		t.Fatalf("queued=%d, want %d", queued, len(keys)-productionSessionReopenWorkers)
	}
	close(release)
}
