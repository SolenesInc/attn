package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseSessionReopenArgs(t *testing.T) {
	parsed, err := parseSessionReopenArgs([]string{"sess-1", "--action", "start_fresh_elsewhere", "--cwd", "/tmp/here"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.target != "sess-1" || parsed.action != "start_fresh_elsewhere" || parsed.cwd != "/tmp/here" {
		t.Fatalf("parsed = %+v", parsed)
	}

	bad := [][]string{
		{},
		{"--action", "reopen"},
		{"sess-1", "sess-2"},
		{"sess-1", "--action", "recreate_worktree"},
	}
	for _, args := range bad {
		if _, err := parseSessionReopenArgs(args); err == nil {
			t.Errorf("parseSessionReopenArgs(%v) accepted arguments it should refuse", args)
		}
	}
}

func TestAnUnknownActionNamesTheOnesThatExist(t *testing.T) {
	_, err := parseSessionReopenArgs([]string{"sess-1", "--action", "recreate_worktree"})
	if err == nil {
		t.Fatal("an unknown action was accepted")
	}
	for _, action := range sessionReopenActions {
		if !strings.Contains(err.Error(), string(action)) {
			t.Errorf("error %q does not name the action %s", err, action)
		}
	}
}

func TestSessionReopenPrintsWhatItDid(t *testing.T) {
	var out bytes.Buffer
	fprintSessionReopen(&out, &protocol.SessionReopenResult{
		SessionID:       "sess-1",
		WorkspaceID:     "workspace-sess-1",
		Directory:       "/tmp/repo--feat",
		Action:          protocol.SessionReopenActionRecreateWorktreeAndReopen,
		WorktreeCreated: protocol.Ptr("/tmp/repo--feat"),
	})
	printed := out.String()
	if !strings.Contains(printed, "recreated worktree /tmp/repo--feat") {
		t.Errorf("output does not report the worktree it recreated:\n%s", printed)
	}
	if !strings.Contains(printed, "sess-1 reopened in /tmp/repo--feat") {
		t.Errorf("output does not report the reopen:\n%s", printed)
	}

	out.Reset()
	fprintSessionReopen(&out, &protocol.SessionReopenResult{
		SessionID:      "sess-1",
		WorkspaceID:    "workspace-sess-1",
		Directory:      "/tmp/repo",
		Action:         protocol.SessionReopenActionReopen,
		AlreadyRunning: protocol.Ptr(true),
	})
	if printed := out.String(); !strings.Contains(printed, "already running") {
		t.Errorf("a live session must be reported as running, not reopened:\n%s", printed)
	}
}
