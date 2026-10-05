package fakeagent

import (
	"context"
	"testing"
)

// CodexServer plays the model behind a shared Codex app-server's conversations, including those no
// terminal shows.
type CodexServer struct {
	t    testing.TB
	fake *fake
}

// Prompted returns the next prompt attn sent the conversation through the server; Run.Prompted
// reports what was typed into a terminal.
func (s *CodexServer) Prompted(conversation string) string {
	s.t.Helper()
	var prompted promptedResult
	s.call(methodPrompted, serverThreadParams{ThreadID: conversation}, &prompted)
	return prompted.Text
}

// Reply ends the conversation's running turn with text, as Run.Reply does.
func (s *CodexServer) Reply(conversation, text string) {
	s.t.Helper()
	s.call(methodReply, serverThreadParams{ThreadID: conversation, Text: text}, nil)
}

// AwaitName returns once the conversation is named name in Codex.
func (s *CodexServer) AwaitName(conversation, name string) {
	s.t.Helper()
	s.call(methodThreadName, serverThreadParams{ThreadID: conversation, Text: name}, nil)
}

// AskApproval makes the conversation's running turn wait on a command approval.
func (s *CodexServer) AskApproval(conversation string) {
	s.t.Helper()
	s.call(methodAskApproval, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *CodexServer) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := s.fake.peer.call(ctx, method, params, result); err != nil {
		s.t.Fatalf("Codex app-server %s: %v", method, err)
	}
}
