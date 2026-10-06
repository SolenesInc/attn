package daemon_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAStateChangeReachesAnAppWhoseQueueIsFullOfAnotherSessionsOutput(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		noisy := w.bubbleClaude(t, app, "noisy")
		quiet := w.bubbleClaude(t, app, "quiet")
		app.TypeLine(quiet.id, "tidy the imports")
		testworld.AwaitSession(app, quiet.id, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWorking
		})

		ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
		defer cancel()
		behind := transportDial(t, ctx, w)
		evictionHello(t, ctx, behind, "behind")
		attach, err := json.Marshal(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(noisy.self)})
		if err != nil {
			t.Fatal(err)
		}
		if err := behind.Write(ctx, websocket.MessageText, attach); err != nil {
			t.Fatalf("attach the lagging app to %s: %v", noisy.id, err)
		}
		synctest.Wait()
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(noisy.self), Data: strings.Repeat("x", 1000)})
		synctest.Wait()

		quiet.reply("Which alias should stay? <!-- attn:state=waiting_input -->")

		var mu sync.Mutex
		var told, dropped bool
		go func() {
			for {
				_, data, err := behind.Read(context.Background())
				mu.Lock()
				if err != nil {
					dropped = true
					mu.Unlock()
					return
				}
				told = told || tellsState(data, quiet.id, protocol.SessionStateWaitingInput)
				mu.Unlock()
			}
		}()
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		switch {
		case !told && dropped:
			t.Fatalf("an app behind on %s's output was disconnected instead of told %s is waiting for input", noisy.id, quiet.id)
		case !told:
			t.Fatalf("an app still connected after catching up on %s's output was never told %s is waiting for input", noisy.id, quiet.id)
		}
	})
}

func tellsState(data []byte, id string, state protocol.SessionState) bool {
	var carrier struct {
		Session  *protocol.Session  `json:"session"`
		Sessions []protocol.Session `json:"sessions"`
	}
	if json.Unmarshal(data, &carrier) != nil {
		return false
	}
	if carrier.Session != nil {
		carrier.Sessions = append(carrier.Sessions, *carrier.Session)
	}
	for _, s := range carrier.Sessions {
		if string(s.ID) == id && s.State == state {
			return true
		}
	}
	return false
}
