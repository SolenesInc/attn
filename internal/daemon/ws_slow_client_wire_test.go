package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASlowAppIsDroppedWhileHealthyAppsKeepUp(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	healthy := w.App()
	agent := w.Spawn(healthy, fakeagent.Claude, w.Path("shop"))
	noisy := w.Spawn(healthy, workspaceShell, w.Path("logs"))
	cli := w.Client()
	link := newSlowClientLink(t, w.WSAddr)

	slow := link.connect(t, w, "slow-app")
	slow.await(t, protocol.EventInitialState)
	slow.send(t, protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, DashboardVisible: true})
	slow.send(t, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: noisy})
	slow.await(t, protocol.EventAttachResult)
	if tier := presenceTier(t, cli); tier != "watching" {
		t.Fatalf("with the slow app watching the dashboard the presence tier is %q, want watching", tier)
	}
	link.throttle()
	healthy.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: noisy, Data: "head -c 24000000 /dev/zero | tr '\\0' x; exit\r"})
	testworld.Await(healthy, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == noisy })

	todo := strings.Repeat("x", 48<<10)
	deadline := time.After(fakeagent.HangGuard)
	reports := 0
	for presenceTier(t, cli) == "watching" {
		select {
		case <-deadline:
			t.Fatalf("the slow app is still connected after %d reports on its throttled link", reports)
		default:
		}
		reports++
		label := fmt.Sprint(reports)
		if err := cli.UpdateTodos(agent, []string{label, todo}); err != nil {
			t.Fatalf("report todos: %v", err)
		}
		testworld.AwaitSession(healthy, agent, func(s protocol.Session) bool { return len(s.Todos) > 0 && s.Todos[0] == label })
	}

	link.heal()
	select {
	case <-slow.ended:
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("the slow app is still connected %s after its link healed", fakeagent.HangGuard)
	}

	back := link.connect(t, w, "slow-app")
	returned := back.await(t, protocol.EventClientEvictionNotice, protocol.EventInitialState)
	var notice protocol.ClientEvictionNoticeMessage
	if err := json.Unmarshal(returned[protocol.EventClientEvictionNotice], &notice); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, notice.EvictedAt); err != nil || notice.Reason != "client too slow" || notice.UndeliveredMessages < 1 {
		t.Errorf("the returning app was told %+v, want when and why it was dropped and how much it missed", notice)
	}

	if err := cli.UpdateTodos(agent, []string{"after the slow app left"}); err != nil {
		t.Fatalf("report todos: %v", err)
	}
	testworld.AwaitSession(healthy, agent, func(s protocol.Session) bool { return slices.Equal(s.Todos, []string{"after the slow app left"}) })
}

func presenceTier(t *testing.T, cli *client.Client) string {
	t.Helper()
	status, err := cli.ActivityStatus()
	if err != nil {
		t.Fatalf("activity status: %v", err)
	}
	return status.PresenceTier
}

type slowClientLink struct {
	proxy *toxiproxy.Proxy
	addr  string
	t     *testing.T
}

func newSlowClientLink(t *testing.T, upstream string) *slowClientLink {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	silent := zerolog.New(io.Discard)
	api := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(prometheus.NewRegistry()), silent)
	proxy := toxiproxy.NewProxy(api, "attn-ws", addr, upstream)
	proxy.Logger = &silent
	if err := proxy.Start(); err != nil {
		t.Fatalf("start toxiproxy on %s: %v", addr, err)
	}
	t.Cleanup(proxy.Stop)
	return &slowClientLink{proxy: proxy, addr: addr, t: t}
}

func (l *slowClientLink) throttle() {
	l.t.Helper()
	toxic := `{"name":"molasses","type":"bandwidth","stream":"downstream","toxicity":1.0,"attributes":{"rate":10}}`
	if _, err := l.proxy.Toxics.AddToxicJson(strings.NewReader(toxic)); err != nil {
		l.t.Fatalf("throttle the link: %v", err)
	}
}

func (l *slowClientLink) heal() {
	l.t.Helper()
	if err := l.proxy.Toxics.RemoveToxic(context.Background(), "molasses"); err != nil {
		l.t.Fatalf("heal the link: %v", err)
	}
}

type slowClientConn struct {
	conn   *websocket.Conn
	frames chan []byte
	ended  chan struct{}
}

func (l *slowClientLink) connect(t *testing.T, w *world, clientID string) *slowClientConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+l.addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial the daemon through the link: %v", err)
	}
	conn.SetReadLimit(-1)
	t.Cleanup(func() { _ = conn.CloseNow() })
	token, err := os.ReadFile(filepath.Join(w.Dir, config.ClientTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	hello, err := json.Marshal(protocol.ClientHelloMessage{
		Cmd: protocol.CmdClientHello, ClientKind: "tauri-app", ClientID: protocol.Ptr(clientID),
		Version:      "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions, protocol.CapabilityBinaryPtyOutput},
		ClientToken:  protocol.Ptr(strings.TrimSpace(string(token))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("send client_hello: %v", err)
	}
	c := &slowClientConn{conn: conn, frames: make(chan []byte, 1<<16), ended: make(chan struct{})}
	go func() {
		defer close(c.ended)
		for {
			kind, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if kind == websocket.MessageText {
				select {
				case c.frames <- data:
				default:
				}
			}
		}
	}()
	return c
}

func (c *slowClientConn) send(t *testing.T, cmd any) {
	t.Helper()
	payload, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("send %s: %v", payload, err)
	}
}

func (c *slowClientConn) await(t *testing.T, events ...string) map[string]json.RawMessage {
	t.Helper()
	found := map[string]json.RawMessage{}
	timeout := time.After(fakeagent.HangGuard)
	for len(found) < len(events) {
		select {
		case data := <-c.frames:
			var envelope struct {
				Event string `json:"event"`
			}
			if json.Unmarshal(data, &envelope) == nil && slices.Contains(events, envelope.Event) {
				found[envelope.Event] = data
			}
		case <-c.ended:
			t.Fatalf("the connection ended while awaiting %v", events)
		case <-timeout:
			t.Fatalf("still awaiting %v after %s", events, fakeagent.HangGuard)
		}
	}
	return found
}
