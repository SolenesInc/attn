package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestPluginDriverRun_IgnoresGenericPTYState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "plugin-state-owner",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWaitingInput,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("plugin-state-owner", "snipe-plugin", "run-state-owner") {
		t.Fatal("failed to begin plugin-owned state run")
	}

	d.handlePTYState("plugin-state-owner", pty.Observation{
		Source: pty.SourceWorkerInfo,
		Claim:  protocol.StateWorking,
		Detail: "worker info",
		At:     time.Now(),
	})
	if got := d.store.Get("plugin-state-owner").State; got != protocol.SessionStateWaitingInput {
		t.Fatalf("state=%q after generic PTY event, want plugin-owned waiting_input", got)
	}
}

func TestPluginDriverSessionClosed_FailedKillKeepsRunActive(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             "failed-kill",
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("failed-kill", "snipe-plugin", "run-live") {
		t.Fatal("failed to begin test plugin run")
	}
	d.ptyBackend = &fakeSpawnBackend{killErr: errors.New("kill failed")}

	ws := &wsClient{send: make(chan outboundMessage, 1), attachedStreams: make(map[string]ptybackend.Stream)}
	d.handleKillSession(ws, &protocol.KillSessionMessage{ID: "failed-kill"})
	sendPluginMethod(t, client, 21, "session.report_state", pluginReportStateParams{
		SessionID: "failed-kill",
		RunID:     "run-live",
		Seq:       1,
		State:     protocol.StatePendingApproval,
	})
	if got := d.store.Get("failed-kill").State; got != protocol.SessionStatePendingApproval {
		t.Fatalf("state=%q after failed kill report, want pending_approval", got)
	}
}

func registerTestPluginDriver(t *testing.T, conn net.Conn, agent string, capabilities map[string]bool) {
	t.Helper()
	sendPluginMethod(t, conn, 2, "driver.register", pluginDriverRegisterParams{
		Agent:        agent,
		Capabilities: capabilities,
	})
}

func sendPluginMethod(t *testing.T, conn net.Conn, id int, method string, params interface{}) {
	t.Helper()
	response := sendPluginMethodResponse(t, conn, id, method, params)
	if response.Error != nil {
		t.Fatalf("%s error=%#v", method, response.Error)
	}
}

func sendPluginMethodResponse(t *testing.T, conn net.Conn, id int, method string, params interface{}) jsonRPCMessage {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	if err := json.NewEncoder(conn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage([]byte(strconv.Itoa(id))),
		Method:  method,
		Params:  payload,
	}); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
	return decodeJSONRPCMessage(t, conn)
}
