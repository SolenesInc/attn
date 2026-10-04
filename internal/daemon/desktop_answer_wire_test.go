package daemon_test

import (
	"encoding/json"
	"testing"
	"testing/synctest"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type loggedEvent struct {
	Event     string  `json:"event"`
	RequestID *string `json:"request_id"`
	ID        string  `json:"id"`
}

func logSince(t *testing.T, p *testworld.Peer, mark int) []loggedEvent {
	t.Helper()
	var events []loggedEvent
	for _, raw := range p.Log()[mark:] {
		var e loggedEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("event does not decode: %v: %s", err, raw)
		}
		events = append(events, e)
	}
	return events
}

// ownAnswerFirst checks the requester's first arrangement after mark is its tagged answer, ahead of its result.
func ownAnswerFirst(t *testing.T, events []loggedEvent, requestID, resultEvent string) loggedEvent {
	t.Helper()
	for _, e := range events {
		switch {
		case e.Event == protocol.EventProfileArrangementChanged && protocol.Deref(e.RequestID) == requestID:
			return e
		case e.Event == protocol.EventProfileArrangementChanged:
			t.Fatalf("the requester saw an arrangement not marked as its answer before its answer: %+v", events)
		case e.Event == resultEvent && (protocol.Deref(e.RequestID) == requestID || e.ID == requestID):
			t.Fatalf("the requester got its %s before its arrangement answer: %+v", resultEvent, events)
		}
	}
	t.Fatalf("the requester never got an arrangement answering request %s: %+v", requestID, events)
	return loggedEvent{}
}

func TestAClientsOwnArrangementChangeReachesItAsItsAnswerBeforeItsResult(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		desktopA, paneA := viewProfile(t, w, profileID).paneOf(t, "a")
		second := createDesktop(app, profileID)
		switchDesktop(app, profileID, second.ID)
		injectAgent(t, w, "b")
		watcher := w.AppOn(profileID)
		synctest.Wait()
		appMark, watcherMark := len(app.Log()), len(watcher.Log())

		requestID := uuid.NewString()
		mustProfileRequest(app, protocol.DesktopShowLeafMessage{Cmd: protocol.CmdDesktopShowLeaf, RequestID: requestID, DesktopID: desktopA.ID, LeafID: paneA}, requestID)

		synctest.Wait()
		ownAnswerFirst(t, logSince(t, app, appMark), requestID, protocol.EventProfileActionResult)
		for _, e := range logSince(t, watcher, watcherMark) {
			if e.RequestID != nil {
				t.Fatalf("another client received an answer to someone else's request: %+v", e)
			}
		}
		awaitShown(watcher, profileID, desktopA.ID, paneA)
	})
}

func TestOpeningADocumentAnswersTheOpenerBeforeTheBroadcastTheOpenCaused(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		injectAgent(t, w, "a")
		notes := writeNotes(t, w)
		synctest.Wait()
		mark := len(app.Log())

		requestID := uuid.NewString()
		opened := testworld.Request(app, protocol.OpenMarkdownMessage{
			Cmd: protocol.CmdOpenMarkdown, Path: notes, SessionID: protocol.Ptr("a"), RequestID: protocol.Ptr(requestID),
		}, protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool {
			return protocol.Deref(r.RequestID) == requestID
		})
		if !opened.Success {
			t.Fatalf("opening %s refused: %s", notes, protocol.Deref(opened.Error))
		}

		synctest.Wait()
		ownAnswerFirst(t, logSince(t, app, mark), requestID, protocol.EventOpenMarkdownResult)
	})
}

func TestALaunchPlacementReachesTheLauncherAsItsAnswerBeforeTheSpawnResult(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	mark := len(app.Log())

	spawned, _, pane := w.RequestSpawn(app, fakeagent.Claude, w.Path("work"))
	if !spawned.Success || pane == "" {
		t.Fatalf("spawn answered %+v with pane %q", spawned, pane)
	}

	ownAnswerFirst(t, logSince(t, app, mark), spawned.ID, protocol.EventSpawnResult)
}

func TestAChangeThatLandsWhileAClientsRequestIsHeldStillReachesItWhenTheRequestFails(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		injectAgent(t, w, "a")
		desktop, _ := viewProfile(t, w, app.SelectedProfile()).paneOf(t, "a")
		paused, resume := make(chan struct{}), make(chan struct{})
		w.daemon.PauseNextHeldAction(paused, resume)
		mark := len(app.Log())

		requestID := uuid.NewString()
		app.Send(protocol.DesktopShowLeafMessage{Cmd: protocol.CmdDesktopShowLeaf, RequestID: requestID, DesktopID: desktop.ID, LeafID: "no-such-leaf"})
		<-paused
		injectAgent(t, w, "b")
		close(resume)
		synctest.Wait()

		refused := false
		for _, raw := range app.Log()[mark:] {
			var result protocol.ProfileActionResultMessage
			if json.Unmarshal(raw, &result) == nil && result.Event == protocol.EventProfileActionResult && result.RequestID == requestID {
				refused = !result.Success
			}
		}
		if !refused {
			t.Fatalf("showing a missing leaf was not refused: %s", app.Log()[mark:])
		}
		for _, raw := range app.Log()[mark:] {
			var arrangement protocol.ProfileArrangementChangedMessage
			if json.Unmarshal(raw, &arrangement) != nil || arrangement.Event != protocol.EventProfileArrangementChanged {
				continue
			}
			for _, d := range arrangement.Desktops {
				for _, pane := range d.Panes {
					if pane.SessionID == "b" {
						return
					}
				}
			}
		}
		t.Fatalf("the client never learned of agent b, placed while its refused request held its broadcasts")
	})
}
