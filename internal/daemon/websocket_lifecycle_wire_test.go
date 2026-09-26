package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestStoppingTheDaemonClosesConnectionsThatNeverSaidHello(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	silent := transportDial(t, ctx, w)

	w.stop()

	if _, _, err := silent.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("a connection that never said hello read %v after the daemon stopped, want it closed", err)
	}
}

func transportDial(t *testing.T, ctx context.Context, w *world) *websocket.Conn {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return w.Dial(ctx) },
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+w.WSAddr+"/ws", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("dial the daemon's WebSocket: %v", err)
	}
	conn.SetReadLimit(-1)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func transportHello(clientID string, capabilities ...string) protocol.ClientHelloMessage {
	hello := protocol.ClientHelloMessage{
		Cmd:          protocol.CmdClientHello,
		ClientKind:   "tauri-app",
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: append([]string{protocol.CapabilityWorkspaceSessions}, capabilities...),
		ClientToken:  protocol.Ptr(config.ClientToken()),
	}
	if clientID != "" {
		hello.ClientID = protocol.Ptr(clientID)
	}
	return hello
}

func transportPeer(w *world, capabilities ...string) *testworld.Peer {
	w.T.Helper()
	p := w.Connect(transportHello("", capabilities...), nil)
	p.Initial = testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}

type transportFrame struct {
	binary bool
	data   []byte
	event  string
}

type transportRawPeer struct {
	t      *testing.T
	conn   *websocket.Conn
	frames chan transportFrame
}

func transportConnectRaw(t *testing.T, w *world, capabilities ...string) *transportRawPeer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	p := &transportRawPeer{t: t, conn: transportDial(t, ctx, w), frames: make(chan transportFrame, 4096)}
	go func() {
		defer close(p.frames)
		for {
			kind, data, err := p.conn.Read(context.Background())
			if err != nil {
				return
			}
			frame := transportFrame{binary: kind == websocket.MessageBinary, data: data}
			if !frame.binary {
				var envelope struct {
					Event string `json:"event"`
				}
				_ = json.Unmarshal(data, &envelope)
				frame.event = envelope.Event
			}
			p.frames <- frame
		}
	}()
	p.send(transportHello("", capabilities...))
	p.next("initial_state", func(f transportFrame) bool { return f.event == protocol.EventInitialState })
	return p
}

func (p *transportRawPeer) send(cmd any) {
	p.t.Helper()
	payload, err := json.Marshal(cmd)
	if err != nil {
		p.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		p.t.Fatalf("send %s: %v", payload, err)
	}
}

func (p *transportRawPeer) next(awaiting string, match func(transportFrame) bool) transportFrame {
	p.t.Helper()
	deadline := time.After(fakeagent.HangGuard)
	for {
		select {
		case frame, open := <-p.frames:
			if !open {
				p.t.Fatalf("the connection closed while awaiting %s", awaiting)
			}
			if match(frame) {
				return frame
			}
		case <-deadline:
			p.t.Fatalf("still awaiting %s after %s", awaiting, fakeagent.HangGuard)
		}
	}
}
