package daemon

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

// The close frame travels the same TCP stream as the backlog, so a wedged client never
// reads it (measured: on a 10 KB/s link the client read nothing for ~45s after hang-up).
const (
	// evictionCloseGrace bounds the close-frame attempt before the transport is
	// aborted. Tripwire: a writable socket takes microseconds.
	evictionCloseGrace = 1 * time.Second
	// evictionMemoryTTL: reconnect backoff caps at 5s and the circuit breaker
	// resets at 30s, so this is two orders of magnitude past a same-visit return.
	evictionMemoryTTL      = 10 * time.Minute
	maxRememberedEvictions = 16
)

type evictionRecord struct {
	at          time.Time
	reason      string
	undelivered int
}

func (h *wsHub) rememberEviction(clientID string, record evictionRecord) {
	if clientID == "" {
		return
	}
	h.evictionMu.Lock()
	defer h.evictionMu.Unlock()
	if h.evictions == nil {
		h.evictions = make(map[string]evictionRecord)
	}
	h.pruneEvictionsLocked(record.at)
	if len(h.evictions) >= maxRememberedEvictions {
		oldestID, oldest := "", time.Time{}
		for id, rec := range h.evictions {
			if oldest.IsZero() || rec.at.Before(oldest) {
				oldestID, oldest = id, rec.at
			}
		}
		h.logf(
			"eviction memory full (%d records); dropping the notice for client %s so client %s can be remembered",
			maxRememberedEvictions, oldestID, clientID,
		)
		delete(h.evictions, oldestID)
	}
	h.evictions[clientID] = record
}

func (h *wsHub) takeEviction(clientID string) (evictionRecord, bool) {
	if clientID == "" {
		return evictionRecord{}, false
	}
	h.evictionMu.Lock()
	defer h.evictionMu.Unlock()
	h.pruneEvictionsLocked(time.Now())
	record, ok := h.evictions[clientID]
	if ok {
		delete(h.evictions, clientID)
	}
	return record, ok
}

func (h *wsHub) pruneEvictionsLocked(now time.Time) {
	for id, rec := range h.evictions {
		if now.Sub(rec.at) > evictionMemoryTTL {
			delete(h.evictions, id)
		}
	}
}

// Callers hold h.mu, so the hang-up runs on its own goroutine. The channel closes BEFORE
// the record is filed, or a hello here takes it and queues the notice to the dying conn.
func (h *wsHub) evict(client *wsClient, reason string) {
	record := evictionRecord{
		at:          time.Now(),
		reason:      reason,
		undelivered: client.slowCount + len(client.send),
	}
	client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, reason)
	h.rememberEviction(client.ClientID(), record)
	go client.hangUp(websocket.StatusPolicyViolation, reason, evictionCloseGrace)
}

// Attempts the close frame, then aborts the transport: SO_LINGER 0 sends a RST, the only
// thing that reaches the peer without queuing behind the backlog.
func (c *wsClient) hangUp(code websocket.StatusCode, reason string, grace time.Duration) {
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = c.conn.Close(code, reason)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-closed:
	case <-timer.C:
	}
	c.abortTransport()
}

func (c *wsClient) abortTransport() {
	if c.rawConn == nil {
		_ = c.conn.CloseNow()
		return
	}
	if tcp, ok := c.rawConn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = c.rawConn.Close()
}

// rawConnKey carries the accepted connection down to the handler, the only way to
// reach it: the WebSocket handshake hijacks the conn behind a wrapper.
type rawConnKey struct{}

func withRawConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, rawConnKey{}, conn)
}

func rawConnFrom(ctx context.Context) net.Conn {
	conn, _ := ctx.Value(rawConnKey{}).(net.Conn)
	return conn
}

func (d *Daemon) sendEvictionNotice(client *wsClient, record evictionRecord) bool {
	notice := &protocol.ClientEvictionNoticeMessage{
		Event:               protocol.EventClientEvictionNotice,
		EvictedAt:           record.at.Format(time.RFC3339),
		Reason:              record.reason,
		UndeliveredMessages: record.undelivered,
	}
	data, err := json.Marshal(notice)
	if err != nil {
		d.logf("eviction notice marshal error: %v", err)
		return false
	}
	d.logf(
		"telling client %s it was evicted at %s (%s)",
		client.ClientID(), notice.EvictedAt, record.reason,
	)
	return d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
}
