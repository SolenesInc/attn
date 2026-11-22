package plugin

import (
	"encoding/json"

	"github.com/victorarias/attn/internal/protocol"
)

type launchInstructions struct {
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	ProfileID string `json:"profile_id,omitempty"`
}

type spawnParams struct {
	Executable      string              `json:"executable,omitempty"`
	Agent           string              `json:"agent"`
	TerminalID      protocol.TerminalID `json:"session_id"`
	RunID           string              `json:"run_id"`
	CWD             string              `json:"cwd"`
	Label           string              `json:"label,omitempty"`
	Yolo            bool                `json:"yolo,omitempty"`
	Model           string              `json:"model,omitempty"`
	Effort          string              `json:"effort,omitempty"`
	ExecutionTarget string              `json:"execution_target,omitempty"`
	ConversationID  string              `json:"conversation_id,omitempty"`
	InitialPrompt   string              `json:"initial_prompt,omitempty"`
	Metadata        json.RawMessage     `json:"metadata,omitempty"`
	ResumeSessionID string              `json:"resume_session_id,omitempty"`
	Instructions    *launchInstructions `json:"instructions,omitempty"`
	AgentTools      *agentTools         `json:"agent_tools,omitempty"`
}

type agentTools struct {
	URL         string `json:"url,omitempty"`
	Credential  string `json:"credential,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description"`
}

type spawnResult struct {
	Argv []string          `json:"argv"`
	Env  map[string]string `json:"env,omitempty"`
	CWD  string            `json:"cwd,omitempty"`
}

type resumeConversation struct {
	Agent           string `json:"agent"`
	CWD             string `json:"cwd"`
	ResumeSessionID string `json:"resume_session_id"`
}

type resumeAvailableParams struct {
	Conversations []resumeConversation `json:"conversations"`
}

type resumeAvailableResult struct {
	Availability []struct {
		resumeConversation
		Available bool   `json:"available"`
		Reason    string `json:"reason,omitempty"`
	} `json:"availability"`
}

type sessionClosedParams struct {
	TerminalID protocol.TerminalID `json:"session_id"`
	RunID      string              `json:"run_id"`
	Reason     string              `json:"reason"`
	ExitCode   *int                `json:"exit_code,omitempty"`
	Signal     string              `json:"signal,omitempty"`
}

type sessionClosedResult struct {
	OK bool `json:"ok"`
}

type deliverParams struct {
	TerminalID     protocol.TerminalID `json:"session_id"`
	RunID          string              `json:"run_id"`
	InputID        string              `json:"input_id"`
	Text           string              `json:"text"`
	Voice          string              `json:"voice,omitempty"`
	ConversationID string              `json:"conversation_id,omitempty"`
}

type deliverResult struct {
	OK     bool   `json:"ok"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}
