package ptyworker

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/pty"
)

func TestConnCtx_HandleRequest_SetThemeReachesSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	r := &Runtime{
		cfg:   Config{SessionID: "theme-rpc-sess"},
		state: "working",
		logf:  func(string, ...interface{}) {},
	}
	r.manager = pty.NewManager(r.logf)
	t.Cleanup(r.manager.Shutdown)

	if err := r.manager.Spawn(pty.SpawnOptions{
		ID:    r.cfg.SessionID,
		Agent: "shell",
		CWD:   t.TempDir(),
		Cols:  80,
		Rows:  24,
	}); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	conn := &connCtx{runtime: r, authed: true, connID: "1", sendQ: make(chan any, 4)}
	params, err := json.Marshal(SetThemeParams{Background: "#ff00ff"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	conn.handleRequest(RequestEnvelope{Type: "req", ID: "req-1", Method: MethodSetTheme, Params: params})

	select {
	case msg := <-conn.sendQ:
		res, ok := msg.(ResponseEnvelope)
		if !ok || !res.OK {
			t.Fatalf("set_theme response = %+v, want ok response", msg)
		}
	default:
		t.Fatal("handleRequest(set_theme) sent no response")
	}

	// The reply cannot be read back off the output stream: fish disables kernel echo, so it
	// lands in fish's stdin. Instead the shell runs a script whose `read` takes it explicitly.
	scriptPath := t.TempDir() + "/query.sh"
	script := "#!/bin/bash\n" +
		"printf '\\033]11;?\\007'\n" +
		"IFS= read -r -t 3 -n 25 reply\n" +
		"printf 'THEME_REPLY_START%sTHEME_REPLY_END\\n' \"$reply\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write query script: %v", err)
	}

	outputCh := make(chan []byte, 16)
	_, err = r.manager.Attach(r.cfg.SessionID, "test-observer", func(data []byte, _ uint32) bool {
		outputCh <- append([]byte(nil), data...)
		return true
	}, nil)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	t.Cleanup(func() { r.manager.Detach(r.cfg.SessionID, "test-observer") })

	if err := r.manager.Input(r.cfg.SessionID, []byte("bash "+scriptPath+"\n")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}

	wantReply := "\x1b]11;rgb:ffff/0000/ffff\x1b\\"
	deadline := time.After(5 * time.Second)
	var seen strings.Builder
	for {
		select {
		case chunk := <-outputCh:
			seen.Write(chunk)
			if idx := strings.Index(seen.String(), "THEME_REPLY_START"); idx != -1 {
				if end := strings.Index(seen.String(), "THEME_REPLY_END"); end != -1 {
					got := seen.String()[idx+len("THEME_REPLY_START") : end]
					if got != wantReply {
						t.Fatalf("OSC11 reply captured via stdin read = %q, want %q", got, wantReply)
					}
					return
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for OSC11 reply marker; got output %q", seen.String())
		}
	}
}

// The two self-stop clocks run inside synctest bubbles at their shipped lengths (45s and
// 12h). The Runtime owns no socket, PTY or child, so nothing pins the bubble to real time.

func TestRuntime_ExitedSessionCleansUpAfterTTLWithoutConnections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &Runtime{stopCh: make(chan struct{})}
		r.noteSessionExited()

		time.Sleep(exitedSessionCleanupTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
		default:
			t.Fatal("the runtime did not stop when the exit TTL elapsed")
		}
	})
}

func TestRuntime_ExitCleanupWaitsForConnectionsToClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &Runtime{stopCh: make(chan struct{})}
		r.noteConnAuthed()
		r.noteSessionExited()

		time.Sleep(4 * exitedSessionCleanupTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
			t.Fatal("runtime stopped while authed connection was still active")
		default:
		}

		r.noteConnClosed()

		time.Sleep(exitedSessionCleanupTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
		default:
			t.Fatal("the runtime did not stop once the last connection closed")
		}
	})
}

func TestRuntime_OrphanWatchStopsIdleUnownedWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &Runtime{stopCh: make(chan struct{}), orphanTTL: orphanedWorkerTTL}
		r.noteOutputActivity()
		r.armOrphanWatch()

		time.Sleep(orphanedWorkerTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
		default:
			t.Fatal("the orphan watch did not stop an idle unowned worker at its TTL")
		}
	})
}

func TestRuntime_OrphanWatchCanceledByAuthedConn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &Runtime{stopCh: make(chan struct{}), orphanTTL: orphanedWorkerTTL}
		r.noteOutputActivity()
		r.armOrphanWatch()
		r.noteConnAuthed()

		time.Sleep(4 * orphanedWorkerTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
			t.Fatal("orphan watch stopped runtime while a daemon connection was authed")
		default:
		}

		r.noteConnClosed()

		time.Sleep(orphanedWorkerTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
		default:
			t.Fatal("the orphan watch did not stop the worker after the last connection closed")
		}
	})
}

func TestRuntime_OrphanWatchDeferredByOutputActivity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &Runtime{stopCh: make(chan struct{}), orphanTTL: orphanedWorkerTTL}
		r.noteOutputActivity()
		r.armOrphanWatch()

		for i := 0; i < 4; i++ {
			r.noteOutputActivity()
			time.Sleep(orphanedWorkerTTL / 2)
			synctest.Wait()
			select {
			case <-r.stopCh:
				t.Fatal("orphan watch stopped runtime while output was still flowing")
			default:
			}
		}

		time.Sleep(orphanedWorkerTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
		default:
			t.Fatal("the orphan watch did not stop the worker once output went quiet")
		}
	})
}

func TestRuntime_OrphanWatchDisabledByZeroTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &Runtime{stopCh: make(chan struct{}), orphanTTL: 0}
		r.armOrphanWatch()

		time.Sleep(4 * orphanedWorkerTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
			t.Fatal("orphan watch fired despite zero TTL")
		default:
		}
	})
}

func TestRuntime_OrphanWatchNotArmedAfterExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origTTL := exitedSessionCleanupTTL
		exitedSessionCleanupTTL = 10 * orphanedWorkerTTL
		defer func() { exitedSessionCleanupTTL = origTTL }()

		r := &Runtime{stopCh: make(chan struct{}), orphanTTL: orphanedWorkerTTL}
		r.noteSessionExited()
		r.armOrphanWatch()

		time.Sleep(2 * orphanedWorkerTTL)
		synctest.Wait()
		select {
		case <-r.stopCh:
			t.Fatal("orphan watch fired for an exited session (exit cleanup owns that path)")
		default:
		}
	})
}

func TestConnCtx_NextReadTimeout(t *testing.T) {
	tests := []struct {
		name        string
		authed      bool
		subID       string
		watching    bool
		wantTimeout time.Duration
		wantSet     bool
	}{
		{
			name:        "hello deadline before auth",
			authed:      false,
			wantTimeout: connHelloTimeout,
			wantSet:     true,
		},
		{
			name:        "idle rpc connection uses idle deadline",
			authed:      true,
			wantTimeout: connIdleReadTimeout,
			wantSet:     true,
		},
		{
			name:        "attached stream disables read deadline",
			authed:      true,
			subID:       "sub-1",
			wantTimeout: 0,
			wantSet:     false,
		},
		{
			name:        "watch stream disables read deadline",
			authed:      true,
			watching:    true,
			wantTimeout: 0,
			wantSet:     false,
		},
	}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			ctx := &connCtx{
				authed:   tt.authed,
				subID:    tt.subID,
				watching: tt.watching,
			}
			gotTimeout, gotSet := ctx.nextReadTimeout()
			if gotTimeout != tt.wantTimeout {
				t.Fatalf("nextReadTimeout timeout = %v, want %v", gotTimeout, tt.wantTimeout)
			}
			if gotSet != tt.wantSet {
				t.Fatalf("nextReadTimeout setDeadline = %v, want %v", gotSet, tt.wantSet)
			}
		})
	}
}
