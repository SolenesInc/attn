package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRecentFilesMergeWhatUsersOpenAndAgentsEditUntilAFileIsGone(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "first", "second")
		plan, notes, agent, gone := markdownFile(t, w, "plan.md"), markdownFile(t, w, "notes.md"), w.Path("docs", "agent.md"), markdownFile(t, w, "gone.md")

		openMarkdown(t, app, "first", plan)
		openMarkdown(t, app, "second", plan)
		openMarkdown(t, app, "first", notes)
		editFiles(t, cli, "first", agent, w.Path("docs", "script.sh"), "relative.md")
		files := recentFiles(app, 0, "")
		if got := paths(files); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Fatalf("recent files = %v, want the twice-opened plan, then the opened notes above the agent's edit", got)
		}
		if files[0].Count != 2 || protocol.Deref(files[0].SessionID) != "second" || files[0].Source != "opened" {
			t.Errorf("the reopened plan = %+v, want count 2 last opened by the second session", files[0])
		}

		w.advance(time.Minute)
		editFiles(t, cli, "second", plan)
		if merged := recentFiles(app, 0, "")[0]; merged.Path != plan || merged.Count != 3 || merged.Source != "edited" {
			t.Errorf("plan after an agent edit = %+v, want one entry counting all three with the edit as its latest source", merged)
		}

		editFiles(t, cli, "first", gone)
		openMarkdown(t, app, "first", gone)
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(paths(recentFiles(app, 0, "")), gone) {
			t.Fatal("a file that went missing left recent files before anyone tried to open it")
		}
		if opened := requestOpenMarkdown(app, "first", gone); opened.Success {
			t.Fatalf("opening a deleted file = %+v, want it refused", opened)
		}
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Errorf("recent files after the missing file failed to open = %v, want it forgotten under every source", got)
		}
	})
}

func TestRecentFilesRankByFrecencyBeforeApplyingTheLimit(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		frequentOld, staleOnce, recentTwice := w.Path("docs", "frequent-old.md"), w.Path("docs", "stale-once.md"), w.Path("docs", "recent-twice.md")

		for range 5 {
			editFiles(t, cli, "agent", frequentOld)
		}
		editFiles(t, cli, "agent", staleOnce)
		w.advance(time.Hour)
		editFiles(t, cli, "agent", recentTwice)
		editFiles(t, cli, "agent", recentTwice)
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{frequentOld, recentTwice, staleOnce}) {
			t.Fatalf("recent files = %v, want five older edits above two recent ones", got)
		}

		var fresh []string
		for i := range 6 {
			fresh = append(fresh, w.Path("docs", fmt.Sprintf("fresh-%d.md", i)))
		}
		editFiles(t, cli, "agent", fresh...)
		want := append(append([]string{frequentOld, recentTwice}, fresh...), staleOnce)
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, want) {
			t.Errorf("recent files = %v, want a fresh single edit above an hour-old one", got)
		}
		if got := paths(recentFiles(app, 2, "")); !slices.Equal(got, []string{frequentOld, recentTwice}) {
			t.Errorf("the top two = %v, want the two best-ranked files, not the newest", got)
		}
	})
}

func TestRecentFilesPreferTheCallersWorkspace(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		here, there, sibling := w.Path("repo", "docs", "here.md"), w.Path("elsewhere", "there.md"), w.Path("repo-other", "near.md")
		editFiles(t, cli, "agent", here, there, sibling)

		if got := paths(recentFiles(app, 0, "")); len(got) != 3 || !slices.Contains(got, here) || !slices.Contains(got, there) || !slices.Contains(got, sibling) {
			t.Fatalf("recent files without a workspace = %v, want all three", got)
		}
		if got := paths(recentFiles(app, 0, w.Path("repo"))); !slices.Equal(got, []string{here, there, sibling}) {
			t.Errorf("recent files for %s = %v, want its own file first and the sibling directory unboosted", w.Path("repo"), got)
		}
	})
}

func placeSessions(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, ids ...string) {
	t.Helper()
	dir := w.Path("docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testworld.Request(app, protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-docs", Title: "docs", Directory: dir,
	}, protocol.EventWorkspaceRegistered, func(protocol.WebSocketEvent) bool { return true })
	for _, id := range ids {
		if err := cli.Register(id, id, dir); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
		testworld.Request(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
			Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "workspace-docs", SessionID: id, PaneID: protocol.Ptr("pane-" + id),
		}, protocol.EventWorkspaceLayoutActionResult, func(protocol.WorkspaceLayoutActionResultMessage) bool { return true })
	}
}

func markdownFile(t *testing.T, w *world, name string) string {
	t.Helper()
	path := w.Path("docs", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func openMarkdown(t *testing.T, app *testworld.Peer, sessionID, path string) {
	t.Helper()
	if opened := requestOpenMarkdown(app, sessionID, path); !opened.Success {
		t.Fatalf("open %s from %s refused: %s", path, sessionID, protocol.Deref(opened.Error))
	}
}

func requestOpenMarkdown(app *testworld.Peer, sessionID, path string) protocol.OpenMarkdownResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.OpenMarkdownMessage{
		Cmd: protocol.CmdOpenMarkdown, Path: path, SessionID: protocol.Ptr(sessionID), RequestID: protocol.Ptr(requestID),
	}, protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool {
		return protocol.Deref(r.RequestID) == requestID
	})
}

func editFiles(t *testing.T, cli *client.Client, sessionID string, paths ...string) {
	t.Helper()
	if err := cli.RecordFilesEdited(sessionID, paths); err != nil {
		t.Fatalf("report files %s edited: %v", sessionID, err)
	}
}

func recentFiles(app *testworld.Peer, limit int, root string) []protocol.FileActivity {
	app.T.Helper()
	requestID := uuid.NewString()
	msg := protocol.RecentFilesMessage{Cmd: protocol.CmdRecentFiles, RequestID: protocol.Ptr(requestID)}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
	}
	if root != "" {
		msg.Root = protocol.Ptr(root)
	}
	result := testworld.Request(app, msg, protocol.EventRecentFilesResult, func(r protocol.RecentFilesResultMessage) bool {
		return r.RequestID == requestID
	})
	if !result.Success {
		app.T.Fatalf("recent files refused: %s", protocol.Deref(result.Error))
	}
	return result.Files
}

func paths(files []protocol.FileActivity) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}
