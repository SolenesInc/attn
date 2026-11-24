package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRecentFilesMergeActivityAndForgetDeletedFilesOnRequest(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "first", "second")
		plan, notes, agent, gone := markdownFile(t, w, "plan.md"), markdownFile(t, w, "notes.md"), markdownFile(t, w, "agent.md"), markdownFile(t, w, "gone.md")
		if err := os.WriteFile(w.Path("docs", "script.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		openMarkdown(t, app, "first", plan)
		openMarkdown(t, app, "second", plan)
		openMarkdown(t, app, "first", notes)
		editFiles(t, w, cli, "first", agent, w.Path("docs", "script.sh"), "relative.md")
		files := recentFiles(app, 0)
		if got := paths(files); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Fatalf("recent files = %v, want the twice-opened plan, then the opened notes above the agent's edit", got)
		}
		if files[0].Count != 2 || protocol.Deref(files[0].SessionID) != "second" || files[0].Source != "opened" {
			t.Errorf("the reopened plan = %+v, want count 2 last opened by the second session", files[0])
		}

		w.advance(time.Minute)
		editFiles(t, w, cli, "second", plan)
		if merged := recentFiles(app, 0)[0]; merged.Path != plan || merged.Count != 3 || merged.Source != "edited" {
			t.Errorf("plan after an agent edit = %+v, want one entry counting all three with the edit as its latest source", merged)
		}

		editFiles(t, w, cli, "first", gone)
		openMarkdown(t, app, "first", gone)
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		if got := paths(recentFiles(app, 0)); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Fatalf("recent files after deletion = %v, want the missing file forgotten under every source", got)
		}
		markdownFile(t, w, "gone.md")
		if got := paths(recentFiles(app, 0)); !slices.Equal(got, []string{plan, notes, agent}) {
			t.Fatalf("recent files after recreating the file = %v, want its old activity forgotten", got)
		}
		openMarkdown(t, app, "first", gone)
		files = recentFiles(app, 0)
		index := slices.Index(paths(files), gone)
		if index < 0 {
			t.Fatal("reopened file missing from recent files")
		}
		if files[index].Count != 1 || files[index].Source != "opened" {
			t.Errorf("reopened file = %+v, want only the new open recorded", files[index])
		}
	})
}

func TestRecentFilesPutAFileAnAgentJustEditedAboveAnOlderBurst(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "agent")
		burst, fresh, pair := markdownFile(t, w, "burst.md"), markdownFile(t, w, "fresh.md"), markdownFile(t, w, "pair.md")
		for range 20 {
			editFiles(t, w, cli, "agent", burst)
		}
		later := func(elapsed time.Duration, session string) (*testworld.Peer, *client.Client) {
			t.Helper()
			w.stop()
			w.advance(elapsed)
			w.restart()
			app, cli := w.App(), w.Client()
			placeSessions(t, w, app, cli, session)
			return app, cli
		}
		app, cli = later(time.Hour, "second")
		editFiles(t, w, cli, "second", fresh)
		if got := paths(recentFiles(app, 0)); !slices.Equal(got, []string{fresh, burst}) {
			t.Fatalf("recent files = %v, want the file just edited above twenty edits an hour ago", got)
		}
		app, _ = later(3*time.Hour, "third")
		if got := paths(recentFiles(app, 0)); !slices.Equal(got, []string{burst, fresh}) {
			t.Fatalf("three hours later = %v, want the burst back above the single edit", got)
		}
		_, cli = later(9*24*time.Hour, "fourth")
		editFiles(t, w, cli, "fourth", pair)
		editFiles(t, w, cli, "fourth", pair)
		app, _ = later(24*time.Hour, "fifth")
		if got := paths(recentFiles(app, 2)); !slices.Equal(got, []string{pair, burst}) {
			t.Fatalf("ten days on, the top two = %v, want two edits a day ago above twenty edits ten days ago", got)
		}
	})
}

func TestRecentFilesFillTheLimitPastDeletedFiles(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		placeSessions(t, w, app, cli, "agent")
		live, gone, alsoGone := markdownFile(t, w, "live.md"), markdownFile(t, w, "gone.md"), markdownFile(t, w, "also-gone.md")
		openMarkdown(t, app, "agent", live)
		w.advance(time.Minute)
		openMarkdown(t, app, "agent", gone)
		w.advance(time.Minute)
		openMarkdown(t, app, "agent", alsoGone)
		for _, path := range []string{gone, alsoGone} {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if got := paths(recentFiles(app, 1)); !slices.Equal(got, []string{live}) {
			t.Fatalf("recent files up to one = %v, want the live file past both deleted ones", got)
		}
	})
}

type outpostFiles struct {
	mu        sync.Mutex
	missing   map[string]bool
	asked     [][]string
	stall     chan struct{}
	roundTrip time.Duration
}

func (f *outpostFiles) answer(_ *bridge.Conn, incoming ssh.NewChannel) {
	go bridge.Answer(incoming, func(ctx context.Context, raw []byte) (any, error) {
		var call struct {
			Cmd   string   `json:"cmd"`
			Paths []string `json:"paths"`
		}
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		switch call.Cmd {
		case protocol.CmdTileDocumentRead:
			return protocol.TileDocument{Content: "# far\n", Version: 1}, nil
		case protocol.CmdFindMissingFiles:
			f.mu.Lock()
			f.asked = append(f.asked, slices.Clone(call.Paths))
			stall, roundTrip := f.stall, f.roundTrip
			f.mu.Unlock()
			select {
			case <-time.After(roundTrip):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if stall != nil {
				select {
				case <-stall:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			missing := []string{}
			for _, path := range call.Paths {
				if f.missing[path] {
					missing = append(missing, path)
				}
			}
			return protocol.MissingFiles{Missing: missing}, nil
		}
		return struct{}{}, nil
	})
}

func (f *outpostFiles) set(change func(*outpostFiles)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *outpostFiles) takeAsked() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	asked := f.asked
	f.asked = nil
	return asked
}

func (f *outpostFiles) connect(t *testing.T, w *world) (far *farOutpost, app *testworld.Peer, open func(path string)) {
	t.Helper()
	far = startFarOutpostWith(t, w, nil, bridge.Handlers{daemon.BridgeCall: f.answer})
	app = w.App()
	placeSessions(t, w, app, w.Client(), "s1")
	setSetting(t, app, "outposts_enabled", "true")
	app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
	host := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
		return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
	}).Outposts[0].ID
	return far, app, func(path string) {
		t.Helper()
		requestID := uuid.NewString()
		opened := testworld.Request(app, protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Host: protocol.Ptr(host), Path: path, SessionID: protocol.Ptr(protocol.SessionID("s1")), RequestID: protocol.Ptr(requestID)},
			protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool { return protocol.Deref(r.RequestID) == requestID })
		if !opened.Success {
			t.Fatalf("open %s on the outpost: %s", path, protocol.Deref(opened.Error))
		}
	}
}

func TestRecentFilesAnswerWithinASecondHoweverManyOfAnOutpostsFilesWereDeleted(t *testing.T) {
	for _, outpost := range []struct {
		name      string
		roundTrip time.Duration
		want      func(home string, deletedNewestFirst []string) []string
	}{
		{"that answers promptly lists the files that exist", 89 * time.Millisecond, func(home string, _ []string) []string {
			return []string{"/far/newest.md", home, "/far/oldest.md"}
		}},
		{"too slow to check them all lists the rest unchecked", 600 * time.Millisecond, func(home string, deleted []string) []string {
			return []string{"/far/newest.md", home, deleted[1]}
		}},
	} {
		t.Run(outpost.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				files := &outpostFiles{missing: map[string]bool{}}
				_, app, openOnOutpost := files.connect(t, w)
				openOnOutpost("/far/oldest.md")
				var deleted []string
				for i := range 22 {
					w.advance(time.Minute)
					path := fmt.Sprintf("/far/removed-worktree/%02d.md", i)
					openOnOutpost(path)
					deleted = append([]string{path}, deleted...)
				}
				w.advance(time.Minute)
				home := markdownFile(t, w, "home.md")
				openMarkdown(t, app, "s1", home)
				w.advance(time.Minute)
				openOnOutpost(home)
				deleted = append([]string{home}, deleted...)
				w.advance(time.Minute)
				openOnOutpost("/far/newest.md")
				files.set(func(f *outpostFiles) {
					f.roundTrip = outpost.roundTrip
					for _, path := range deleted {
						f.missing[path] = true
					}
				})

				answer := make(chan []protocol.FileActivity, 1)
				go func() { answer <- recentFiles(app, 2) }()
				w.advance(time.Second)
				select {
				case got := <-answer:
					if want := outpost.want(home, deleted); !slices.Equal(paths(got), want) {
						t.Fatalf("recent files up to two per machine = %v, want %v", paths(got), want)
					}
				default:
					t.Fatal("recent files, the home's included, still wait on the outpost after a second")
				}
			})
		})
	}
}

func TestAFileReopenedAfterItsOutpostReportedItMissingStaysListed(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		files := &outpostFiles{missing: map[string]bool{}}
		_, app, openOnOutpost := files.connect(t, w)
		for _, path := range []string{"/far/older.md", "/far/recreated.md", "/far/newest.md"} {
			openOnOutpost(path)
			w.advance(time.Minute)
		}
		files.set(func(f *outpostFiles) {
			f.roundTrip = 600 * time.Millisecond
			f.missing["/far/recreated.md"] = true
		})
		answer := make(chan []protocol.FileActivity, 1)
		go func() { answer <- recentFiles(w.App(), 2) }()
		w.advance(700 * time.Millisecond)
		files.set(func(f *outpostFiles) {
			f.roundTrip = 0
			f.missing = map[string]bool{}
		})
		openOnOutpost("/far/recreated.md")
		w.advance(300 * time.Millisecond)
		<-answer
		if got, want := paths(recentFiles(app, 3)), []string{"/far/recreated.md", "/far/newest.md", "/far/older.md"}; !slices.Equal(got, want) {
			t.Fatalf("after recreated.md was deleted, recreated and reopened while its outpost was being asked = %v, want %v", got, want)
		}
	})
}

func TestRecentFilesKeepAnOutpostsOwnLimitAndCheckItsFilesOnTheOutpost(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		files := &outpostFiles{missing: map[string]bool{}}
		far, app, openOnOutpost := files.connect(t, w)
		home1, home2, home3 := markdownFile(t, w, "home1.md"), markdownFile(t, w, "home2.md"), markdownFile(t, w, "home3.md")
		for _, open := range []func(){
			func() { openMarkdown(t, app, "s1", home1) }, func() { openOnOutpost("/far/a.md") },
			func() { openMarkdown(t, app, "s1", home2) }, func() { openOnOutpost("/far/b.md") },
			func() { openMarkdown(t, app, "s1", home3) }, func() { openOnOutpost("/far/c.md") },
		} {
			w.advance(time.Minute)
			open()
		}
		files.takeAsked()

		if got, want := paths(recentFiles(app, 2)), []string{"/far/c.md", home3, "/far/b.md", home2}; !slices.Equal(got, want) {
			t.Fatalf("recent files up to two per machine = %v, want %v", got, want)
		}
		if got, want := files.takeAsked(), [][]string{{"/far/c.md", "/far/b.md"}}; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
			t.Fatalf("the outpost was asked about %v, want only its own two candidates %v", got, want)
		}

		files.set(func(f *outpostFiles) { f.missing["/far/c.md"] = true })
		unchecked := []string{home3, "/far/b.md", home2, "/far/a.md"}
		if got := paths(recentFiles(app, 2)); !slices.Equal(got, unchecked) {
			t.Fatalf("after c.md was deleted on the outpost = %v, want %v", got, unchecked)
		}
		files.takeAsked()

		stall := make(chan struct{})
		defer close(stall)
		files.set(func(f *outpostFiles) { f.stall = stall })
		answer := make(chan []protocol.FileActivity, 1)
		go func() { answer <- recentFiles(w.App(), 2) }()
		w.advance(999 * time.Millisecond)
		select {
		case got := <-answer:
			t.Fatalf("recent files answered %v before the stalled outpost's second was up", paths(got))
		default:
		}
		w.advance(time.Millisecond)
		select {
		case got := <-answer:
			if !slices.Equal(paths(got), unchecked) {
				t.Fatalf("with the outpost stalled = %v, want its files unchecked %v", paths(got), unchecked)
			}
		default:
			t.Fatal("recent files still waits on a stalled outpost past its second")
		}
		files.set(func(f *outpostFiles) { f.stall = nil })
		files.takeAsked()

		far.link.Store(int32(linkRefused))
		far.network.Cut(true)
		w.advance(bridge.LostAfter)
		if got := paths(recentFiles(app, 2)); !slices.Equal(got, unchecked) {
			t.Fatalf("with the outpost lost = %v, want its files unchecked %v", got, unchecked)
		}
		if asked := files.takeAsked(); len(asked) != 0 {
			t.Fatalf("a lost outpost was asked about %v", asked)
		}
	})
}

func TestRecentFilesForgetActivityWhenStatIsDenied(t *testing.T) {
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
		_, statErr := os.Stat(path)
		if !os.IsPermission(statErr) {
			t.Skipf("filesystem does not deny stat: %v", statErr)
		}
		if got := paths(recentFiles(app, 0)); len(got) != 0 {
			t.Fatalf("recent files with permission denied = %v, want its activity forgotten", got)
		}
		if refused := requestOpenMarkdown(app, "agent", path); refused.Success || protocol.Deref(refused.Error) != statErr.Error() {
			t.Errorf("opening a denied file answered %+v, want %q", refused, statErr)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := paths(recentFiles(app, 0)); len(got) != 0 {
			t.Fatalf("recent files after restoring permission = %v, want its old activity forgotten", got)
		}
	})
}

func TestRecentFilesForgetHungPathsAtTheDeadlineWithoutBlockingShutdown(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, secondApp, cli := w.App(), w.App(), w.Client()
		placeSessions(t, w, app, cli, "agent")
		live, hung := markdownFile(t, w, "live.md"), markdownFile(t, w, "hung.md")
		editFiles(t, w, cli, "agent", live, hung)
		entered, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		var calls atomic.Int32
		w.UseFileStat(func(path string) (os.FileInfo, error) {
			if path == hung {
				if calls.Add(1) == 1 {
					close(entered)
				}
				<-release
			}
			return os.Stat(path)
		})
		answer, secondAnswer := make(chan []protocol.FileActivity, 1), make(chan []protocol.FileActivity, 1)
		startedAt := time.Now()
		go func() { answer <- recentFiles(app, 0) }()
		<-entered
		go func() { secondAnswer <- recentFiles(secondApp, 0) }()
		synctest.Wait()
		w.advance(499 * time.Millisecond)
		select {
		case <-answer:
			t.Fatal("recent files answered before the filesystem deadline")
		case <-secondAnswer:
			t.Fatal("the overlapping request answered before the running pass finished")
		default:
		}
		w.advance(time.Millisecond)
		if got := paths(<-answer); !slices.Equal(got, []string{live}) {
			t.Fatalf("recent files with a hung stat = %v, want only the healthy path", got)
		}
		if got := paths(<-secondAnswer); !slices.Equal(got, []string{live}) {
			t.Fatalf("overlapping recent files = %v, want the hung path already forgotten", got)
		}
		if elapsed := time.Since(startedAt); elapsed != 500*time.Millisecond {
			t.Fatalf("recent files answered after %s, want the 500ms deadline", elapsed)
		}
		if got := paths(recentFiles(app, 0)); !slices.Equal(got, []string{live}) {
			t.Fatalf("next recent files = %v, want the hung path forgotten", got)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("stat called %d times for the hung path, want it checked only once", got)
		}
		w.stop()
	})
}

func placeSessions(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, ids ...string) {
	t.Helper()
	dir := w.Path("docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := w.InjectSession(id, id, dir, "claude"); err != nil {
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

func recentFiles(app *testworld.Peer, limit int) []protocol.FileActivity {
	app.T.Helper()
	requestID := uuid.NewString()
	msg := protocol.RecentFilesMessage{Cmd: protocol.CmdRecentFiles, RequestID: protocol.Ptr(requestID)}
	if limit > 0 {
		msg.Limit = protocol.Ptr(limit)
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

func TestAMarkdownFileAnAppServerAgentEditsThroughAFileURLIsARecentFileAtItsPath(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		notes := w.Path("notes", "my notes.md")
		writeMarkdownFile(t, notes)
		promptUntilWorking(app, session, view, server, "write my notes")
		server.ToolCall(view.ConversationID, writeMarkdownCall((&url.URL{Scheme: "file", Path: notes}).String()))
		awaitServerFeed(app, session, view.ConversationID, server, 100)
		if got := paths(recentFiles(app, 0)); !slices.Equal(got, []string{notes}) {
			t.Fatalf("recent files = %q, want the notes the agent wrote at %s", got, notes)
		}
	})
}

func TestAMarkdownFileAnAppServerAgentEditsIsARecentFile(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		plan := w.Path("shop", "docs", "plan.md")
		writeMarkdownFile(t, plan)
		promptUntilWorking(app, session, view, server, "write the plan")
		server.ToolCall(view.ConversationID, writeMarkdownCall("docs/plan.md"))
		server.ToolCall(view.ConversationID, writeMarkdownCall("main.go"))
		awaitServerFeed(app, session, view.ConversationID, server, 100)
		files := recentFiles(app, 0)
		if got := paths(files); !slices.Equal(got, []string{plan}) || files[0].Source != "edited" || string(protocol.Deref(files[0].SessionID)) != session {
			t.Fatalf("recent files = %+v, want the plan the agent wrote, edited by %s", files, session)
		}
	})
}
