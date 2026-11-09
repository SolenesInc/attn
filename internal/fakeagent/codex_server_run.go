package fakeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

type CodexServer struct {
	t         testing.TB
	fake      *fake
	codexHome string
}

func (s *CodexServer) AwaitStopped() {
	s.t.Helper()
	select {
	case <-s.fake.peer.done:
	case <-time.After(HangGuard):
		s.t.Fatalf("Codex app-server (pid %d) still runs %s after nothing needs it", s.fake.Pid, HangGuard)
	}
}

func (s *CodexServer) LoseSocketLink() {
	s.t.Helper()
	path := codexListenPath(s.fake.Argv)
	folder, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		s.t.Fatalf("resolve the socket folder of Codex app-server (pid %d): %v", s.fake.Pid, err)
	}
	temporaryRoot, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		s.t.Fatalf("resolve /tmp: %v", err)
	}
	daemonDir := filepath.Join(temporaryRoot, fmt.Sprintf("codex-daemon-%d", os.Geteuid()))
	if err := os.MkdirAll(daemonDir, 0o700); err != nil {
		s.t.Fatalf("create Codex's socket directory: %v", err)
	}
	sum := sha256.Sum256([]byte(filepath.Join(folder, filepath.Base(path))))
	socket := filepath.Join(daemonDir, hex.EncodeToString(sum[:]))
	s.t.Cleanup(func() { _ = os.Remove(socket) })
	if err := os.Rename(path, socket); err != nil {
		s.t.Fatalf("move the socket of Codex app-server (pid %d): %v", s.fake.Pid, err)
	}
}

func (s *CodexServer) LoseSocketFolder() {
	s.t.Helper()
	if err := os.RemoveAll(filepath.Dir(codexListenPath(s.fake.Argv))); err != nil {
		s.t.Fatalf("remove the socket folder of Codex app-server (pid %d): %v", s.fake.Pid, err)
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

func (s *CodexServer) Subagent(conversation, text string) string {
	s.t.Helper()
	var spawned promptedResult
	s.call(methodSubagent, serverThreadParams{ThreadID: conversation, Text: text}, &spawned)
	return spawned.ConversationID
}

func (s *CodexServer) Halt(conversation string) {
	s.t.Helper()
	s.call(methodHalt, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *CodexServer) AwaitName(conversation, name string) {
	s.t.Helper()
	s.call(methodThreadName, serverThreadParams{ThreadID: conversation, Text: name}, nil)
}

func (s *CodexServer) AwaitArchived(conversation string) string {
	s.t.Helper()
	archive := filepath.Join(s.codexHome, "archived_sessions")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		s.t.Fatalf("create Codex's archive: %v", err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.t.Fatalf("watch Codex's archive: %v", err)
	}
	defer watcher.Close()
	if err := watcher.Add(archive); err != nil {
		s.t.Fatalf("watch Codex's archive: %v", err)
	}
	deadline := time.After(HangGuard)
	for {
		if name := codexArchivedRollout(s.codexHome, conversation); name != "" {
			return filepath.Join(archive, name)
		}
		select {
		case <-watcher.Events:
		case err := <-watcher.Errors:
			s.t.Fatalf("watch Codex's archive: %v", err)
		case <-deadline:
			s.t.Fatalf("conversation %s is not archived after %s", conversation, HangGuard)
		}
	}
}

func (s *CodexServer) AskApproval(conversation string) {
	s.t.Helper()
	s.call(methodAskApproval, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *CodexServer) Work(conversation string) {
	s.t.Helper()
	s.call(methodWork, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *CodexServer) StartBackgroundTerminal(conversation string) {
	s.t.Helper()
	s.call(methodStartTerminal, serverThreadParams{ThreadID: conversation}, nil)
}

func (s *CodexServer) AwaitBackgroundTerminalsListed(conversation string) {
	s.t.Helper()
	s.call(methodAwaitListed, serverThreadParams{ThreadID: conversation}, nil)
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

func (s *CodexServer) ReportMCPStartup(conversation, server, status string) {
	s.t.Helper()
	s.call(methodMCPStartup, serverThreadParams{ThreadID: conversation, Server: server, Text: status}, nil)
}

func (s *CodexServer) Drain() {
	s.t.Helper()
	s.call(methodDrain, serverThreadParams{}, nil)
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

func (s *CodexServer) MemoryConfig(conversation string) map[string]any {
	s.t.Helper()
	var loaded map[string]any
	s.call(methodMemoryConfig, serverThreadParams{ThreadID: conversation}, &loaded)
	return loaded
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

func (s *CodexServer) SpawnSubagent(conversation string) string {
	s.t.Helper()
	var spawned promptedResult
	s.call(methodSpawnSubagent, serverThreadParams{ThreadID: conversation}, &spawned)
	return spawned.ConversationID
}

func (s *CodexServer) call(method string, params, result any) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := s.fake.peer.call(ctx, method, params, result); err != nil {
		s.t.Fatalf("Codex app-server %s: %v", method, err)
	}
}

func (s *CodexServer) CloseThread(conversation string) {
	s.t.Helper()
	s.call(methodCloseThread, serverThreadParams{ThreadID: conversation}, nil)
}
