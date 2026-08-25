package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/protocol"
)

type toxiProxy struct {
	t     *testing.T
	proxy *toxiproxy.Proxy
	addr  string
}

func newToxiProxy(t *testing.T, upstream string) *toxiProxy {
	t.Helper()
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("allocate toxiproxy port: %v", err)
	}
	listen := fmt.Sprintf("127.0.0.1:%d", port)

	// Both loggers: left at its default the proxy traces every toxic to stdout.
	silent := zerolog.New(io.Discard)
	api := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(prometheus.NewRegistry()), silent)
	p := toxiproxy.NewProxy(api, "attn-ws", listen, upstream)
	p.Logger = &silent
	if err := p.Start(); err != nil {
		t.Fatalf("start toxiproxy on %s: %v", listen, err)
	}
	t.Cleanup(p.Stop)
	return &toxiProxy{t: t, proxy: p, addr: listen}
}

func (p *toxiProxy) throttleDownstream(name string, rateKBPerSec int64) {
	p.t.Helper()
	spec := fmt.Sprintf(
		`{"name":%q,"type":"bandwidth","stream":"downstream","toxicity":1.0,"attributes":{"rate":%d}}`,
		name, rateKBPerSec,
	)
	if _, err := p.proxy.Toxics.AddToxicJson(strings.NewReader(spec)); err != nil {
		p.t.Fatalf("add bandwidth toxic %s: %v", name, err)
	}
}

func (p *toxiProxy) healDownstream(name string) {
	p.t.Helper()
	if err := p.proxy.Toxics.RemoveToxic(context.Background(), name); err != nil {
		p.t.Fatalf("remove toxic %s: %v", name, err)
	}
}

// Eviction sizing receipt, at 4 KB a message: the throttled link drains 2.5 msg/s against a
// 200 msg/s flood, so 256 slots fill in ~1.3s while the healthy client stays 3 orders under.
const (
	slowLinkRateKBPerSec = 10
	floodMessageBytes    = 4 << 10
	floodInterval        = 5 * time.Millisecond
)

// A tripwire: the daemon offers the close frame evictionCloseGrace (1s) then aborts
// the socket, so a working eviction lands in milliseconds.
const evictionDeathBudget = 5 * time.Second

// Measured through this proxy: after the daemon aborts with SO_LINGER 0 the throttled client
// still reads for another 65 seconds before a plain EOF — a userspace hop forwards no reset.
func TestWebSocketSlowClientIsEvictedOverADegradedLink(t *testing.T) {
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

	daemonAddr := "127.0.0.1:" + wsPort
	proxy := newToxiProxy(t, daemonAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	healthy := dialDaemonWS(t, ctx, daemonAddr)
	defer healthy.Close(websocket.StatusNormalClosure, "")
	healthyReads := drainWS(ctx, healthy)

	const slowClientID = "slow-client-under-test"
	slow := dialDaemonWSAs(t, ctx, proxy.addr, slowClientID)
	defer slow.Close(websocket.StatusNormalClosure, "")
	proxy.throttleDownstream("molasses", slowLinkRateKBPerSec)

	stopFlood := floodBroadcasts(d, floodMessageBytes, floodInterval)
	select {
	case <-evicted:
	case <-ctx.Done():
		stopFlood()
		t.Fatal("the hub never evicted the throttled client")
	}
	stopFlood()

	d.wsHub.mu.RLock()
	remaining := len(d.wsHub.clients)
	d.wsHub.mu.RUnlock()
	if remaining != 1 {
		t.Fatalf("hub holds %d clients after the eviction, want 1 (the healthy one)", remaining)
	}

	if err := healthyReads.err(); err != nil {
		t.Fatalf("healthy client was disturbed by the eviction: %v", err)
	}
	before := healthyReads.count()
	if before == 0 {
		t.Fatal("healthy client received nothing; the flood never reached it")
	}
	d.wsHub.Broadcast(&protocol.WebSocketEvent{Event: protocol.EventInitialState})
	waitForCond(t, 10*time.Second, "the healthy client to keep receiving", func() bool {
		return healthyReads.count() > before
	})

	proxy.healDownstream("molasses")
	recovered := dialDaemonWSAs(t, ctx, proxy.addr, slowClientID)
	defer recovered.Close(websocket.StatusNormalClosure, "")
	notice := readEventUntil(t, ctx, recovered, protocol.EventClientEvictionNotice)
	if got := asString(notice["reason"]); got != slowClientCloseReason {
		t.Errorf("eviction notice reason = %q, want %q", got, slowClientCloseReason)
	}
	if got, ok := notice["undelivered_messages"].(float64); !ok || got < 1 {
		t.Errorf("eviction notice undelivered_messages = %v, want at least 1", notice["undelivered_messages"])
	}
	if _, err := time.Parse(time.RFC3339, asString(notice["evicted_at"])); err != nil {
		t.Errorf("eviction notice evicted_at = %q, want RFC3339: %v", notice["evicted_at"], err)
	}

	recoveredReads := drainWS(ctx, recovered)
	d.wsHub.Broadcast(&protocol.WebSocketEvent{Event: protocol.EventInitialState})
	waitForCond(t, 10*time.Second, "the reconnected client to receive", func() bool {
		return recoveredReads.count() > 0
	})
	if err := recoveredReads.err(); err != nil {
		t.Fatalf("reconnected client failed over the healed link: %v", err)
	}
}

func dialDaemonWSAs(t *testing.T, ctx context.Context, addr, clientID string) *websocket.Conn {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	conn.SetReadLimit(64 << 20)
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

func dialDaemonWS(t *testing.T, ctx context.Context, addr string) *websocket.Conn {
	t.Helper()
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	conn.SetReadLimit(64 << 20)
	sendWorkspaceClientHello(t, conn)
	return conn
}

type wsReads struct {
	reads chan struct{}
	fail  chan error
	n     int
	e     error
}

func (r *wsReads) settle() {
	for {
		select {
		case <-r.reads:
			r.n++
		case err := <-r.fail:
			if r.e == nil {
				r.e = err
			}
		default:
			return
		}
	}
}

func (r *wsReads) count() int { r.settle(); return r.n }
func (r *wsReads) err() error { r.settle(); return r.e }

func drainWS(ctx context.Context, conn *websocket.Conn) *wsReads {
	r := &wsReads{
		reads: make(chan struct{}, 1<<16),
		fail:  make(chan error, 1),
	}
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				select {
				case r.fail <- err:
				default:
				}
				return
			}
			select {
			case r.reads <- struct{}{}:
			default:
			}
		}
	}()
	return r
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
