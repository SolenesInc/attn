package daemon_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASupportSnapshotCarriesBoundedEvidenceWithoutInputOrWarningText(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "not-a-backend")
	w := newWorld(t)
	app := w.App()
	shell := w.Spawn(app, workspaceShell, w.Path("shop"))
	gone := w.Spawn(app, workspaceShell, w.Path("docs"))
	exitWorkspaceShells(app, gone)

	empty, _ := supportSnapshotRequest(app, "before", nil)
	capacity := empty.TraceCapacity
	if capacity <= 0 || empty.TraceTotal != 0 || len(empty.InputTraces) != 0 {
		t.Fatalf("a fresh daemon's snapshot holds %d of %d traces with capacity %d, want none", len(empty.InputTraces), empty.TraceTotal, capacity)
	}

	probes := 0
	probe := func() {
		t.Helper()
		probes++
		id := fmt.Sprintf("probe-%d", probes)
		answer := testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: shell, Data: "a", Source: protocol.Ptr("automation"), ProbeID: protocol.Ptr(id)},
			protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == id })
		if answer.ID != shell || !answer.Success || answer.WriteDurationUs < 0 {
			t.Errorf("probed input %s answered %+v, want a successful write to %s", id, answer, shell)
		}
	}
	for i := range capacity + 2 {
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: gone, Data: "lost secret", TraceID: protocol.Ptr(fmt.Sprintf("gone-%d", i))})
		if i%100 == 99 {
			probe()
		}
	}
	for _, trace := range []string{"kept-1", "kept-2"} {
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: shell, Data: "do not retain me", Source: protocol.Ptr("user"), TraceID: protocol.Ptr(trace)})
	}

	snapshot, raw := supportSnapshotRequest(app, "after", []string{shell})
	acknowledged := 0
	for _, e := range app.Received() {
		if e.Event == protocol.EventPtyInputProbeResult {
			acknowledged++
		}
	}
	if acknowledged != probes {
		t.Errorf("the daemon acknowledged %d inputs, want only the %d probed ones", acknowledged, probes)
	}
	if snapshot.TraceTotal != capacity+4 || len(snapshot.InputTraces) != capacity {
		t.Fatalf("the snapshot holds %d of %d traces, want the newest %d of %d", len(snapshot.InputTraces), snapshot.TraceTotal, capacity, capacity+4)
	}
	if !slices.IsSortedFunc(snapshot.InputTraces, func(a, b protocol.SupportInputTrace) int { return a.Sequence - b.Sequence }) {
		t.Errorf("the traces are not in the order they arrived")
	}
	oldest, newest := snapshot.InputTraces[0], snapshot.InputTraces[capacity-2:]
	if oldest.TraceID != "gone-4" || oldest.RuntimeID != gone || oldest.WriteResult != "failed" || protocol.Deref(oldest.ErrorClass) != "session_not_found" {
		t.Errorf("the oldest kept trace = %+v, want gone-4, failed as session_not_found", oldest)
	}
	for i, trace := range newest {
		if trace.TraceID != fmt.Sprintf("kept-%d", i+1) || trace.RuntimeID != shell || trace.WriteResult != "accepted" ||
			trace.Source != "user" || trace.ByteCount != len("do not retain me") || trace.ErrorClass != nil {
			t.Errorf("newest trace %d = %+v, want kept-%d accepted by %s with its byte count", i, trace, i+1, shell)
		}
	}
	if !slices.Contains(snapshot.WarningCodes, "pty_backend_unsupported") {
		t.Errorf("the snapshot's warning codes = %v, want the daemon's pty_backend_unsupported", snapshot.WarningCodes)
	}
	if len(snapshot.Runtimes) != 1 || snapshot.Runtimes[0].RuntimeID != shell || !protocol.Deref(snapshot.Runtimes[0].Running) {
		t.Errorf("the snapshot's runtimes = %+v, want only the running %s", snapshot.Runtimes, shell)
	}
	for _, leaked := range []string{"do not retain me", "lost secret", "not found", "not available in this build"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("the snapshot carries %q", leaked)
		}
	}
}

func TestASupportSnapshotForANamedEndpointIsSentToThatEndpoint(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	refuseSSH(t)
	w := newWorld(t)
	app := w.App()
	remote := addEndpoint(t, app, "gpu-box", "user@example", nil)

	for endpoint, want := range map[string]string{
		remote:    "endpoint not connected: " + remote,
		"missing": "endpoint not found: missing",
	} {
		app.Send(protocol.SupportSnapshotMessage{Cmd: protocol.CmdSupportSnapshot, RequestID: endpoint, EndpointID: protocol.Ptr(endpoint)})
		refused := testworld.Await(app, protocol.EventCommandError, func(m protocol.CommandErrorMessage) bool {
			return protocol.Deref(m.Cmd) == protocol.CmdSupportSnapshot && strings.Contains(m.Error, endpoint)
		})
		if refused.Error != want {
			t.Errorf("a snapshot for endpoint %s was refused with %q, want %q", endpoint, refused.Error, want)
		}
	}
	for _, e := range app.Received() {
		if e.Event == protocol.EventSupportSnapshotResult {
			t.Fatalf("the daemon answered a snapshot meant for another endpoint itself")
		}
	}
}

func supportSnapshotRequest(app *testworld.Peer, requestID string, runtimes []string) (protocol.SupportSnapshotResultMessage, json.RawMessage) {
	app.T.Helper()
	raw := testworld.Request(app, protocol.SupportSnapshotMessage{Cmd: protocol.CmdSupportSnapshot, RequestID: requestID, RuntimeIds: runtimes},
		protocol.EventSupportSnapshotResult, func(raw json.RawMessage) bool {
			var result protocol.SupportSnapshotResultMessage
			return json.Unmarshal(raw, &result) == nil && result.RequestID == requestID
		})
	var result protocol.SupportSnapshotResultMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		app.T.Fatalf("decode the support snapshot: %v", err)
	}
	return result, raw
}
