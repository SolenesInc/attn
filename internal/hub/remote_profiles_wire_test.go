package hub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestRemoteProfileChangesArriveThroughSnapshotsAndSessionUpdates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	frames := make(chan any)
	commands := make(chan protocol.PtyInputMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		var hello protocol.ClientHelloMessage
		if err := wsjson.Read(ctx, conn, &hello); err != nil {
			t.Error(err)
			return
		}
		go func() {
			for {
				var command protocol.PtyInputMessage
				if err := wsjson.Read(ctx, conn, &command); err != nil {
					return
				}
				select {
				case commands <- command:
				case <-ctx.Done():
					return
				}
			}
		}()
		for {
			select {
			case frame := <-frames:
				if err := wsjson.Write(ctx, conn, frame); err != nil {
					t.Error(err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}))
	defer func() { cancel(); server.Close() }()
	endpointStore := store.New()
	endpoint, err := endpointStore.AddEndpoint("existing outpost", "remote.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{}, 1)
	manager := hub.NewManager(endpointStore, nil, func(string) { changed <- struct{}{} }, nil, nil, nil)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		_ = manager.AttachEndpointConnection(ctx, endpoint.ID, conn, "")
		close(stopped)
	}()
	defer func() { _ = conn.CloseNow(); <-stopped }()
	for _, step := range []struct{ event, profile string }{
		{protocol.EventInitialState, "Default"},
		{protocol.EventSessionsUpdated, "Work"},
		{protocol.EventSessionRegistered, "Personal"},
		{protocol.EventSessionStateChanged, "Default"},
	} {
		var frame any
		session := protocol.Session{ID: "remote-worker", ProfileID: step.profile, State: protocol.SessionStateIdle}
		switch step.event {
		case protocol.EventInitialState:
			frame = protocol.InitialStateMessage{Event: step.event, Sessions: []protocol.Session{session, {ID: "other-session", ProfileID: "Other"}}, TerminalBindings: []protocol.TerminalBinding{{TerminalID: "worker-terminal", SessionID: session.ID}, {TerminalID: "other-terminal", SessionID: "other-session"}}, ProtocolVersion: protocol.Ptr(protocol.ProtocolVersion)}
		case protocol.EventSessionsUpdated:
			frame = protocol.SessionsUpdatedMessage{Event: step.event, Sessions: []protocol.Session{session}}
		default:
			frame = struct {
				Event   string           `json:"event"`
				Session protocol.Session `json:"session"`
			}{step.event, session}
		}
		select {
		case frames <- frame:
		case <-ctx.Done():
			t.Fatal("the remote endpoint stopped accepting session frames")
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("the hub did not publish the profile change in %s", step.event)
		}
		remote := manager.RemoteSession(session.ID)
		if remote == nil || remote.ProfileID != step.profile || protocol.Deref(remote.EndpointID) != endpoint.ID {
			t.Fatalf("%s lost the remote profile or endpoint: %+v", step.event, remote)
		}
	}
	forward := func(terminal protocol.TerminalID) {
		t.Helper()
		payload, err := json.Marshal(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "eA=="})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.ForwardPTYCommand(ctx, terminal, payload); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-commands:
			if got.ID != terminal || got.Data != "eA==" {
				t.Fatalf("forwarded %+v", got)
			}
		case <-ctx.Done():
			t.Fatal("endpoint received no PTY command")
		}
	}
	forward("other-terminal")
	frames <- protocol.TerminalBindingsUpdatedMessage{Event: protocol.EventTerminalBindingsUpdated, TerminalBindings: []protocol.TerminalBinding{{TerminalID: "new-other-terminal", SessionID: "other-session"}}}
	// A subsequent session change is the barrier after the binding update.
	frames <- protocol.SessionsUpdatedMessage{Event: protocol.EventSessionsUpdated, Sessions: []protocol.Session{{ID: "remote-worker", ProfileID: "Other"}}}
	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("hub did not apply the binding update")
	}
	if _, found := manager.EndpointIDForPTYTarget("other-terminal"); found {
		t.Fatal("removed terminal still routes")
	}
	forward("new-other-terminal")

}
