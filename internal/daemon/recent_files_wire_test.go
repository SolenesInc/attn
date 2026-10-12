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

func TestRecentFilesMergeActivityAndForgetDeletedFilesOnRequest(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "first", "second")
		plan, notes, agent, gone := markdownFile(t, w, "plan.md"), markdownFile(t, w, "notes.md"), markdownFile(t, w, "agent.md"), markdownFile(t, w, "gone.md")

		openMarkdown(t, app, "first", plan)
		openMarkdown(t, app, "second", plan)
		openMarkdown(t, app, "first", notes)
		editFiles(t, w, cli, "first", agent, w.Path("docs", "script.sh"), "relative.md")
		files := recentFiles(app, 0, "")
		if got := paths(files); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Fatalf("recent files = %v, want the twice-opened plan, then the opened notes above the agent's edit", got)
		}
		if files[0].Count != 2 || protocol.Deref(files[0].SessionID) != "second" || files[0].Source != "opened" {
			t.Errorf("the reopened plan = %+v, want count 2 last opened by the second session", files[0])
		}

		w.advance(time.Minute)
		editFiles(t, w, cli, "second", plan)
		if merged := recentFiles(app, 0, "")[0]; merged.Path != plan || merged.Count != 3 || merged.Source != "edited" {
			t.Errorf("plan after an agent edit = %+v, want one entry counting all three with the edit as its latest source", merged)
		}

		editFiles(t, w, cli, "first", gone)
		openMarkdown(t, app, "first", gone)
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Fatalf("recent files after deletion = %v, want the missing file forgotten under every source", got)
		}
		markdownFile(t, w, "gone.md")
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Fatalf("recent files after recreating the file = %v, want its old activity forgotten", got)
		}
		openMarkdown(t, app, "first", gone)
		files = recentFiles(app, 0, "")
		index := slices.Index(paths(files), gone)
		if index < 0 {
			t.Fatal("reopened file missing from recent files")
		}
		if files[index].Count != 1 || files[index].Source != "opened" {
			t.Errorf("reopened file = %+v, want only the new open recorded", files[index])
		}
	})
}

func TestRecentFilesRankByFrecencyBeforeApplyingTheLimit(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "agent")
		frequentOld, staleOnce, recentTwice := markdownFile(t, w, "frequent-old.md"), markdownFile(t, w, "stale-once.md"), markdownFile(t, w, "recent-twice.md")

		for range 5 {
			editFiles(t, w, cli, "agent", frequentOld)
		}
		editFiles(t, w, cli, "agent", staleOnce)
		w.advance(time.Hour)
		editFiles(t, w, cli, "agent", recentTwice)
		editFiles(t, w, cli, "agent", recentTwice)
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{frequentOld, recentTwice, staleOnce}) {
			t.Fatalf("recent files = %v, want five older edits above two recent ones", got)
		}

		var fresh []string
		for i := range 6 {
			fresh = append(fresh, markdownFile(t, w, fmt.Sprintf("fresh-%d.md", i)))
		}
		editFiles(t, w, cli, "agent", fresh...)
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
		placeSessions(t, w, app, cli, "agent")
		here, there, sibling := w.Path("repo", "docs", "here.md"), w.Path("elsewhere", "there.md"), w.Path("repo-other", "near.md")
		for _, path := range []string{here, there, sibling} {
			writeMarkdownFile(t, path)
		}
		editFiles(t, w, cli, "agent", here, there, sibling)

		if got := paths(recentFiles(app, 0, "")); len(got) != 3 || !slices.Contains(got, here) || !slices.Contains(got, there) || !slices.Contains(got, sibling) {
			t.Fatalf("recent files without a workspace = %v, want all three", got)
		}
		if got := paths(recentFiles(app, 0, w.Path("repo"))); !slices.Equal(got, []string{here, there, sibling}) {
			t.Errorf("recent files for %s = %v, want its own file first and the sibling directory unboosted", w.Path("repo"), got)
		}
	})
}

func TestRecentFilesForgetDeletedFilesBelowTheRequestedLimit(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "agent")
		live, gone := markdownFile(t, w, "live.md"), markdownFile(t, w, "gone.md")
		openMarkdown(t, app, "agent", live)
		editFiles(t, w, cli, "agent", gone)
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		if got := paths(recentFiles(app, 1, "")); !slices.Equal(got, []string{live}) {
			t.Fatalf("limited recent files = %v, want the live file", got)
		}
		markdownFile(t, w, "gone.md")
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{live}) {
			t.Fatal("the deleted file below the requested limit kept its old activity")
		}
		editFiles(t, w, cli, "agent", gone)
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{live, gone}) {
			t.Fatalf("recent files after a new edit = %v, want the file recorded again", got)
		}
	})
}

func TestRecentFilesKeepActivityWhenStatIsDenied(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "agent")
		path := markdownFile(t, w, "protected.md")
		editFiles(t, w, cli, "agent", path)
		dir := filepath.Dir(path)
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Error(err)
			}
		})
		if _, err := os.Stat(path); !os.IsPermission(err) {
			t.Skipf("filesystem does not deny stat: %v", err)
		}
		if got := paths(recentFiles(app, 0, "")); !slices.Equal(got, []string{path}) {
			t.Fatalf("recent files with permission denied = %v, want its activity preserved", got)
		}
	})
}

func placeSessions(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, ids ...string) {
	t.Helper()
	dir := w.Path("docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := w.InjectSession(id, id, dir, protocol.SessionAgentClaude); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
}

func markdownFile(t *testing.T, w *world, name string) string {
	t.Helper()
	path := w.Path("docs", name)
	writeMarkdownFile(t, path)
	return path
}

func writeMarkdownFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# "+filepath.Base(path)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		Cmd: protocol.CmdOpenMarkdown, Path: path, SessionID: protocol.Ptr(protocol.SessionID(sessionID)), RequestID: protocol.Ptr(requestID),
	}, protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool {
		return protocol.Deref(r.RequestID) == requestID
	})
}

func editFiles(t *testing.T, w *world, cli *client.Client, sessionID string, paths ...string) {
	t.Helper()
	if err := cli.RecordFilesEdited(protocol.TerminalID(w.Terminal(sessionID)), paths); err != nil {
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
