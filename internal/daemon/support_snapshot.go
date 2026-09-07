package daemon

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const supportInputTraceCapacity = 512

type supportInputTraceRing struct {
	mu    sync.Mutex
	next  uint64
	slots [supportInputTraceCapacity]protocol.SupportInputTrace
}

func (r *supportInputTraceRing) append(entry protocol.SupportInputTrace) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	sequence := r.next
	entry.Sequence = int(sequence)
	r.slots[(sequence-1)%supportInputTraceCapacity] = entry
}

func (r *supportInputTraceRing) snapshot() ([]protocol.SupportInputTrace, uint64) {
	r.mu.Lock()
	total := r.next
	entries := make([]protocol.SupportInputTrace, 0, min(int(total), supportInputTraceCapacity))
	for i := range r.slots {
		if entry := r.slots[i]; entry.Sequence > 0 && uint64(entry.Sequence) <= total {
			entries = append(entries, entry)
		}
	}
	r.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Sequence < entries[j].Sequence })
	return entries, total
}

func (d *Daemon) supportInputTraceRing() *supportInputTraceRing {
	d.supportInputTraceOnce.Do(func() {
		d.supportInputTrace = &supportInputTraceRing{}
	})
	return d.supportInputTrace
}

func normalizeSupportTraceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == ':' {
			continue
		}
		return ""
	}
	return value
}

func supportInputSource(value string) string {
	switch strings.TrimSpace(value) {
	case "", "user":
		return "user"
	case "pointer", "response", "automation", "attach_replay":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func supportErrorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, pty.ErrSessionNotFound), errors.Is(err, os.ErrNotExist):
		return "session_not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	case errors.Is(err, syscall.EIO):
		return "io_error"
	default:
		return "runtime_error"
	}
}

func (d *Daemon) recordSupportInputTrace(msg *protocol.PtyInputMessage, receivedAt time.Time, writeDuration time.Duration, writeErr error) {
	traceID := normalizeSupportTraceID(protocol.Deref(msg.TraceID))
	if traceID == "" {
		return
	}
	entry := protocol.SupportInputTrace{
		TraceID:          traceID,
		RuntimeID:        msg.ID,
		ReceivedAtUnixMs: int(receivedAt.UnixMilli()),
		Source:           supportInputSource(protocol.Deref(msg.Source)),
		ByteCount:        len(msg.Data),
		WriteDurationUs:  int(writeDuration.Microseconds()),
		WriteResult:      "accepted",
	}
	if writeErr != nil {
		entry.WriteResult = "failed"
		entry.ErrorClass = protocol.Ptr(supportErrorClass(writeErr))
	}
	d.supportInputTraceRing().append(entry)
}

func (d *Daemon) handleSupportSnapshot(client *wsClient, msg *protocol.SupportSnapshotMessage) {
	traces, total := d.supportInputTraceRing().snapshot()
	runtimeIDs := make(map[string]struct{}, len(traces))
	for _, trace := range traces {
		runtimeIDs[trace.RuntimeID] = struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if d.ptyBackend != nil {
		for _, runtimeID := range d.ptyBackend.SessionIDs(ctx) {
			runtimeIDs[runtimeID] = struct{}{}
			if len(runtimeIDs) >= supportInputTraceCapacity {
				break
			}
		}
	}

	ids := make([]string, 0, len(runtimeIDs))
	for runtimeID := range runtimeIDs {
		ids = append(ids, runtimeID)
	}
	sort.Strings(ids)
	runtimes := make([]protocol.SupportRuntimeEvidence, 0, len(ids))
	for _, runtimeID := range ids {
		_, attached := client.attachedStreams[runtimeID]
		runtimes = append(runtimes, d.supportRuntimeEvidence(ctx, runtimeID, attached))
	}
	warnings := d.getWarnings()
	warningCodes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		warningCodes = append(warningCodes, normalizeSupportCode(warning.Code))
	}

	d.sendToClient(client, &protocol.SupportSnapshotResultMessage{
		Event:                 protocol.EventSupportSnapshotResult,
		RequestID:             msg.RequestID,
		EndpointID:            msg.EndpointID,
		ProtocolVersion:       protocol.ProtocolVersion,
		DaemonInstanceID:      d.daemonInstanceID,
		DaemonStartedAtUnixMs: int(d.presentSince.UnixMilli()),
		CapturedAtUnixMs:      int(time.Now().UnixMilli()),
		Backend:               d.ptyBackendMode(),
		WarningCodes:          warningCodes,
		TraceCapacity:         supportInputTraceCapacity,
		TraceTotal:            int(total),
		InputTraces:           traces,
		Runtimes:              runtimes,
	})
}

func (d *Daemon) supportRuntimeEvidence(ctx context.Context, runtimeID string, attached bool) protocol.SupportRuntimeEvidence {
	evidence := protocol.SupportRuntimeEvidence{
		RuntimeID: runtimeID,
		Backend:   d.ptyBackendMode(),
		Attached:  attached,
	}
	if buildProvider, ok := d.ptyBackend.(ptybackend.TerminalBuildProvider); ok {
		format, known := buildProvider.SessionTerminalBuild(runtimeID)
		evidence.TerminalBuildKnown = known
		if known {
			evidence.TerminalBuild = protocol.Ptr(format)
		}
	}
	infoProvider, ok := d.ptyBackend.(ptybackend.SessionInfoProvider)
	if !ok {
		return evidence
	}
	info, err := infoProvider.SessionInfo(ctx, runtimeID)
	if err != nil {
		evidence.InfoErrorClass = protocol.Ptr(supportErrorClass(err))
		return evidence
	}
	evidence.Running = protocol.Ptr(info.Running)
	evidence.State = protocol.Ptr(info.State)
	evidence.Cols = protocol.Ptr(int(info.Cols))
	evidence.Rows = protocol.Ptr(int(info.Rows))
	evidence.Pid = protocol.Ptr(info.PID)
	evidence.LastSeq = protocol.Ptr(int(info.LastSeq))
	return evidence
}

func normalizeSupportCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 80 {
		return "other"
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == ':' {
			continue
		}
		return "other"
	}
	return value
}
