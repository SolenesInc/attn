package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func sendClientHelloAs(t *testing.T, conn *websocket.Conn, clientID string) {
	t.Helper()
	if err := writeWS(conn, map[string]interface{}{
		"cmd":          protocol.CmdClientHello,
		"client_kind":  "daemon-test",
		"client_id":    clientID,
		"version":      "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{protocol.CapabilityWorkspaceSessions},
		"client_token": config.ClientToken(),
	}); err != nil {
		t.Fatalf("send client hello: %v", err)
	}
}

// Receipt: an aborted connection on loopback delivers what the client's receive buffer
// holds and then fails; measured on macOS 400,368 bytes (1 MB written, SO_LINGER 0).
const (
	stalledClientMessageBytes = 1 << 20
	maxBacklogDeliveredBytes  = 32 << 20
)

func TestEvictedClientIsNotFedItsBacklogFirst(t *testing.T) {
	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	d := NewForTesting(sockPath)
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	evicted := make(chan struct{}, 1)
	d.wsHub.logf = func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Log("hub: " + line)
		if strings.Contains(line, "too slow") {
			select {
			case evicted <- struct{}{}:
			default:
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stalled := dialDaemonWSAs(t, ctx, "127.0.0.1:"+wsPort, "stalled-client")
	defer stalled.Close(websocket.StatusNormalClosure, "")

	stopFlood := floodBroadcasts(d, stalledClientMessageBytes, time.Millisecond)
	select {
	case <-evicted:
	case <-ctx.Done():
		stopFlood()
		t.Fatal("the hub never evicted the stalled client")
	}
	stopFlood()

	readCtx, cancelRead := context.WithTimeout(ctx, evictionDeathBudget)
	defer cancelRead()
	start := time.Now()
	delivered := 0
	for {
		_, data, err := stalled.Read(readCtx)
		if err != nil {
			t.Logf("stalled client's read ended after %s having read %d bytes: %v",
				time.Since(start).Round(time.Millisecond), delivered, err)
			break
		}
		delivered += len(data)
		if delivered > maxBacklogDeliveredBytes {
			t.Fatalf(
				"evicted client was fed %d bytes of backlog (cap %d); the daemon is draining its queue into a connection it already gave up on",
				delivered, maxBacklogDeliveredBytes,
			)
		}
	}
	if readCtx.Err() != nil {
		t.Fatalf("the evicted client was still connected %s after the eviction", evictionDeathBudget)
	}
}

func TestEvictedClientLearnsWhyOnItsNextConnection(t *testing.T) {
	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	d := NewForTesting(filepath.Join(tmpDir, "test.sock"))
	go d.Start()
	defer d.Stop()
	waitForSocket(t, filepath.Join(tmpDir, "test.sock"), 5*time.Second)
	waitForRecovery(t, d)

	addr := "127.0.0.1:" + wsPort
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const clientID = "app-instance-1"
	first := dialDaemonWSAs(t, ctx, addr, clientID)
	defer first.Close(websocket.StatusNormalClosure, "")

	waitForCond(t, 5*time.Second, "the daemon to record the client id", func() bool {
		found := false
		d.wsHub.ForEachClient(func(c *wsClient) {
			if c.ClientID() == clientID {
				found = true
			}
		})
		return found
	})

	d.wsHub.mu.Lock()
	for client := range d.wsHub.clients {
		if client.ClientID() != clientID {
			continue
		}
		client.slowCount = maxSlowCount
		delete(d.wsHub.clients, client)
		d.wsHub.evict(client, slowClientCloseReason)
	}
	d.wsHub.mu.Unlock()

	deathCtx, cancelDeath := context.WithTimeout(ctx, evictionDeathBudget)
	defer cancelDeath()
	if err := readUntilClosed(deathCtx, first); err == nil {
		t.Fatal("evicted client's read succeeded; want the connection to end")
	} else if deathCtx.Err() != nil {
		t.Fatalf("evicted client still connected after %s", evictionDeathBudget)
	}

	second := dialDaemonWSAs(t, ctx, addr, clientID)
	defer second.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, second, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
	if got, ok := notice["undelivered_messages"].(float64); !ok || got < float64(maxSlowCount) {
		t.Errorf("eviction notice undelivered_messages = %v, want at least %d", notice["undelivered_messages"], maxSlowCount)
	}

	if _, ok := d.wsHub.takeEviction(clientID); ok {
		t.Error("the eviction is still on file after being delivered")
	}
}

func TestAnEvictionFiledMidHelloIsNotLostWithTheConnection(t *testing.T) {
	d := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	defer d.Stop()

	const clientID = "app-instance-1"
	client := &wsClient{
		send:             make(chan outboundMessage, 8),
		bearerAuthorized: true,
	}

	client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, slowClientCloseReason)
	d.wsHub.rememberEviction(clientID, evictionRecord{
		at: time.Now(), reason: slowClientCloseReason, undelivered: maxSlowCount,
	})

	d.handleClientHello(client, &protocol.ClientHelloMessage{
		Cmd: protocol.CmdClientHello, ClientKind: "daemon-test", ClientID: protocol.Ptr(clientID),
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
	})

	record, ok := d.wsHub.takeEviction(clientID)
	if !ok {
		t.Fatal("an eviction filed mid-hello was lost: the next connection will never be told why")
	}
	if record.reason != slowClientCloseReason {
		t.Errorf("re-filed reason = %q, want %q", record.reason, slowClientCloseReason)
	}
	if record.undelivered < maxSlowCount {
		t.Errorf("re-filed undelivered = %d, want at least %d", record.undelivered, maxSlowCount)
	}
}

func TestWriteStallEndsTheConnectionAndIsRemembered(t *testing.T) {
	const clientID = "stalled-app"

	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	d := NewForTesting(sockPath)
	d.wsWriteTimeout = 300 * time.Millisecond
	evictionRecorded := make(chan evictionRecord, 1)
	d.wsHub.evictionListener = func(id string, record evictionRecord) {
		if id == clientID {
			evictionRecorded <- record
		}
	}
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	var logMu sync.Mutex
	var hubLog []string
	d.wsHub.logf = func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Log("hub: " + line)
		logMu.Lock()
		hubLog = append(hubLog, line)
		logMu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	addr := "127.0.0.1:" + wsPort
	stalled := dialDaemonWSAs(t, ctx, addr, clientID)
	defer stalled.Close(websocket.StatusNormalClosure, "")
	waitForCond(t, 5*time.Second, "the daemon to record the client id", func() bool {
		found := false
		d.wsHub.ForEachClient(func(c *wsClient) {
			if c.ClientID() == clientID {
				found = true
			}
		})
		return found
	})

	d.wsHub.BroadcastRawText([]byte(strings.Repeat("x", 8<<20)))
	recordCtx, cancelRecord := context.WithTimeout(ctx, evictionDeathBudget)
	defer cancelRecord()
	var recorded evictionRecord
	select {
	case recorded = <-evictionRecorded:
	case <-recordCtx.Done():
		t.Fatalf("the write stall was not recorded within %s", evictionDeathBudget)
	}
	if recorded.reason != slowClientCloseReason {
		t.Errorf("recorded eviction reason = %q, want %q", recorded.reason, slowClientCloseReason)
	}
	if recorded.undelivered < 1 {
		t.Errorf("recorded eviction undelivered = %d, want at least 1", recorded.undelivered)
	}

	deathCtx, cancelDeath := context.WithTimeout(ctx, evictionDeathBudget)
	defer cancelDeath()
	if err := readUntilClosed(deathCtx, stalled); err == nil {
		t.Fatal("stalled client's read succeeded; want the connection to end")
	} else if deathCtx.Err() != nil {
		t.Fatalf("stalled client still connected %s after the write deadline", evictionDeathBudget)
	}

	logMu.Lock()
	for _, line := range hubLog {
		if strings.Contains(line, "too slow") || strings.Contains(line, "client slow") {
			t.Errorf("the hub's slow-count fired (%q); this test no longer covers the write-stall path", line)
		}
	}
	logMu.Unlock()

	second := dialDaemonWSAs(t, ctx, addr, clientID)
	defer second.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, second, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
	if got, ok := notice["undelivered_messages"].(float64); !ok || got < 1 {
		t.Errorf("eviction notice undelivered_messages = %v, want at least 1", notice["undelivered_messages"])
	}
}

func TestUnansweredPingIsAnEvictionOnlyWhenTheDaemonOwesTheClient(t *testing.T) {
	wsPort := useFreeWSPort(t)
	tmpDir := shortTempDir(t)
	sockPath := filepath.Join(tmpDir, "test.sock")
	d := NewForTesting(sockPath)
	d.wsPingInterval, d.wsPingTimeout = 200*time.Millisecond, 200*time.Millisecond
	go d.Start()
	defer d.Stop()
	waitForSocket(t, sockPath, 5*time.Second)
	waitForRecovery(t, d)

	addr := "127.0.0.1:" + wsPort
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A peer that stopped answering pings will not answer a close handshake either;
	// measured, the same client held on for 5.4s when the daemon waited one out.
	const pingDeathBudget = 2 * time.Second
	const quietID = "went-away"
	quiet := dialDaemonWSAs(t, ctx, addr, quietID)
	defer quiet.Close(websocket.StatusNormalClosure, "")
	waitForCond(t, pingDeathBudget, "the daemon to drop the silent client", func() bool {
		return d.wsHub.ClientCount() == 0
	})
	if record, ok := d.wsHub.takeEviction(quietID); ok {
		t.Errorf("a connection that died owing nothing was filed as an eviction: %+v", record)
	}

	const stalledID = "fell-behind"
	stalled := dialDaemonWSAs(t, ctx, addr, stalledID)
	defer stalled.Close(websocket.StatusNormalClosure, "")
	waitForCond(t, 5*time.Second, "the daemon to record the client id", func() bool {
		found := false
		d.wsHub.ForEachClient(func(c *wsClient) {
			if c.ClientID() == stalledID {
				found = true
			}
		})
		return found
	})
	d.wsHub.BroadcastRawText([]byte(strings.Repeat("x", 8<<20)))
	waitForCond(t, pingDeathBudget, "the daemon to drop the stalled client", func() bool {
		return d.wsHub.ClientCount() == 0
	})

	d.wsPingInterval, d.wsPingTimeout = 0, 0
	second := dialDaemonWSAs(t, ctx, addr, stalledID)
	defer second.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, second, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
}

func TestEvictionMemoryForgetsWhatItShould(t *testing.T) {
	h := newWSHub()
	logged := 0
	h.logf = func(string, ...interface{}) { logged++ }

	now := time.Now()

	t.Run("a record older than the TTL is gone", func(t *testing.T) {
		h.rememberEviction("stale", evictionRecord{at: now.Add(-evictionMemoryTTL - time.Minute)})
		if _, ok := h.takeEviction("stale"); ok {
			t.Error("an eviction older than the TTL was still delivered")
		}
	})

	t.Run("a client that never named itself files nothing", func(t *testing.T) {
		h.rememberEviction("", evictionRecord{at: now})
		if _, ok := h.takeEviction(""); ok {
			t.Error("an empty client id matched a record")
		}
	})

	t.Run("the map is bounded, and says so when it drops one", func(t *testing.T) {
		before := logged
		for i := 0; i < maxRememberedEvictions*2; i++ {
			h.rememberEviction(
				"client-"+time.Duration(i).String(),
				evictionRecord{at: now.Add(time.Duration(i) * time.Second), reason: slowClientCloseReason},
			)
		}
		h.evictionMu.Lock()
		held := len(h.evictions)
		h.evictionMu.Unlock()
		if held > maxRememberedEvictions {
			t.Errorf("eviction memory holds %d records, over the %d cap", held, maxRememberedEvictions)
		}
		if logged == before {
			t.Error("records were dropped silently; a notice nobody will receive must be logged")
		}
	})
}
