package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const sessionInstructionsTimeout = 45 * time.Second

func (h codexHarness) runSessionInstructions(ctx context.Context, c command.Caller, a command.SessionInstructionsArgs) (any, error) {
	question := strings.TrimSpace(a.Question)
	if question == "" {
		return nil, command.Usage("{question} is required")
	}
	target := protocol.SessionID(strings.TrimSpace(a.ID))
	if profile, ok := h.host.Sessions().LedgerProfile(target); ok && !c.Sees(profile) {
		return nil, command.Refuse("session_not_found", "%s", sessionInstructionsErrorMessage("session_not_found"))
	}
	ctx, cancel := context.WithTimeout(ctx, sessionInstructionsTimeout)
	defer cancel()
	result, err := h.askSessionInstructions(ctx, target, question)
	if err != nil {
		code := "model_unavailable"
		var sessionErr *Error
		if errors.As(err, &sessionErr) {
			code = sessionErr.Code
		}
		return nil, command.Refuse(code, "%s", sessionInstructionsErrorMessage(code))
	}
	return result, nil
}

func (h codexHarness) askSessionInstructions(ctx context.Context, target protocol.SessionID, question string) (*protocol.SessionInstructionsResult, error) {
	if protocol.TrimID(target) == "" {
		return nil, &Error{Code: "session_not_found", Message: "The target session was not found"}
	}
	sessions := h.host.Sessions()
	session := sessions.Get(target)
	if session == nil {
		return nil, &Error{Code: "session_not_found", Message: "The target session was not found"}
	}
	if session.Agent != codexID {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	resumeID, path := sessions.StoredConversation(target)
	resumeID = strings.TrimSpace(resumeID)
	if resumeID == "" {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	path = strings.TrimSpace(path)
	if _, err := os.Stat(path); path == "" || err != nil {
		path = strings.TrimSpace(transcript.FindCodexTranscriptForResume(resumeID))
	}
	if path == "" {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	raw, err := readSnapshot(path, defaultSnapshotMax)
	if err != nil {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	turns, err := projectCodexConversation(raw)
	if err != nil || len(turns) == 0 {
		return nil, &Error{Code: "transcript_unavailable", Message: "The target transcript is unavailable"}
	}
	if conversationChars(turns) > defaultConversationMax {
		return nil, &Error{Code: "conversation_too_large", Message: "The target conversation is too large to inspect"}
	}
	fingerprint := "sha256:" + hash(raw)

	first, err := h.runInstructionsModel(ctx, ModelRequest{Question: question, Conversation: turns, Effort: lowEffort})
	if err != nil && !errors.Is(err, ErrInvalidResponse) {
		return nil, &Error{Code: "model_unavailable", Message: "The session-instructions model is unavailable"}
	}
	firstErrs, _ := validateCandidate(first, question, turns)
	if errors.Is(err, ErrInvalidResponse) {
		firstErrs = []string{"the first response was not valid structured output"}
	}
	if len(firstErrs) == 0 {
		return resultFromCandidate(first, target, path, fingerprint, lowEffort, turns), nil
	}

	retry, err := h.runInstructionsModel(ctx, ModelRequest{
		Question:                 question,
		Conversation:             turns,
		Effort:                   mediumEffort,
		PreviousValidationErrors: firstErrs,
	})
	if err != nil && !errors.Is(err, ErrInvalidResponse) {
		return nil, &Error{Code: "model_unavailable", Message: "The session-instructions model is unavailable"}
	}
	retryErrs, retryMalformed := validateCandidate(retry, question, turns)
	if errors.Is(err, ErrInvalidResponse) {
		return nil, &Error{Code: "invalid_response", Message: "The model did not return valid structured output"}
	}
	if len(retryErrs) > 0 {
		if retryMalformed {
			return nil, &Error{Code: "invalid_response", Message: "The model did not return valid structured output"}
		}
		return nil, &Error{Code: "invalid_evidence", Message: "The model did not return verifiable evidence"}
	}
	return resultFromCandidate(retry, target, path, fingerprint, mediumEffort, turns), nil
}

func (h codexHarness) runInstructionsModel(ctx context.Context, request ModelRequest) (ModelAnswer, error) {
	if h.host.HeadlessRefused("session_instructions") {
		return ModelAnswer{}, headless.Refusal("session_instructions")
	}
	result, err := h.Run(ctx, harness.OneShotRequest{
		Model:  ModelName,
		Effort: request.Effort,
		Input:  Prompt(request),
		Schema: json.RawMessage(AnswerSchema),
	})
	if err != nil {
		diagnostic := strings.TrimSpace(result.Diagnostics)
		if diagnostic == "" {
			diagnostic = "unknown headless failure"
		}
		h.host.Logf("session instructions model failed: %s", diagnostic)
		return ModelAnswer{}, err
	}
	return ParseModelAnswer(result.Text)
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
