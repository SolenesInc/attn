package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioninstructions"
)

const sessionInstructionsTimeout = 45 * time.Second

func (h codexHarness) runSessionInstructions(ctx context.Context, c command.Caller, a command.SessionInstructionsArgs) (any, error) {
	if strings.TrimSpace(a.Question) == "" {
		return nil, command.Usage("{question} is required")
	}
	target := protocol.SessionID(strings.TrimSpace(a.ID))
	if profile, ok := h.host.Sessions().LedgerProfile(target); ok && !c.Sees(profile) {
		return nil, command.Refuse("session_not_found", "%s", sessionInstructionsErrorMessage("session_not_found"))
	}
	service := sessioninstructions.Service{
		Store:  codexInstructionsStore{sessions: h.host.Sessions()},
		Finder: h.driver().(agentdriver.TranscriptFinder),
		Model:  codexInstructionsModel{harness: h},
	}
	ctx, cancel := context.WithTimeout(ctx, sessionInstructionsTimeout)
	defer cancel()
	result, err := service.Ask(ctx, sessioninstructions.Request{TargetSessionID: target, Question: strings.TrimSpace(a.Question)})
	if err != nil {
		code := "model_unavailable"
		var sessionErr *sessioninstructions.Error
		if errors.As(err, &sessionErr) {
			code = sessionErr.Code
		}
		return nil, command.Refuse(code, "%s", sessionInstructionsErrorMessage(code))
	}
	return result, nil
}

type codexInstructionsStore struct{ sessions harness.HostSessions }

func (s codexInstructionsStore) Get(id protocol.SessionID) *protocol.Session {
	return s.sessions.Get(id)
}

func (s codexInstructionsStore) GetResumeSessionID(id protocol.SessionID) string {
	conversation, _ := s.sessions.StoredConversation(id)
	return conversation
}

func (s codexInstructionsStore) GetSessionTranscriptPath(id protocol.SessionID) string {
	_, path := s.sessions.StoredConversation(id)
	return path
}

type codexInstructionsModel struct{ harness codexHarness }

func (m codexInstructionsModel) Run(ctx context.Context, request sessioninstructions.ModelRequest) (sessioninstructions.ModelAnswer, error) {
	if m.harness.host.HeadlessRefused("session_instructions") {
		return sessioninstructions.ModelAnswer{}, headless.Refusal("session_instructions")
	}
	result, err := m.harness.Run(ctx, harness.OneShotRequest{
		Model:  sessioninstructions.ModelName,
		Effort: request.Effort,
		Input:  sessioninstructions.Prompt(request),
		Schema: json.RawMessage(sessioninstructions.AnswerSchema),
	})
	if err != nil {
		diagnostic := strings.TrimSpace(result.Diagnostics)
		if diagnostic == "" {
			diagnostic = "unknown headless failure"
		}
		m.harness.host.Logf("session instructions model failed: %s", diagnostic)
		return sessioninstructions.ModelAnswer{}, err
	}
	return sessioninstructions.ParseModelAnswer(result.Text)
}

func sessionInstructionsErrorMessage(code string) string {
	switch strings.TrimSpace(code) {
	case "session_not_found":
		return "The target session was not found"
	case "transcript_unavailable":
		return "The target transcript is unavailable"
	case "conversation_too_large":
		return "The target conversation is too large to inspect"
	case "model_unavailable":
		return "The session-instructions model is unavailable"
	case "invalid_response":
		return "The model did not return valid structured output"
	case "invalid_evidence":
		return "The model did not return verifiable evidence"
	default:
		return "Session instructions failed"
	}
}
