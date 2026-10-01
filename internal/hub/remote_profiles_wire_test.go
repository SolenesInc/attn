package hub_test

import (
	"context"
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
			frame = protocol.InitialStateMessage{Event: step.event, Sessions: []protocol.Session{session}, ProtocolVersion: protocol.Ptr(protocol.ProtocolVersion)}
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
}
