package fakeagent

import (
	"context"
	"testing"
)

type CodexServer struct {
	t    testing.TB
	fake *fake
}

func (s *CodexServer) Prompted(conversation string) string {
	s.t.Helper()
	var prompted promptedResult
	s.call(methodPrompted, serverThreadParams{ThreadID: conversation}, &prompted)
	return prompted.Text
}

func (s *CodexServer) Reply(conversation, text string) {
	s.t.Helper()
	s.call(methodReply, serverThreadParams{ThreadID: conversation, Text: text}, nil)
}

func (s *CodexServer) AwaitName(conversation, name string) {
	s.t.Helper()
	s.call(methodThreadName, serverThreadParams{ThreadID: conversation, Text: name}, nil)
}

func (s *CodexServer) AskApproval(conversation string) {
	s.t.Helper()
	s.call(methodAskApproval, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *CodexServer) AskQuestion(conversation string) {
	s.t.Helper()
	s.call(methodAskQuestion, serverThreadParams{ThreadID: conversation}, nil)
}

// RunTool runs command as a shell tool of the conversation, in the environment Codex gives one.
func (s *CodexServer) RunTool(conversation, command string) string {
	s.t.Helper()
	var ran promptedResult
	s.call(methodRunTool, serverThreadParams{ThreadID: conversation, Text: command}, &ran)
	return ran.Text
}

func (s *CodexServer) Instructions(conversation string) string {
	s.t.Helper()
	var started promptedResult
	s.call(methodInstructions, serverThreadParams{ThreadID: conversation}, &started)
	return started.Text
}

// DropControl breaks every connection that is not a terminal's and waits for a control connection to return.
func (s *CodexServer) DropControl() {
	s.t.Helper()
	s.call(methodDropControl, serverThreadParams{}, nil)
}

// CompactLimit is the auto-compact limit the loaded conversation took from its last load, or "".
func (s *CodexServer) CompactLimit(conversation string) string {
	s.t.Helper()
	var loaded promptedResult
	s.call(methodCompactLimit, serverThreadParams{ThreadID: conversation}, &loaded)
	return loaded.Text
}

func (s *CodexServer) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := s.fake.peer.call(ctx, method, params, result); err != nil {
		s.t.Fatalf("Codex app-server %s: %v", method, err)
	}
}
