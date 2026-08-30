package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

func seedStoppingSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent) {
	t.Helper()
	now := time.Now()
	nowStr := string(protocol.NewTimestamp(now))
	d.store.Add(&protocol.Session{
		ID:             id,
		Agent:          agent,
		Label:          "marker",
		Directory:      "/tmp",
		State:          protocol.StateWorking,
		StateSince:     nowStr,
		StateUpdatedAt: nowStr,
		LastSeen:       nowStr,
	})
	d.recordBracketEvidence(id, protocol.StateWorking)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "busy", At: now})
	d.recordBracketEvidence(id, protocol.StateIdle)
	d.recordPTYEvidence(id, pty.Observation{Source: pty.SourceHeartbeat, Claim: "not_busy", At: now})
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func claudeAssistantLine(text string) string {
	return `{"type":"assistant","uuid":"turn-1","timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) +
		`","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`
}

func codexAssistantLine(text string) string {
	return `{"timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) +
		`","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}}`
}

func classifyWithMarker(t *testing.T, agent protocol.SessionAgent, line string) (*Daemon, *protocol.Session, func() string) {
	t.Helper()
	t.Setenv(headless.EnvVar, "off")
	d, readLog := newHeadlessDaemon(t)
	fake := NewFakeClassifier(protocol.StateWaitingInput)
	d.classifier = fake

	id := "sess-marker"
	seedStoppingSession(t, d, id, agent)
	d.classifySessionState(id, writeTranscript(t, line))
	d.resolveAllSessions(time.Now())

	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("a model-backed classifier ran %d times with headless tasks off", len(calls))
	}
	session := d.store.Get(id)
	if session == nil {
		t.Fatal("session missing after classify")
	}
	return d, session, readLog
}

func TestMarkerClassifierReadsTheClaudeShape(t *testing.T) {
	_, session, readLog := classifyWithMarker(
		t,
		protocol.SessionAgentClaude,
		claudeAssistantLine("Which branch should I target?\\n\\n<!-- attn:state=waiting_input -->"),
	)

	if session.State != protocol.StateWaitingInput {
		t.Fatalf("state = %s, want %s", session.State, protocol.StateWaitingInput)
	}
	if log := readLog(); !strings.Contains(log, "state marker verdict=waiting_input") {
		t.Fatalf("the daemon log does not report the marker verdict:\n%s", log)
	}
}

func TestMarkerClassifierReadsTheCodexShape(t *testing.T) {
	_, session, _ := classifyWithMarker(
		t,
		protocol.SessionAgentCodex,
		codexAssistantLine("Which branch should I target?\\n\\n<!-- attn:state=waiting_input -->"),
	)

	if session.State != protocol.StateWaitingInput {
		t.Fatalf("state = %s, want %s", session.State, protocol.StateWaitingInput)
	}
}

func TestMarkerlessTranscriptSettlesThroughHookEvidence(t *testing.T) {
	_, session, readLog := classifyWithMarker(
		t,
		protocol.SessionAgentCodex,
		codexAssistantLine("All done, the branch is pushed."),
	)

	if session.State != protocol.StateIdle {
		t.Fatalf("state = %s, want %s: hook evidence must still settle the turn", session.State, protocol.StateIdle)
	}
	if log := readLog(); !strings.Contains(log, "headless task refused (classifier)") {
		t.Fatalf("the daemon log does not report the refusal:\n%s", log)
	}
}

func TestMarkerNamingAStateNoVerdictCarriesIsLoud(t *testing.T) {
	for _, state := range []string{"pending_approval", "parked"} {
		t.Run(state, func(t *testing.T) {
			_, session, readLog := classifyWithMarker(
				t,
				protocol.SessionAgentCodex,
				codexAssistantLine("Approve this? <!-- attn:state="+state+" -->"),
			)

			if session.State != protocol.StateIdle {
				t.Fatalf("state = %s, want %s: an unusable marker settles on hook evidence like an unmarked message", session.State, protocol.StateIdle)
			}
			log := readLog()
			for _, want := range []string{`state marker unusable`, `"` + state + `"`, "waiting_input, idle"} {
				if !strings.Contains(log, want) {
					t.Fatalf("the daemon log does not name %q:\n%s", want, log)
				}
			}
		})
	}
}
