package main_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// As in the file view's outpost test, the review is opened through
// /proc/self/cwd, so one path names a different repository on each machine.
func TestAnEditOfAnOutpostReviewWritesTheOutpostsFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("gives each daemon its own repository at one path through /proc/self/cwd, which only Linux has")
	}
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	const document = "/proc/self/cwd/review.md"
	review := "---\nattn: review\nrepository: /proc/self/cwd/repo\nbase: main\nhead: main\n---\n# Calc\n"
	const homeNow, outpostNow = "package calc\n\n// home\n", "package calc\n\n// outpost sum\n"
	for stack, now := range map[*testworld.Stack]string{home: homeNow, outpost: outpostNow} {
		stack.DaemonDir = stack.Path("daemon-dir")
		reviewedRepository(t, filepath.Join(stack.DaemonDir, "repo"), "package calc\n", now)
		if err := os.WriteFile(filepath.Join(stack.DaemonDir, "review.md"), []byte(review), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	remote := outpost.App()
	pair.Connect()
	copied := remote.EventCount()
	requireStdout(t, home.Attn("settings", "set", "code_review_enabled", "true"), "true")
	testworld.AwaitSince(remote, copied, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(m.ChangedKey) == "code_review_enabled"
	})
	app := pair.App

	fileID := uuid.NewString()
	file := testworld.Request(app, protocol.ReviewFileMessage{Cmd: protocol.CmdReviewFile, Host: protocol.Ptr(pair.Host), Document: document, Path: "calc.go", RequestID: protocol.Ptr(fileID)},
		protocol.EventReviewFileResult, func(r protocol.ReviewFileResultMessage) bool { return protocol.Deref(r.RequestID) == fileID })
	if !file.Success || file.Hash == nil {
		t.Fatalf("review_file on the outpost answered %+v, want the outpost's file with its hash", file)
	}

	editID := uuid.NewString()
	edited := testworld.Request(app, protocol.ReviewEditMessage{Cmd: protocol.CmdReviewEdit, Host: protocol.Ptr(pair.Host), Document: document, Path: "calc.go",
		BaseHash: protocol.Deref(file.Hash), From: 3, To: 3, Text: "// the outpost's sum, edited", RequestID: protocol.Ptr(editID)},
		protocol.EventReviewEditResult, func(r protocol.ReviewEditResultMessage) bool { return protocol.Deref(r.RequestID) == editID })
	if !edited.Success || edited.Conflict {
		t.Fatalf("review_edit on the outpost answered %+v, want it written", edited)
	}
	outpostFile, err := os.ReadFile(filepath.Join(outpost.DaemonDir, "repo", "calc.go"))
	if err != nil || string(outpostFile) != "package calc\n\n// the outpost's sum, edited\n" {
		t.Errorf("the outpost's calc.go after the edit is %q (%v), want the edited line", outpostFile, err)
	}
	homeFile, err := os.ReadFile(filepath.Join(home.DaemonDir, "repo", "calc.go"))
	if err != nil || string(homeFile) != homeNow {
		t.Errorf("the home's calc.go after an outpost edit is %q (%v), want it untouched", homeFile, err)
	}
	t.Logf("review_edit for host %s wrote the outpost's calc.go; the home's file at the same path kept %q", pair.Host, homeFile)
}
