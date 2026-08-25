package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// attn is a full-duplex JSON-RPC peer, so one reader routes by shape — a method is
// a request, a bare id a response. A peer assuming its response came next loses one.
type pluginFixturePeer struct {
	t       *testing.T
	conn    net.Conn
	decoder *json.Decoder
	nextID  int
}

// Takes over reading conn; nothing else may read from it.
func newPluginFixturePeer(t *testing.T, conn net.Conn) *pluginFixturePeer {
	// Id 1 is the hello the fixture writes before handing the socket over.
	return &pluginFixturePeer{t: t, conn: conn, decoder: json.NewDecoder(conn), nextID: 1}
}

func (p *pluginFixturePeer) call(method string, params interface{}) jsonRPCMessage {
	p.t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		p.t.Fatalf("marshal %s params: %v", method, err)
	}
	p.nextID++
	id := strconv.Itoa(p.nextID)
	if err := p.write(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(id),
		Method:  method,
		Params:  payload,
	}); err != nil {
		p.t.Fatalf("send %s: %v", method, err)
	}
	return p.awaitResponse(id)
}

func (p *pluginFixturePeer) callOK(method string, params interface{}) {
	p.t.Helper()
	if response := p.call(method, params); response.Error != nil {
		p.t.Fatalf("%s error=%#v", method, response.Error)
	}
}

func (p *pluginFixturePeer) awaitResponse(id string) jsonRPCMessage {
	p.t.Helper()
	for {
		message, err := p.read()
		if err != nil {
			p.t.Fatalf("read response id=%s: %v", id, err)
		}
		if message.Method != "" {
			p.handle(message)
			continue
		}
		if jsonRPCIDKey(message.ID) != id {
			p.t.Fatalf("plugin response id=%s while waiting for id=%s", jsonRPCIDKey(message.ID), id)
		}
		return message
	}
}

func (p *pluginFixturePeer) serve() {
	for {
		message, err := p.read()
		if err != nil {
			return
		}
		if message.Method == "" {
			continue
		}
		p.handle(message)
	}
}

func (p *pluginFixturePeer) handle(request jsonRPCMessage) {
	p.t.Helper()
	switch request.Method {
	case pluginHealthMethod:
		p.respond(request.ID, pluginHealthResult{OK: true})
	case "driver.session_closed":
		var params pluginDriverSessionClosedParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			p.t.Fatalf("decode fixture session close params: %v", err)
		}
		appendPluginFixtureCloseRecord(p.t, pluginDriverCloseRecord{Params: params})
		p.respond(request.ID, pluginDriverSessionClosedResult{OK: true})
	case "driver.spawn", "driver.resume":
		p.launch(request)
	default:
		_ = p.write(jsonRPCFailure(request.ID, jsonRPCMethodNotFound, fmt.Sprintf("unknown method %q", request.Method)))
	}
}

func (p *pluginFixturePeer) launch(request jsonRPCMessage) {
	p.t.Helper()
	var params pluginDriverSpawnParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		p.t.Fatalf("decode fixture launch params: %v", err)
	}
	appendPluginFixtureRecord(p.t, pluginDriverFixtureRecord{Method: request.Method, Params: params})
	script := `IFS= read -r input; printf 'PLUGIN_RUN method=%s cwd=%s input=%s\n' "$ATTN_PLUGIN_FIXTURE_METHOD" "$PWD" "$input"; trap 'exit 0' TERM INT; while :; do sleep 1; done`
	p.respond(request.ID, pluginDriverSpawnResult{
		Argv: []string{"/bin/sh", "-c", script},
		Env:  map[string]string{"ATTN_PLUGIN_FIXTURE_METHOD": request.Method},
		CWD:  os.Getenv("ATTN_DRIVER_FIXTURE_CWD"),
	})
	p.callOK("session.report_state", pluginReportStateParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       1,
		State:     protocol.StateWorking,
	})
	p.callOK("session.report_metadata", pluginReportMetadataParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       2,
		Metadata:  json.RawMessage(`{"native_id":"` + request.Method + `-native"}`),
	})
	p.callOK("session.report_stop", pluginReportStopParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       3,
		Verdict:   protocol.StateWaitingInput,
	})
	if request.Method != "driver.spawn" {
		return
	}
	waitForPluginFixtureStateTrigger(p.t)
	p.callOK("session.report_state", pluginReportStateParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       4,
		State:     protocol.StateWorking,
	})
	p.callOK("session.report_stop", pluginReportStopParams{
		SessionID: params.SessionID,
		RunID:     params.RunID,
		Seq:       5,
		Verdict:   protocol.StateWaitingInput,
	})
}

func (p *pluginFixturePeer) respond(id json.RawMessage, result interface{}) {
	p.t.Helper()
	if err := p.write(jsonRPCResult(id, result)); err != nil {
		p.t.Fatalf("respond to request id=%s: %v", jsonRPCIDKey(id), err)
	}
}

func (p *pluginFixturePeer) read() (jsonRPCMessage, error) {
	var message jsonRPCMessage
	err := p.decoder.Decode(&message)
	return message, err
}

func (p *pluginFixturePeer) write(message jsonRPCMessage) error {
	return json.NewEncoder(p.conn).Encode(message)
}

// The ordering the end-to-end fixture used to fail on: under CI load the daemon
// wrote driver.session_closed before answering the report the fixture awaited.
func TestPluginFixturePeerAnswersRequestArrivingBeforeItsResponse(t *testing.T) {
	closeLog := filepath.Join(t.TempDir(), "driver-close.jsonl")
	t.Setenv("ATTN_DRIVER_FIXTURE_CLOSE_LOG", closeLog)

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	scripted := make(chan error, 1)
	go func() { scripted <- scriptCloseBeforeReportResponse(server) }()

	peer := newPluginFixturePeer(t, client)
	peer.callOK("session.report_stop", pluginReportStopParams{
		SessionID: "session-1",
		RunID:     "run-1",
		Seq:       3,
		Verdict:   protocol.StateWaitingInput,
	})
	if err := <-scripted; err != nil {
		t.Fatalf("scripted daemon: %v", err)
	}

	records, ok := readPluginFixtureCloseRecords(closeLog, 1)
	if !ok {
		t.Fatal("peer recorded no close notification, want the one that arrived mid-request")
	}
	if records[0].Params.RunID != "run-1" || records[0].Params.Reason != "exited" {
		t.Fatalf("close record=%+v, want exited notification for run-1", records[0].Params)
	}
}

func scriptCloseBeforeReportResponse(conn net.Conn) error {
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var report jsonRPCMessage
	if err := decoder.Decode(&report); err != nil {
		return fmt.Errorf("read plugin report: %w", err)
	}
	if report.Method != "session.report_stop" {
		return fmt.Errorf("first plugin message method=%q, want session.report_stop", report.Method)
	}

	params, err := json.Marshal(pluginDriverSessionClosedParams{
		SessionID: "session-1",
		RunID:     "run-1",
		Reason:    "exited",
	})
	if err != nil {
		return fmt.Errorf("marshal close params: %w", err)
	}
	if err := encoder.Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("900"),
		Method:  "driver.session_closed",
		Params:  params,
	}); err != nil {
		return fmt.Errorf("send close notification: %w", err)
	}

	var ack jsonRPCMessage
	if err := decoder.Decode(&ack); err != nil {
		return fmt.Errorf("read close acknowledgement: %w", err)
	}
	if jsonRPCIDKey(ack.ID) != "900" || ack.Method != "" {
		return fmt.Errorf("close acknowledgement=%+v, want a response to id 900", ack)
	}
	if err := encoder.Encode(jsonRPCResult(report.ID, struct{}{})); err != nil {
		return fmt.Errorf("answer plugin report: %w", err)
	}
	return nil
}
