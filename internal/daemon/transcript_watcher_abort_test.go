package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/toolhome"
)

type haltedTurnCase struct {
	name  string
	agent protocol.SessionAgent
	seed  func(t *testing.T, home, dir, sessionID string) string
	abort func(at time.Time) string
}

func haltedTurnCases() []haltedTurnCase {
	return []haltedTurnCase{
		{
			name:  "claude",
			agent: protocol.SessionAgentClaude,
			seed: func(t *testing.T, home, dir, sessionID string) string {
				projects := filepath.Join(home, ".claude", "projects", "a-project")
				if err := os.MkdirAll(projects, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				path := filepath.Join(projects, sessionID+".jsonl")
				writeLine(t, path, `{"type":"user","message":{"role":"user","content":"write an essay"}}`)
				return path
			},
			abort: func(at time.Time) string {
				return fmt.Sprintf(
					`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"interruptedMessageId":"msg_011Cdcnp6M5nmsdZuTzGRmaF","timestamp":%q}`,
					at.UTC().Format(time.RFC3339Nano),
				)
			},
		},
		{
			name:  "codex",
			agent: protocol.SessionAgentCodex,
			seed: func(t *testing.T, home, dir, sessionID string) string {
				sessions := filepath.Join(home, ".codex", "sessions", "2026", "08", "01")
				if err := os.MkdirAll(sessions, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				path := filepath.Join(sessions, "rollout-"+sessionID+".jsonl")
				stamp := time.Now().UTC().Format(time.RFC3339Nano)
				writeLine(t, path, fmt.Sprintf(
					`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"cwd":%q,"timestamp":%q}}`,
					stamp, sessionID, dir, stamp,
				))
				return path
			},
			abort: func(at time.Time) string {
				return fmt.Sprintf(
					`{"timestamp":%q,"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"019fbf55-ef48","reason":"interrupted"}}`,
					at.UTC().Format(time.RFC3339Nano),
				)
			},
		},
		{
			name:  "copilot",
			agent: protocol.SessionAgentCopilot,
			seed: func(t *testing.T, home, dir, sessionID string) string {
				state := filepath.Join(home, ".copilot", "session-state", sessionID)
				if err := os.MkdirAll(state, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(state, "workspace.yaml"), []byte("cwd: "+dir+"\n"), 0o644); err != nil {
					t.Fatalf("write workspace.yaml: %v", err)
				}
				path := filepath.Join(state, "events.jsonl")
				writeLine(t, path, fmt.Sprintf(
					`{"type":"session.start","data":{"sessionId":%q,"startTime":%q}}`,
					sessionID, time.Now().UTC().Format(time.RFC3339Nano),
				))
				writeLine(t, path, `{"type":"assistant.turn_start","data":{"turnId":"0"}}`)
				return path
			},
			abort: func(at time.Time) string {
				return fmt.Sprintf(
					`{"type":"abort","timestamp":%q,"data":{"reason":"user_initiated"}}`,
					at.UTC().Format(time.RFC3339Nano),
				)
			},
		},
	}
}

func TestAHaltFromBeforeTheSessionStartedIsIgnored(t *testing.T) {
	for _, tc := range haltedTurnCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(toolhome.EnvVar, t.TempDir())
			t.Setenv("CODEX_HOME", "")
			home, _ := toolhome.Dir()

			d := newTraceDaemon(t)
			synctest.Test(t, func(t *testing.T) {
				stopDaemonBackground(t, d)
				id := "sess-replayed-halt-" + tc.name
				addCharacterizationSession(t, d, id, tc.agent, protocol.SessionStateWorking)
				session := d.store.Get(id)
				d.recordBracketEvidence(id, protocol.StateWorking)

				startedAt := time.Now()
				path := tc.seed(t, home, session.Directory, id)
				writeLine(t, path, tc.abort(startedAt.Add(-2*time.Hour)))

				d.store.SetResumeSessionID(id, id)
				d.startTranscriptWatcher(id, tc.agent, session.Directory, startedAt)
				t.Cleanup(func() { d.stopTranscriptWatcher(id) })
				requireTranscriptDiscovery(t, d, id)

				advancePolls(4)

				got, ok := d.evidenceTable().snapshot(id)
				if !ok {
					t.Fatal("no evidence recorded")
				}
				if got.LastHarnessEvent != nil && got.LastHarnessEvent.Claim == sessionstate.ClaimTurnAborted {
					t.Fatal("a halt replayed out of history settled a session that is working")
				}
				if !got.TurnOpen {
					t.Fatal("the open turn was closed by a halt from a previous session")
				}
			})
		})
	}
}

func TestAnUndatedHaltIsIgnored(t *testing.T) {
	t.Setenv(toolhome.EnvVar, t.TempDir())
	t.Setenv("CODEX_HOME", "")
	home, _ := toolhome.Dir()

	d := newTraceDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		id := "sess-undated-halt"
		addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)
		session := d.store.Get(id)
		d.recordBracketEvidence(id, protocol.StateWorking)

		projects := filepath.Join(home, ".claude", "projects", "a-project")
		if err := os.MkdirAll(projects, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		path := filepath.Join(projects, id+".jsonl")
		writeLine(t, path, `{"type":"user","message":{"role":"user","content":"write an essay"}}`)

		d.store.SetResumeSessionID(id, id)
		d.startTranscriptWatcher(id, protocol.SessionAgentClaude, session.Directory, time.Now())
		t.Cleanup(func() { d.stopTranscriptWatcher(id) })
		requireTranscriptDiscovery(t, d, id)

		writeLine(t, path, `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]},"interruptedMessageId":"msg_01"}`)
		advancePolls(4)

		got, ok := d.evidenceTable().snapshot(id)
		if !ok {
			t.Fatal("no evidence recorded")
		}
		if got.LastHarnessEvent != nil && got.LastHarnessEvent.Claim == sessionstate.ClaimTurnAborted {
			t.Fatal("an undated halt was filed as though it had just happened")
		}
		if !got.TurnOpen {
			t.Fatal("the open turn was closed by a halt that carries no date")
		}
	})
}

func writeLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func requireTranscriptDiscovery(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	advancePolls(2)
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	if watcher == nil || watcher.snapshot().Status != protocol.SessionMessageWindowStatusReady {
		t.Fatal("watcher never discovered the transcript")
	}
}

func advancePolls(n int) {
	time.Sleep(time.Duration(n) * transcriptPollInterval)
	synctest.Wait()
}
