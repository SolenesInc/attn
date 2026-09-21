package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"nhooyr.io/websocket"
)

func TestWSHubStopsAndClosesClients(t *testing.T) {
	hub := newWSHub()
	client := &wsClient{
		send: make(chan outboundMessage),
	}
	hub.add(client)

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		hub.runUntil(done)
		close(stopped)
	}()

	hub.closeAll()
	close(done)

	<-stopped
	if got := hub.ClientCount(); got != 0 {
		t.Fatalf("client count = %d, want 0", got)
	}
	if !client.sendChannelClosed() {
		t.Fatal("client send channel remained open")
	}
}

func TestDaemonStopClosesPreHelloWebSocket(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	port := useFreeWSPort(t)
	d := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))

	startErr := make(chan error, 1)
	go func() { startErr <- d.Start() }()
	<-d.Started()

	conn, _, err := websocket.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%s/ws", port), nil)
	if err != nil {
		t.Fatalf("dial WebSocket: %v", err)
	}

	d.Stop()
	if err := <-startErr; err != nil {
		t.Fatalf("Start() after Stop() = %v", err)
	}
	if _, _, err := conn.Read(context.Background()); err == nil {
		t.Fatal("pre-hello WebSocket remained open after Stop")
	}
}

func TestWSHubRejectsAdmissionAfterStop(t *testing.T) {
	hub := newWSHub()
	hub.closeAll()

	client := &wsClient{send: make(chan outboundMessage)}
	if hub.track(client) {
		t.Fatal("stopped hub tracked a new connection")
	}
	if hub.add(client) {
		t.Fatal("stopped hub admitted a new client")
	}
}
