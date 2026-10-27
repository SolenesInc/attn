package main_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/testworld"
)

func TestBufferedEscapeSequencesKeepOutputEventsBoundedAndTheScreenExact(t *testing.T) {
	for _, sequence := range []struct {
		name, prefix, suffix string
		bytes                int
	}{
		{"osc133", "\x1b]133;C;", "\x07", (16 << 20) - 32},
		{"kitty-overflow", "\x1b_G", "\x1b\\", (72 << 20) + 2*pty.OutputEventMaxBytes},
	} {
		t.Run(sequence.name, func(t *testing.T) {
			for _, backend := range []string{"embedded", "worker", "shared"} {
				t.Run(backend, func(t *testing.T) {
					s := testworld.NewStack(t)
					s.Vars = append(s.Vars, "ATTN_PTY_BACKEND="+backend, "SHELL=/bin/bash")
					if backend == "shared" {
						host := os.Getenv("ATTN_TEST_PTY_HOST")
						if host == "" {
							t.Skip("set ATTN_TEST_PTY_HOST to verify the shared host output bound")
						}
						s.Vars = append(s.Vars, "ATTN_PTY_HOST_BINARY="+host)
					}
					s.Start()
					app := s.App()
					shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
					terminal := app.Terminal(shell)
					attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
					if attached.Snapshot == nil {
						t.Fatal("attach carried no screen snapshot")
					}

					snapshot, err := base64.StdEncoding.DecodeString(attached.Snapshot.SnapshotB64)
					if err != nil {
						t.Fatal(err)
					}
					mirror, err := ghosttyvt.Restore(snapshot, ghosttyvt.Options{})
					if err != nil {
						t.Fatal(err)
					}
					defer func() { mirror.Close() }()
					payload := bytes.NewBufferString("\x1b[2J\x1b[Hbefore\r\n" + sequence.prefix)
					payload.WriteString(strings.Repeat("x", sequence.bytes))
					payload.WriteString(sequence.suffix + "\r\n\x1b[32moutput-finished\x1b[0m\x1b]2;output-finished\x07")
					file := s.Path("escape-output")
					if err := os.WriteFile(file, payload.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
					app.TypeLine(shell, "cat '"+file+"'; read after_output")
					testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Title) == "output-finished" })
					largest, events, total := 0, 0, 0
					lastSeq := uint32(protocol.Deref(attached.LastSeq))
					recoveries := 0
					for !strings.Contains(mirror.ViewportText(), "output-finished") {
						if app.AwaitOutputOrDesync(terminal, func(seq uint32, data []byte) bool {
							if seq <= uint32(protocol.Deref(attached.LastSeq)) {
								return false
							}
							if seq <= lastSeq {
								return false
							}
							lastSeq = seq
							largest = max(largest, len(data))
							events++
							total += len(data)
							mirror.Write(data)
							return strings.Contains(mirror.ViewportText(), "output-finished")
						}) {
							break
						}
						attached = testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
						if attached.Snapshot == nil {
							t.Fatal("reattach carried no snapshot")
						}
						snapshot, err := base64.StdEncoding.DecodeString(attached.Snapshot.SnapshotB64)
						if err != nil {
							t.Fatal(err)
						}
						mirror.Close()
						mirror, err = ghosttyvt.Restore(snapshot, ghosttyvt.Options{})
						if err != nil {
							t.Fatal(err)
						}
						lastSeq = uint32(protocol.Deref(attached.LastSeq))
						recoveries++
					}
					if events == 0 {
						t.Fatal("received no output events")
					}

					if largest > pty.OutputEventMaxBytes {
						t.Errorf("output event bytes=%d exceed bound=%d", largest, pty.OutputEventMaxBytes)
					}
					fresh := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)}, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
					if fresh.Snapshot == nil {
						t.Fatal("fresh attach carried no snapshot")
					}
					freshSnapshot, err := base64.StdEncoding.DecodeString(fresh.Snapshot.SnapshotB64)
					if err != nil {
						t.Fatal(err)
					}
					expected, err := ghosttyvt.Restore(freshSnapshot, ghosttyvt.Options{})
					if err != nil {
						t.Fatal(err)
					}
					defer expected.Close()
					if got, want := mirror.SerializeViewport().VTDump, expected.SerializeViewport().VTDump; !bytes.Equal(got, want) {
						t.Errorf("streamed screen=%q, snapshot=%q", got, want)
					}
					x, y := mirror.CursorPos()
					wantX, wantY := expected.CursorPos()
					if x != wantX || y != wantY {
						t.Errorf("streamed cursor=%d,%d snapshot=%d,%d", x, y, wantX, wantY)
					}

					t.Logf("input_bytes=%d output_bytes=%d events=%d largest_event=%d recoveries=%d screen_matches_snapshot=true", payload.Len(), total, events, largest, recoveries)
					app.TypeLine(shell, "")
					app.TypeLine(shell, "exit")
					testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == shell })
				})
			}
		})
	}
}
