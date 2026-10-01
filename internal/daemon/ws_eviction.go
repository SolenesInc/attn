package daemon

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	evictionCloseGrace     = 1 * time.Second
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
	h.evictionMu.Unlock()
	if h.evictionListener != nil {
		h.evictionListener(clientID, record)
	}
}

func (h *wsHub) deliverEviction(clientID string, send func(evictionRecord) bool) {
	if clientID == "" {
		return
	}
	h.evictionMu.Lock()
	defer h.evictionMu.Unlock()
	h.pruneEvictionsLocked(time.Now())
	record, ok := h.evictions[clientID]
	if ok && send(record) {
		delete(h.evictions, clientID)
	}
}

func (h *wsHub) pruneEvictionsLocked(now time.Time) {
	for id, rec := range h.evictions {
		if now.Sub(rec.at) > evictionMemoryTTL {
			delete(h.evictions, id)
		}
	}
}

func (h *wsHub) evict(client *wsClient, reason string) {
	record := evictionRecord{
		at:          time.Now(),
		reason:      reason,
		undelivered: len(client.send) + 1,
	}
	if !client.closeSendChannelWithStatus(websocket.StatusPolicyViolation, reason) {
		return
	}
	h.rememberEviction(client.ClientID(), record)
	goTransport(func() { client.hangUp(websocket.StatusPolicyViolation, reason, evictionCloseGrace) })
}

func (c *wsClient) hangUp(code websocket.StatusCode, reason string, grace time.Duration) {
	closed := make(chan struct{})
	goTransport(func() {
		defer close(closed)
		_ = c.conn.Close(code, reason)
	})
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

type rawConnKey struct{}

func withRawConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, rawConnKey{}, conn)
}

func rawConnFrom(ctx context.Context) net.Conn {
	conn, _ := ctx.Value(rawConnKey{}).(net.Conn)
	return conn
}

func (d *Daemon) deliverEvictionNotice(client *wsClient) {
	var full bool
	d.wsHub.deliverEviction(client.ClientID(), func(record evictionRecord) bool {
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
		d.logf("telling client %s it was evicted at %s (%s)", client.ClientID(), notice.EvictedAt, record.reason)
		queued, queueFull := client.offer(outboundMessage{kind: messageKindText, payload: data})
		full = queueFull
		return queued
	})
	if full && client.conn != nil {
		d.wsHub.logf("WebSocket client stopped draining its %d queued messages, disconnecting", len(client.send))
		d.wsHub.evict(client, slowClientCloseReason)
		d.wsHub.forget(client)
	}
}
