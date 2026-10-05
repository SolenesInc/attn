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

func (s *CodexServer) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := s.fake.peer.call(ctx, method, params, result); err != nil {
		s.t.Fatalf("Codex app-server %s: %v", method, err)
	}
}
