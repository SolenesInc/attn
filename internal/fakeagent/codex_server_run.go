package fakeagent

import (
	"context"
	"os"
	"testing"
	"time"
)

type CodexServer struct {
	t    testing.TB
	fake *fake
}

func (s *CodexServer) AwaitStopped() {
	s.t.Helper()
	select {
	case <-s.fake.peer.done:
	case <-time.After(HangGuard):
		s.t.Fatalf("Codex app-server (pid %d) still runs %s after nothing needs it", s.fake.Pid, HangGuard)
	}
}

func (s *CodexServer) ListenElsewhere() {
	s.t.Helper()
	path := codexListenPath(s.fake.Argv)
	if err := os.Rename(path, path+".elsewhere"); err != nil {
		s.t.Fatalf("move the socket of Codex app-server (pid %d): %v", s.fake.Pid, err)
	}
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

func (s *CodexServer) Halt(conversation string) {
	s.t.Helper()
	s.call(methodHalt, serverThreadParams{ThreadID: conversation}, nil)
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

func (s *CodexServer) DropControl() {
	s.t.Helper()
	s.call(methodDropControl, serverThreadParams{}, nil)
}

func (s *CodexServer) CompactLimit(conversation string) string {
	s.t.Helper()
	var loaded promptedResult
	s.call(methodCompactLimit, serverThreadParams{ThreadID: conversation}, &loaded)
	return loaded.Text
}

func (s *CodexServer) Permissions(conversation string) CodexPermissions {
	s.t.Helper()
	var loaded CodexPermissions
	s.call(methodPermissions, serverThreadParams{ThreadID: conversation}, &loaded)
	return loaded
}

type workspaceRootsResult struct {
	Roots []string `json:"roots"`
}

func (s *CodexServer) WorkspaceRoots(conversation string) []string {
	s.t.Helper()
	var loaded workspaceRootsResult
	s.call(methodWorkspaceRoots, serverThreadParams{ThreadID: conversation}, &loaded)
	return loaded.Roots
}

func (s *CodexServer) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := s.fake.peer.call(ctx, method, params, result); err != nil {
		s.t.Fatalf("Codex app-server %s: %v", method, err)
	}
}
