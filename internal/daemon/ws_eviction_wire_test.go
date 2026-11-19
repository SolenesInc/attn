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

func TestAClientThatFallsBehindIsDroppedWithoutItsBacklog(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		stalled := evictionStalledClient(t, w, "fell-behind")

		for i := range 300 {
			setSetting(t, app, "queue_crew_enabled", strconv.FormatBool(i%2 == 0))
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
	})
}

func TestGardenUpdatesKeepFlowingWhenAnotherClientFallsBehind(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		seed := plantSeedAs(t, cli, "", "Garden delivery under backpressure")
		stalled := evictionStalledClient(t, w, "garden-fell-behind")
		var finalBody string
		for i := range 300 {
			finalBody = strconv.Itoa(i)
			if _, err := editSeedBody(t, cli, seed, finalBody); err != nil {
				t.Fatalf("edit the seed while another client is stalled: %v", err)
			}
			synctest.Wait()
		}
		testworld.Await(app, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			return len(m.Seeds) == 1 && m.Seeds[0].ID == seed && m.Seeds[0].Body == finalBody
		})
		evictionDrain(t, stalled)
		fresh := w.App()
		if protocol.Deref(fresh.Initial.SelectedProfileID) != app.SelectedProfile() {
			t.Fatal("a fresh client could not join the same profile after eviction")
		}
	})
}

func TestAClientWhoseSocketStopsAcceptingWritesIsDropped(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		stalled := evictionStalledClient(t, w, "stopped-reading")
		w.advance(16 * time.Second)

		if delivered := evictionDrain(t, stalled); len(delivered) > 0 {
			t.Errorf("a client that accepted no writes read %q after being dropped", delivered)
		}
	})
}

func TestAClientThatStopsAnsweringPingsIsDropped(t *testing.T) {
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
