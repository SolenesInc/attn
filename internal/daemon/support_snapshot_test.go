package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSupportInputTraceDoesNotAcknowledgeEachInput(t *testing.T) {
	d := &Daemon{ptyBackend: &fakeSpawnBackend{}, done: make(chan struct{})}
	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handlePtyInput(client, &protocol.PtyInputMessage{
		ID:      "runtime-1",
		Data:    "do not retain me",
		Source:  protocol.Ptr("user"),
		TraceID: protocol.Ptr("trace-1"),
	})

	select {
	case outbound := <-client.send:
		t.Fatalf("support trace added per-input websocket traffic: %s", outbound.payload)
	default:
	}
	traces, total := d.supportInputTraceRing().snapshot()
	if total != 1 || len(traces) != 1 {
		t.Fatalf("trace count = %d/%d, want 1/1", total, len(traces))
	}
	if traces[0].TraceID != "trace-1" || traces[0].RuntimeID != "runtime-1" || traces[0].ByteCount != 16 {
		t.Fatalf("trace = %+v", traces[0])
	}
	encoded, err := json.Marshal(traces[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "do not retain me") {
		t.Fatalf("trace retained input content: %s", encoded)
	}
}

func TestSupportInputTraceClassifiesFailuresWithoutErrorText(t *testing.T) {
	d := &Daemon{}
	for name, err := range map[string]error{
		"broken_pipe":   syscall.EPIPE,
		"io_error":      syscall.EIO,
		"runtime_error": errors.New("secret backend failure"),
	} {
		d.recordSupportInputTrace(&protocol.PtyInputMessage{
			ID: "runtime-1", Data: "secret", TraceID: protocol.Ptr("trace-" + name),
		}, time.UnixMilli(42), time.Millisecond, err)
	}

	traces, _ := d.supportInputTraceRing().snapshot()
	if len(traces) != 3 {
		t.Fatalf("len(traces) = %d, want 3", len(traces))
	}
	for _, trace := range traces {
		if trace.WriteResult != "failed" || trace.ErrorClass == nil {
			t.Errorf("failure trace = %+v", trace)
		}
		encoded, err := json.Marshal(trace)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "backend failure") {
			t.Errorf("failure trace leaked content: %s", encoded)
		}
	}
}

func TestSupportInputTraceRingKeepsNewestEntriesInOrder(t *testing.T) {
	ring := &supportInputTraceRing{}
	for i := 0; i < supportInputTraceCapacity+8; i++ {
		ring.append(protocol.SupportInputTrace{TraceID: "trace", ReceivedAtUnixMs: i})
	}

	traces, total := ring.snapshot()
	if total != supportInputTraceCapacity+8 || len(traces) != supportInputTraceCapacity {
		t.Fatalf("trace count = %d/%d", total, len(traces))
	}
	if traces[0].Sequence != 9 || traces[len(traces)-1].Sequence != supportInputTraceCapacity+8 {
		t.Fatalf("retained sequence = %d..%d", traces[0].Sequence, traces[len(traces)-1].Sequence)
	}
}

func TestSupportSnapshotEchoesEndpointAndContainsOnlyAllowlistedTraceEvidence(t *testing.T) {
	d := &Daemon{daemonInstanceID: "daemon-1", presentSince: time.UnixMilli(10)}
	d.warnings = []protocol.DaemonWarning{{Code: "pty_warning", Message: "private warning details"}}
	d.recordSupportInputTrace(&protocol.PtyInputMessage{
		ID: "runtime-1", Data: "private prompt", Source: protocol.Ptr("user"), TraceID: protocol.Ptr("trace-1"),
	}, time.UnixMilli(20), 50*time.Microsecond, nil)
	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleSupportSnapshot(client, &protocol.SupportSnapshotMessage{
		RequestID: "request-1", EndpointID: protocol.Ptr("remote-1"), RuntimeIds: []string{"runtime-2"},
	})

	outbound := <-client.send
	var result protocol.SupportSnapshotResultMessage
	if err := json.Unmarshal(outbound.payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "request-1" || protocol.Deref(result.EndpointID) != "remote-1" {
		t.Fatalf("correlation = %q/%q", result.RequestID, protocol.Deref(result.EndpointID))
	}
	if len(result.InputTraces) != 1 || result.InputTraces[0].WriteResult != "accepted" {
		t.Fatalf("input traces = %+v", result.InputTraces)
	}
	if len(result.Runtimes) != 1 || result.Runtimes[0].RuntimeID != "runtime-2" {
		t.Fatalf("scoped runtimes = %+v", result.Runtimes)
	}
	if len(result.WarningCodes) != 1 || result.WarningCodes[0] != "pty_warning" {
		t.Fatalf("warning codes = %v", result.WarningCodes)
	}
	if strings.Contains(string(outbound.payload), "private prompt") || strings.Contains(string(outbound.payload), "private warning") {
		t.Fatalf("snapshot leaked private content: %s", outbound.payload)
	}
}

func TestSupportSnapshotRoutesToNamedEndpoint(t *testing.T) {
	endpoint := "remote-1"
	got := remoteCommandEndpointID(protocol.CmdSupportSnapshot, &protocol.SupportSnapshotMessage{EndpointID: &endpoint})
	if got != endpoint {
		t.Fatalf("endpoint = %q, want %q", got, endpoint)
	}
}

func BenchmarkSupportInputTrace(b *testing.B) {
	d := &Daemon{}
	message := &protocol.PtyInputMessage{
		ID: "runtime-1", Data: "x", Source: protocol.Ptr("user"), TraceID: protocol.Ptr("trace-1"),
	}
	receivedAt := time.UnixMilli(1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.recordSupportInputTrace(message, receivedAt, 50*time.Microsecond, nil)
	}
}

func BenchmarkSupportInputTraceAbsent(b *testing.B) {
	d := &Daemon{}
	message := &protocol.PtyInputMessage{ID: "runtime-1", Data: "x", Source: protocol.Ptr("user")}
	receivedAt := time.UnixMilli(1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.recordSupportInputTrace(message, receivedAt, 50*time.Microsecond, nil)
	}
}
