package main

import "testing"

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
