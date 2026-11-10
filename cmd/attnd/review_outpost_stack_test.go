package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func reviewedRepository(t *testing.T, dir, atBase, now string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte(atBase), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "calc.go"}, {"-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-qm", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte(now), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Both stacks share one disk, so the review is opened through /proc/self/cwd:
// each daemon resolves that path inside its own working directory, which
// gives one path with a different repository on each machine.
func TestTheFileViewAndSearchOfAnOutpostsReviewReadTheOutpostsRepository(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("gives each daemon its own repository at one path through /proc/self/cwd, which only Linux has")
	}
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithoutAgentKit())
	const document = "/proc/self/cwd/review.md"
	review := "---\nattn: review\nrepository: /proc/self/cwd/repo\nbase: main\nhead: main\n---\n# Calc\n"
	for stack, now := range map[*testworld.Stack]string{home: "package calc\n\n// home\n", outpost: "package calc\n\n// outpost sum\n"} {
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
	var rows []string
	for _, row := range file.Rows {
		rows = append(rows, row.Kind+row.Text)
	}
	if want := []string{" package calc", "+", "+// outpost sum"}; !file.Success || strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("review_file on the outpost answered rows %q (error %s), want the outpost's file with its change %q", rows, protocol.Deref(file.Error), want)
	}

	searchID := uuid.NewString()
	search := testworld.Request(app, protocol.ReviewSearchMessage{Cmd: protocol.CmdReviewSearch, Host: protocol.Ptr(pair.Host), Document: document, Query: "sum", RequestID: protocol.Ptr(searchID)},
		protocol.EventReviewSearchResult, func(r protocol.ReviewSearchResultMessage) bool { return protocol.Deref(r.RequestID) == searchID })
	if !search.Success || len(search.Hits) != 1 || search.Hits[0].Path != "calc.go" || search.Hits[0].Line != 3 {
		t.Fatalf("review_search on the outpost answered %+v, want the outpost's calc.go:3", search)
	}

	homeID := uuid.NewString()
	atHome := testworld.Request(app, protocol.ReviewFileMessage{Cmd: protocol.CmdReviewFile, Document: document, Path: "calc.go", RequestID: protocol.Ptr(homeID)},
		protocol.EventReviewFileResult, func(r protocol.ReviewFileResultMessage) bool { return protocol.Deref(r.RequestID) == homeID })
	if len(atHome.Rows) != 3 || atHome.Rows[2].Text != "// home" {
		t.Fatalf("review_file of the same path at home answered %+v, want the home's file", atHome)
	}
	t.Logf("review_file and review_search for host %s read the outpost's repository; the same path at home reads the home's", pair.Host)
}
