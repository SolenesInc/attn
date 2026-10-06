package daemon_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAClientThatStopsReadingAFloodingTerminalIsToldToResyncWhenItReadsAgain(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		id := app.Terminal(w.Spawn(app, fakeagent.Claude, w.Path("shop")))
		ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
		defer cancel()
		slow := transportDial(t, ctx, w)
		evictionHello(t, ctx, slow, "slow-reader")
		backpressureReadUntil(t, slow, func(e protocol.WebSocketEvent) bool { return e.Event == protocol.EventInitialState })
		attach, err := json.Marshal(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(id)})
		if err != nil {
			t.Fatal(err)
		}
		if err := slow.Write(ctx, websocket.MessageText, attach); err != nil {
			t.Fatal(err)
		}
		backpressureReadUntil(t, slow, func(e protocol.WebSocketEvent) bool {
			return e.Event == protocol.EventAttachResult && protocol.Deref(e.ID) == id
		})

		for range 3 {
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(id), Data: strings.Repeat("x", 200)})
			synctest.Wait()
		}
		w.advance(2 * time.Second)

		backpressureReadUntil(t, slow, func(e protocol.WebSocketEvent) bool {
			return e.Event == protocol.EventPtyDesync && protocol.Deref(e.ID) == id
		})
	})
}

func backpressureReadUntil(t *testing.T, conn *websocket.Conn, match func(protocol.WebSocketEvent) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("the client read everything the daemon sent without the event it awaited: %v", err)
		}
		var e protocol.WebSocketEvent
		if json.Unmarshal(data, &e) == nil && match(e) {
			return
		}
	}
}
