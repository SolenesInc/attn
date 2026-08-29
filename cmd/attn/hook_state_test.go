package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

type recordedConversationObservation struct {
	attnSessionID  string
	agentSessionID string
	transcriptPath string
}

type recordingConversationObserver struct {
	observations []recordedConversationObservation
}

func (r *recordingConversationObserver) ObserveAgentConversation(attnSessionID, agentSessionID, transcriptPath string) error {
	r.observations = append(r.observations, recordedConversationObservation{attnSessionID, agentSessionID, transcriptPath})
	return nil
}

func TestParseHookStateArgsCarriesThePromptSubmitMarker(t *testing.T) {
	previous := os.Args
	t.Cleanup(func() { os.Args = previous })

	t.Run("explicit session", func(t *testing.T) {
		os.Args = []string{"attn", "_hook-state", "session-1", "working", "user_prompt_submit"}
		sessionID, state, event := parseHookStateArgs()
		if sessionID != "session-1" || state != "working" || event != "user_prompt_submit" {
			t.Fatalf("parsed (%q, %q, %q)", sessionID, state, event)
		}
	})

	t.Run("session from environment", func(t *testing.T) {
		t.Setenv("ATTN_SESSION_ID", "session-env")
		os.Args = []string{"attn", "_hook-state", "working", "user_prompt_submit"}
		sessionID, state, event := parseHookStateArgs()
		if sessionID != "session-env" || state != "working" || event != "user_prompt_submit" {
			t.Fatalf("parsed (%q, %q, %q)", sessionID, state, event)
		}
	})
}

func TestUserPromptSubmitCarriesTheExactConversationBinding(t *testing.T) {
	recorder := &recordingConversationObserver{}
	want := recordedConversationObservation{
		attnSessionID:  "attn-session",
		agentSessionID: "codex-after-new",
		transcriptPath: "/codex/sessions/rollout-codex-after-new.jsonl",
	}
	observePromptConversation(recorder, want.attnSessionID, "user_prompt_submit", hookInput{
		SessionID:      want.agentSessionID,
		TranscriptPath: want.transcriptPath,
	})
	if len(recorder.observations) != 1 || recorder.observations[0] != want {
		t.Fatalf("prompt observations = %+v, want %+v", recorder.observations, want)
	}
}

func TestPromptConversationObservationRequiresAnExactPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hookEvent string
		path      string
	}{
		{name: "pathless prompt", hookEvent: "user_prompt_submit"},
		{name: "different hook", hookEvent: "stop", path: "/codex/root.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &recordingConversationObserver{}
			observePromptConversation(recorder, "attn-session", tc.hookEvent, hookInput{
				SessionID:      "ephemeral-root",
				TranscriptPath: tc.path,
			})
			if len(recorder.observations) != 0 {
				t.Fatalf("unexpected observations: %+v", recorder.observations)
			}
		})
	}
}

type recordingSessionStartClient struct {
	recordingConversationObserver
	fakeWorkspaceContextCheckoutClient
}

func TestPathlessSessionStartStillEmitsIndependentGuidance(t *testing.T) {
	t.Setenv("ATTN_WORKSPACE_CONTEXT_GUIDANCE", "")
	t.Setenv("ATTN_CHIEF_GUIDANCE", "")
	c := &recordingSessionStartClient{
		fakeWorkspaceContextCheckoutClient: fakeWorkspaceContextCheckoutClient{
			path:  "/tmp/context.md",
			ready: &protocol.SeedReadyResult{},
		},
	}

	output, contextErr, primeErr := sessionStartHookOutput(c, "attn-session", hookInput{
		SessionID: "pathless-claude-session",
	}, 1, 0)
	if contextErr != nil || primeErr != nil {
		t.Fatalf("pathless SessionStart errors = (%v, %v)", contextErr, primeErr)
	}
	if len(c.observations) != 0 {
		t.Fatalf("pathless SessionStart observations = %+v, want none", c.observations)
	}

	var decoded struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("pathless SessionStart output is not JSON: %v", err)
	}
	if decoded.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("pathless SessionStart event = %q", decoded.HookSpecificOutput.HookEventName)
	}
	context := decoded.HookSpecificOutput.AdditionalContext
	for _, want := range []string{"/tmp/context.md", "Nothing is ready now."} {
		if !strings.Contains(context, want) {
			t.Fatalf("pathless SessionStart context = %q, want it to contain %q", context, want)
		}
	}
}
