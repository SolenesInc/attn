package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

const evictionDeathBudget = 5 * time.Second

func dialDaemonWSAs(t *testing.T, ctx context.Context, addr, clientID string) *websocket.Conn {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	conn.SetReadLimit(-1)
	sendClientHelloAs(t, conn, clientID)
	return conn
}

func readEventUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, event string) map[string]interface{} {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", event, err)
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if asString(msg["event"]) == event {
			return msg
		}
	}
}

func floodBroadcasts(d *Daemon, payloadBytes int, every time.Duration) func() {
	stop := make(chan struct{})
	stopped := make(chan struct{})
	payload := []byte(strings.Repeat("x", payloadBytes))
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case d.wsHub.broadcast <- outboundMessage{kind: messageKindText, payload: payload}:
				case <-stop:
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-stopped
	}
}

func readUntilClosed(ctx context.Context, conn *websocket.Conn) error {
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return err
		}
	}
}

func waitForCond(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
