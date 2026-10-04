package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"nhooyr.io/websocket"
)

func TestRemotePriorityOnlyUpdatesReachSessionSnapshots(t *testing.T) {
	session := protocol.Session{ID: "remote-session", Label: "Remote", State: protocol.SessionStateWaitingInput, TurnOwed: protocol.Ptr(true)}
	marked := session
	marked.Priority = protocol.Ptr(true)
	frames := []any{
		protocol.InitialStateMessage{Event: protocol.EventInitialState, ProtocolVersion: protocol.Ptr(protocol.ProtocolVersion), Sessions: []protocol.Session{session}},
		protocol.SessionStateChangedMessage{Event: protocol.EventSessionStateChanged, Session: marked},
		protocol.SessionsUpdatedMessage{Event: protocol.EventSessionsUpdated, Sessions: []protocol.Session{session}},
		protocol.SessionsUpdatedMessage{Event: protocol.EventSessionsUpdated, Sessions: []protocol.Session{marked}},
		protocol.SessionStateChangedMessage{Event: protocol.EventSessionStateChanged, Session: session},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for _, frame := range frames {
			data, err := json.Marshal(frame)
			if err != nil {
				t.Errorf("marshal: %v", err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, data); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	endpointStore := store.New()
	defer endpointStore.Close()
	endpoint, err := endpointStore.AddEndpoint("remote", "remote.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	var flags []bool
	var manager *Manager
	manager = NewManager(endpointStore, nil, func(id string) {
		sessions := manager.RemoteSessions()
		if len(sessions) != 1 {
			t.Errorf("sessions = %v, want one remote session", sessions)
			return
		}
		flags = append(flags, protocol.Deref(sessions[0].Priority))
	}, nil, nil, nil)
	connected, _ := manager.consumeRemote(t.Context(), endpoint.ID, conn)
	if !connected {
		t.Fatal("remote initial state was not accepted")
	}
	if !slices.Equal(flags, []bool{false, true, false, true, false}) {
		t.Fatalf("published priority flags = %v, want initial state and each single/bulk priority change", flags)
	}
}
