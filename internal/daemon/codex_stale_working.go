package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

// A healthy TUI and its server agree within 103 ms of a turn starting and 30 ms of it ending, measured on Codex 0.164.0.
const codexWorkingCheckAfter = 5 * time.Second

func (r *codexShared) checkWorkingSoon(sessionID protocol.SessionID) {
	if !r.launchedShared(sessionID) {
		return
	}
	r.host.Clock().AfterFunc(codexshared.WorkingCheckTimer, codexWorkingCheckAfter, func() {
		r.host.Life().Go("codexWorkingCheck", func() { r.checkWorking(sessionID) })
	})
}

func (r *codexShared) checkWorking(sessionID protocol.SessionID) {
	session := r.host.Sessions().Get(sessionID)
	if session == nil || !r.terminalClaimsWorking(sessionID) {
		return
	}
	s := r.server(session.ProfileID)
	client := s.client()
	if client == nil {
		return
	}
	conversation := r.conversation(sessionID)
	idle, err := r.idleWithoutBackgroundTerminals(client, conversation)
	if err != nil {
		r.host.Logf("shared Codex: checking the working terminal of session %s: %v", sessionID, err)
		return
	}
	if !idle {
		return
	}
	s.events.run(r.host, func() {
		if r.terminalClaimsWorking(sessionID) && !r.mcpServerStarting(conversation) {
			r.report(sessionID, session, harness.TurnEnded)
		}
	})
}

func (r *codexShared) terminalClaimsWorking(sessionID protocol.SessionID) bool {
	evidence, _ := r.host.Sessions().Evidence(sessionID)
	return evidence.TerminalClaimsWorking
}

func (r *codexShared) followsServer(sessionID protocol.SessionID) bool {
	evidence, _ := r.host.Sessions().Evidence(sessionID)
	return evidence.FollowsServer
}

func (r *codexShared) idleWithoutBackgroundTerminals(client *codexshared.Client, conversation string) (bool, error) {
	var read struct {
		Thread struct {
			Status codexThreadStatus `json:"status"`
		} `json:"thread"`
	}
	if err := r.callControl(client, "thread/read", conversation, &read); err != nil {
		return false, err
	}
	if read.Thread.Status.Type != "idle" {
		return false, nil
	}
	var terminals struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := r.callControl(client, "thread/backgroundTerminals/list", conversation, &terminals); err != nil {
		return false, err
	}
	return len(terminals.Data) == 0, nil
}

func (r *codexShared) callControl(client *codexshared.Client, method, conversation string, result any) error {
	ctx, cancel := context.WithTimeout(r.host.Life().Context(), codexServerCallLimit)
	defer cancel()
	raw, err := client.Call(ctx, method, map[string]any{"threadId": conversation})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}
