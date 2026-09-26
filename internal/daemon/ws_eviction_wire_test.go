package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAClientThatFallsBehindIsDroppedWithoutItsBacklogAndToldWhyOnce(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		stalled := evictionStalledClient(t, w, "fell-behind")

		for i := range 300 {
			setSetting(t, app, "workflows_enabled", strconv.FormatBool(i%2 == 0))
		}
		w.advance(2 * time.Second)

		backlog := 0
		for _, event := range evictionDrain(t, stalled) {
			if event == protocol.EventSettingsUpdated {
				backlog++
			}
		}
		if backlog > 0 {
			t.Errorf("the dropped client was fed %d settings_updated frames of the backlog it fell behind on", backlog)
		}
		notice := evictionNotice(t, w, "fell-behind")
		if notice == nil || notice.Reason != "client too slow" || notice.UndeliveredMessages < 256 {
			t.Fatalf("reconnecting after falling behind brought eviction notice %+v, want client too slow with the undelivered backlog", notice)
		}
		if _, err := time.Parse(time.RFC3339, notice.EvictedAt); err != nil {
			t.Errorf("the eviction notice says it happened at %q: %v", notice.EvictedAt, err)
		}
		if again := evictionNotice(t, w, "fell-behind"); again != nil {
			t.Errorf("reconnecting a second time brought eviction notice %+v again", again)
		}
	})
}

func TestAClientWhoseSocketStopsAcceptingWritesIsDroppedAndToldWhyWhileItIsRemembered(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		prompt := evictionStalledClient(t, w, "returns-soon")
		late := evictionStalledClient(t, w, "returns-late")
		w.advance(16 * time.Second)

		for _, conn := range []*websocket.Conn{prompt, late} {
			if delivered := evictionDrain(t, conn); len(delivered) > 0 {
				t.Errorf("a client that accepted no writes read %q after being dropped", delivered)
			}
		}
		if notice := evictionNotice(t, w, "returns-soon"); notice == nil || notice.Reason != "client too slow" || notice.UndeliveredMessages < 1 {
			t.Errorf("reconnecting soon after the stall brought eviction notice %+v, want client too slow with its undelivered message", notice)
		}
		w.advance(10 * time.Minute)
		if notice := evictionNotice(t, w, "returns-late"); notice != nil {
			t.Errorf("reconnecting eleven minutes after the stall brought eviction notice %+v, want it forgotten", notice)
		}
	})
}

func TestAClientThatGoesQuietOwingNothingIsDroppedWithoutANotice(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
		defer cancel()
		link := &evictionLink{frozen: make(chan struct{}), severed: make(chan struct{}), closed: make(chan struct{})}
		quiet := evictionDialThrough(t, ctx, w, link)
		go func() {
			for {
				if _, _, err := quiet.Read(context.Background()); err != nil {
					return
				}
			}
		}()
		evictionHello(t, ctx, quiet, "went-quiet")
		synctest.Wait()
		close(link.frozen)

		w.advance(45 * time.Second)

		select {
		case <-link.severed:
		default:
			t.Error("the daemon still holds the connection of a client that stopped answering pings")
		}
		if notice := evictionNotice(t, w, "went-quiet"); notice != nil {
			t.Errorf("a client dropped while owed nothing was told %+v on its return", notice)
		}
	})
}

type evictionLink struct {
	net.Conn
	frozen, severed, closed chan struct{}
	severOnce, closeOnce    sync.Once
}

func (l *evictionLink) Read(p []byte) (int, error) {
	n, err := l.Conn.Read(p)
	select {
	case <-l.frozen:
	default:
		return n, err
	}
	for err == nil {
		_, err = l.Conn.Read(p)
	}
	l.severOnce.Do(func() { close(l.severed) })
	<-l.closed
	return 0, net.ErrClosed
}

func (l *evictionLink) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.Conn.Close()
}

func evictionDialThrough(t *testing.T, ctx context.Context, w *world, link *evictionLink) *websocket.Conn {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := w.Dial(ctx)
			link.Conn = conn
			return link, err
		},
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+w.WSAddr+"/ws", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("dial the daemon's WebSocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func evictionStalledClient(t *testing.T, w *world, clientID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	conn := transportDial(t, ctx, w)
	evictionHello(t, ctx, conn, clientID)
	synctest.Wait()
	return conn
}

func evictionHello(t *testing.T, ctx context.Context, conn *websocket.Conn, clientID string) {
	t.Helper()
	hello, err := json.Marshal(transportHello(clientID))
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("send client_hello as %s: %v", clientID, err)
	}
}

func evictionDrain(t *testing.T, conn *websocket.Conn) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	var delivered []string
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatalf("the dropped client was still connected %s later", fakeagent.HangGuard)
			}
			return delivered
		}
		var frame struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal(data, &frame)
		delivered = append(delivered, frame.Event)
	}
}

func evictionNotice(t *testing.T, w *world, clientID string) *protocol.ClientEvictionNoticeMessage {
	t.Helper()
	p := w.Connect(transportHello(clientID), nil)
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	synctest.Wait()
	for _, e := range p.Received() {
		if e.Event == protocol.EventClientEvictionNotice {
			notice := testworld.Await[protocol.ClientEvictionNoticeMessage](p, protocol.EventClientEvictionNotice, nil)
			p.Close()
			return &notice
		}
	}
	p.Close()
	return nil
}
