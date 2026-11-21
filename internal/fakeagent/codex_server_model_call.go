package fakeagent

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
)

type codexModelCall struct {
	model, instructions string
	config              map[string]any
	conn                *codexServerConn
}

func isCodexModelCall(p codexServerParams) bool {
	return p.Ephemeral && p.Config["features.shell_tool"] == false
}

func (s *codexAppServer) startModelCall(conn *codexServerConn, p codexServerParams) (any, func(), error) {
	id := uuid.NewString()
	s.mu.Lock()
	if s.modelCalls == nil {
		s.modelCalls = map[string]*codexModelCall{}
	}
	s.modelCalls[id] = &codexModelCall{model: p.Model, instructions: p.BaseInstructions, config: p.Config, conn: conn}
	s.mu.Unlock()
	return map[string]any{"thread": map[string]any{"id": id, "ephemeral": true}}, nil, nil
}

func (s *codexAppServer) modelCall(id string) *codexModelCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelCalls[id]
}

func (s *codexAppServer) modelCallTurn(call *codexModelCall, p codexServerParams) (any, func(), error) {
	asked := headlessTask{Harness: Codex, Model: call.model, Effort: p.Effort, launchedAs: launchedAs{Argv: os.Args, Env: os.Environ()}}
	asked.Refusal = codexModelCallToolsLeft(s.cfg.CodexHome, call.config)
	for _, input := range p.Input {
		asked.Prompt = joinSystemPrompt(call.instructions, input.Text)
	}
	turn := uuid.NewString()
	return map[string]any{"turn": map[string]string{"id": turn, "status": "inProgress"}}, func() {
		go func() {
			var answer headlessAnswer
			if err := s.control.call(context.Background(), methodHeadless, asked, &answer); err != nil {
				return
			}
			status, turnError := "completed", any(nil)
			if answer.Failure != "" {
				status, turnError = "failed", map[string]string{"message": answer.Failure}
			}
			call.conn.send(notification("item/completed", map[string]any{"threadId": p.ThreadID, "item": map[string]string{"type": "agentMessage", "text": answer.Text}}))
			call.conn.send(notification("thread/tokenUsage/updated", map[string]any{"threadId": p.ThreadID, "tokenUsage": map[string]any{"last": map[string]int{"inputTokens": 661, "outputTokens": 12}}}))
			call.conn.send(notification("turn/completed", map[string]any{"threadId": p.ThreadID, "turn": map[string]any{"id": turn, "status": status, "error": turnError}}))
		}()
	}, nil
}

func codexModelCallToolsLeft(codexHome string, config map[string]any) string {
	user, err := readCodexConfig(codexHome)
	if err != nil {
		return err.Error()
	}
	servers, _ := user["mcp_servers"].(map[string]any)
	for name := range servers {
		if config["mcp_servers."+name+".enabled"] != false {
			return fmt.Sprintf("a model call on the shared app-server kept the user's MCP server %s", name)
		}
	}
	return ""
}
