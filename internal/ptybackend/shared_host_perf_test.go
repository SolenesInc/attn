package ptybackend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type hostResourceSample struct {
	PhysicalBytes uint64 `json:"physical_bytes"`
	ResidentBytes uint64 `json:"resident_bytes"`
	CPUNS         uint64 `json:"cpu_ns"`
	Instructions  uint64 `json:"instructions"`
	Threads       int    `json:"threads"`
}

// This opt-in experiment records process counters; time is the measured input,
// never a correctness assertion. PTY replies and stream markers are barriers.
func TestSharedHostResourceExperiment(t *testing.T) {
	probe, binary := os.Getenv("ATTN_RESOURCE_PROBE"), os.Getenv("ATTN_TEST_PTY_HOST")
	if runtime.GOOS != "darwin" || probe == "" || binary == "" {
		t.Skip("set ATTN_RESOURCE_PROBE and ATTN_TEST_PTY_HOST on macOS")
	}
	root, err := os.MkdirTemp("/tmp", "pty-perf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	listener, err := net.Listen("unix", filepath.Join(root, "ack.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-perf", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	defer backend.Shutdown(ctx)
	var hostPID int
	defer func() {
		for _, id := range backend.SessionIDs(ctx) {
			_ = backend.Remove(ctx, id)
		}
		if hostPID > 0 {
			_ = syscall.Kill(hostPID, syscall.SIGTERM)
		}
	}()
	measure := func() hostResourceSample {
		t.Helper()
		output, err := exec.Command(probe, strconv.Itoa(hostPID)).Output()
		if err != nil {
			t.Fatal(err)
		}
		var sample hostResourceSample
		if err := json.Unmarshal(output, &sample); err != nil {
			t.Fatal(err)
		}
		return sample
	}
	emit := func(fields map[string]any) {
		fields["binary"] = binary
		fields["round"] = os.Getenv("ATTN_PERF_ROUND")
		data, _ := json.Marshal(fields)
		t.Logf("PTY_RESOURCE %s", data)
	}
	var acknowledgements []*bufio.Reader
	for i := 0; i < 32; i++ {
		id := fmt.Sprintf("perf-%02d", i)
		accepted := make(chan net.Conn, 1)
		go func() {
			conn, _ := listener.Accept()
			accepted <- conn
		}()
		if err := backend.Spawn(ctx, SpawnOptions{
			ID: id, CWD: root, Agent: "perf", Cols: 80, Rows: 24,
			ExternalCommand: []string{probe, "--child", listener.Addr().String(), id},
			LoginShellEnv:   []string{"PATH=/usr/bin:/bin"},
		}); err != nil {
			t.Fatal(err)
		}
		hostPID = backend.WorkerPIDs(ctx)[id]
		select {
		case conn := <-accepted:
			if conn == nil {
				t.Fatal("fixture failed to connect")
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			reader := bufio.NewReader(conn)
			got, err := reader.ReadString('\n')
			if err != nil || strings.TrimSpace(got) != id {
				t.Fatalf("fixture handshake = %q, %v", got, err)
			}
			acknowledgements = append(acknowledgements, reader)
		case <-time.After(10 * time.Second):
			t.Fatal("fixture handshake timed out")
		}
		if i == 0 || i == 7 || i == 31 {
			emit(map[string]any{"phase": "detached", "ptys": i + 1, "host": measure()})
		}
	}
	var bytesReceived atomic.Uint64
	markers := make(chan struct{}, 8)
	streamClosed := make(chan struct{}, 32)
	for _, attached := range []bool{false, true} {
		if attached {
			for _, id := range backend.SessionIDs(ctx) {
				_, stream, err := backend.Attach(ctx, id, "perf", AttachOptions{OmitReplay: true})
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				go func() {
					var tail []byte
					for event := range stream.Events() {
						if event.Kind == OutputEventKindOutput {
							bytesReceived.Add(uint64(bytes.Count(event.Data, []byte("x"))))
							tail = append(tail, event.Data...)
							if bytes.Contains(tail, []byte("PERF_DONE")) {
								markers <- struct{}{}
							}
							if len(tail) > 8 {
								tail = tail[len(tail)-8:]
							}
						}
					}
					streamClosed <- struct{}{}
				}()
			}
		}
		emit(map[string]any{"phase": "before_work", "attached": attached, "ptys": 32, "host": measure()})
		before := measure()
		started := time.Now()
		<-time.After(2 * time.Second)
		after := measure()
		emit(map[string]any{"phase": "idle", "attached": attached, "cpu_ns": after.CPUNS - before.CPUNS, "wall_ns": time.Since(started).Nanoseconds()})
		for sample := 0; sample < 3; sample++ {
			const size = 8 << 20
			bytesBefore := bytesReceived.Load()
			before = measure()
			started = time.Now()
			if err := backend.Input(ctx, "perf-00", []byte(fmt.Sprintf("flood %d\n", size))); err != nil {
				t.Fatal(err)
			}
			ack, err := acknowledgements[0].ReadString('\n')
			if err != nil || ack != "done\n" {
				t.Fatalf("host did not process all output and answer DSR: %q %v", ack, err)
			}
			if attached {
				select {
				case <-markers:
				case <-streamClosed:
					t.Fatal("host closed an attached stream before the completion marker")
				case <-time.After(15 * time.Second):
					t.Fatal("attached stream lost the completion marker")
				}
				if got := bytesReceived.Load() - bytesBefore; got != size {
					t.Fatalf("attached output bytes = %d, want %d", got, size)
				}
			}
			after = measure()
			emit(map[string]any{"phase": "flood", "attached": attached, "sample": sample, "bytes": size, "cpu_ns": after.CPUNS - before.CPUNS, "instructions": after.Instructions - before.Instructions, "wall_ns": time.Since(started).Nanoseconds(), "host": after})
		}
	}
}
