package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioninstructions"
)

const sessionInstructionsTimeout = 45 * time.Second

func (d *Daemon) runSessionInstructions(ctx context.Context, c command.Caller, a command.SessionInstructionsArgs) (any, error) {
	if strings.TrimSpace(a.Question) == "" {
		return nil, command.Usage("{question} is required")
	}
	if entry := d.store.SessionLedgerEntry(protocol.SessionID(strings.TrimSpace(a.ID))); entry != nil {
		if !c.Sees(entry.ProfileID) {
			return nil, command.Refuse("session_not_found", "%s", SessionInstructionsErrorMessage("session_not_found"))
		}
	}
	service := sessioninstructions.Service{
		Store:  d.store,
		Finder: agentdriver.Get("codex").(agentdriver.TranscriptFinder),
		Model:  daemonSessionInstructionsModel{daemon: d},
	}
	ctx, cancel := context.WithTimeout(ctx, sessionInstructionsTimeout)
	defer cancel()
	result, err := service.Ask(ctx, sessioninstructions.Request{TargetSessionID: protocol.SessionID(strings.TrimSpace(a.ID)), Question: strings.TrimSpace(a.Question)})
	if err != nil {
		code := "model_unavailable"
		var sessionErr *sessioninstructions.Error
		if errors.As(err, &sessionErr) {
			code = sessionErr.Code
		}
		return nil, command.Refuse(code, "%s", SessionInstructionsErrorMessage(code))
	}
	return result, nil
}

type daemonSessionInstructionsModel struct{ daemon *Daemon }

func (m daemonSessionInstructionsModel) Run(ctx context.Context, request sessioninstructions.ModelRequest) (sessioninstructions.ModelAnswer, error) {
	if m.daemon.headlessTaskRefused("session_instructions") {
		return sessioninstructions.ModelAnswer{}, headless.Refusal("session_instructions")
	}
	driver := agentdriver.Get("codex")
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		m.daemon.logf("session instructions model failed: Codex headless provider unavailable")
		return sessioninstructions.ModelAnswer{}, errors.New("codex headless provider unavailable")
	}
	executable, err := m.daemon.machineAgentExecutable(driver.Name(), "")
	if err != nil {
		m.daemon.logf("session instructions model failed: Codex executable unavailable")
		return sessioninstructions.ModelAnswer{}, fmt.Errorf("resolve luna executable: %w", err)
	}
	workDir, err := os.MkdirTemp("", "attn-session-instructions-*")
	if err != nil {
		m.daemon.logf("session instructions model failed: scratch directory unavailable")
		return sessioninstructions.ModelAnswer{}, err
	}
	defer os.RemoveAll(workDir)
	result, err := provider.RunHeadlessTask(ctx, agentdriver.HeadlessTaskRequest{
		Executable:      executable,
		Model:           sessioninstructions.ModelName,
		ReasoningEffort: request.Effort,
		Prompt:          sessioninstructions.Prompt(request),
		WorkDir:         workDir,
		DisableTools:    true,
		OutputSchema:    json.RawMessage(sessioninstructions.AnswerSchema),
	})
	if err != nil {
		diagnostic := strings.TrimSpace(result.Diagnostics)
		if diagnostic == "" {
			diagnostic = "unknown headless failure"
		}
		m.daemon.logf("session instructions model failed: %s", diagnostic)
		return sessioninstructions.ModelAnswer{}, err
	}
	return sessioninstructions.ParseModelAnswer(result.Text)
}

func SessionInstructionsErrorMessage(code string) string {
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
