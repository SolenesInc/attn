package daemon_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const useOfAdd = "package calc\n\nfunc Twice(a int) int {\n\treturn Add(a, a)\n}\n"

// navigatedRepo is reviewRepo with a file that uses Add, and an origin, which
// keys the server definitions set for it.
func navigatedRepo(t *testing.T) string {
	t.Helper()
	repo := reviewRepo(t)
	commitFile(t, repo, "use.go", useOfAdd)
	runGit(t, repo, "remote", "add", "origin", "https://example.test/calc.git")
	return repo
}

func linkFakeLanguageServer(t *testing.T, w *world, cli *client.Client) {
	t.Helper()
	fake := fakeagent.InstallLanguageServer(t, testworld.FakeAgentBinary(t), w.Path("installed"))
	if _, err := cli.CodeNavLink(fake); err != nil {
		t.Fatalf("nav link: %v", err)
	}
}

func requestCodeNav(app *testworld.Peer, document, path string, line, character int, kind string) protocol.CodeNavResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return testworld.Request(app, protocol.CodeNavMessage{Cmd: protocol.CmdCodeNav, Document: document, Path: path, Line: line, Character: character, Kind: kind, RequestID: protocol.Ptr(id)},
		protocol.EventCodeNavResult, func(r protocol.CodeNavResultMessage) bool { return protocol.Deref(r.RequestID) == id })
}

func navigation(review *protocol.ReviewBundle, language string) protocol.ReviewNavigation {
	if review != nil {
		for _, nav := range review.Navigation {
			if nav.Language == language {
				return nav
			}
		}
	}
	return protocol.ReviewNavigation{}
}

func awaitNavigation(app *testworld.Peer, document, language, state string) protocol.DesktopTileContentMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.Path == document && navigation(m.Review, language).State == state
	})
}

func places(locations []protocol.CodeNavLocation) []string {
	var out []string
	for _, l := range locations {
		out = append(out, fmt.Sprintf("%s:%d:%d %s", l.Path, l.Line, l.Character, strings.TrimSpace(l.Text)))
	}
	return out
}

func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func runningServers(t *testing.T, cli *client.Client) []protocol.CodeNavServer {
	t.Helper()
	status, err := cli.CodeNavStatus("")
	if err != nil {
		t.Fatalf("nav status: %v", err)
	}
	return status.Servers
}

func TestAShownReviewAnswersDefinitionUsesAndTypeAndItsServerStopsOnceNoReviewIsShown(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	linkFakeLanguageServer(t, w, cli)
	document := w.Path("reviews", "calc.md")

	if _, err := cli.ReviewStart(document, repo, "HEAD~1"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	w.awaitTimer(daemon.CodeNavIdleStopTimer)
	appendReviewBody(t, document, "# Name the sum\n\n## Add keeps its result\n\n### calc.go:3-6\n\n### use.go:3-5\n")
	if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	shown := awaitNavigation(app, document, "go", "ready")

	definition := requestCodeNav(app, document, "use.go", 4, strings.Index("\treturn Add(a, a)", "Add"), "definition")
	if got, want := places(definition.Locations), []string{"calc.go:3:5 func Add(a, b int) int {"}; !definition.Success || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the definition of Add answered %v (error %s), want %v", got, protocol.Deref(definition.Error), want)
	}
	uses := requestCodeNav(app, document, "calc.go", 3, 5, "references")
	if got, want := places(uses.Locations), []string{"use.go:4:8 return Add(a, a)"}; !uses.Success || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the uses of Add answered %v (error %s), want %v", got, protocol.Deref(uses.Error), want)
	}
	hover := requestCodeNav(app, document, "use.go", 4, 8, "hover")
	if got := protocol.Deref(hover.Text); !hover.Success || got != "func Add(a, b int) int {" {
		t.Errorf("the type of Add answered %q (error %s), want its declaration", got, protocol.Deref(hover.Error))
	}

	servers := runningServers(t, cli)
	if len(servers) != 1 || servers[0].Language != "go" || servers[0].State != "ready" || strings.Join(servers[0].NeededBy, ",") != document {
		t.Fatalf("nav status lists servers %+v, want one ready go server needed by %s", servers, document)
	}
	first := servers[0].Pid

	if closed := desktopAction(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, DesktopID: shown.DesktopID, LeafID: shown.TileID}); !closed.Success {
		t.Fatalf("closing the review's tile failed: %s", protocol.Deref(closed.Error))
	}
	w.awaitTimer(daemon.CodeNavIdleStopTimer)
	w.advance(299 * time.Second)
	if servers := runningServers(t, cli); len(servers) != 1 || processGone(first) {
		t.Fatalf("a second before the idle stop the servers are %+v (pid %d gone: %v), want the server still running", servers, first, processGone(first))
	}
	w.advance(time.Second)
	if servers := runningServers(t, cli); len(servers) != 0 || !processGone(first) {
		t.Fatalf("after the idle stop the servers are %+v (pid %d gone: %v), want none running", servers, first, processGone(first))
	}

	if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
		t.Fatalf("open again: %v", err)
	}
	awaitNavigation(app, document, "go", "ready")
	if servers := runningServers(t, cli); len(servers) != 1 || servers[0].Pid == first {
		t.Fatalf("showing the review again runs servers %+v, want one new server", servers)
	}
}

func TestAServerOnThePathThatNoTestInstalledIsMissingToAReview(t *testing.T) {
	w := newWorld(t)
	onPath := t.TempDir()
	started := filepath.Join(onPath, "started")
	if err := os.WriteFile(filepath.Join(onPath, "gopls"), []byte("#!/bin/sh\ntouch "+started+"\nexec sleep 3600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD~1"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	appendReviewBody(t, document, "# Add\n\n### calc.go:3-6\n")
	if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
		t.Fatalf("open: %v", err)
	}

	awaitNavigation(app, document, "go", "missing")
	if _, err := os.Stat(started); err == nil {
		t.Errorf("a gopls on PATH was started; a daemon finds only the servers its test installed")
	}
}

func TestALanguageWithNoServerIsMissingAndTheReviewStillReads(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	linkFakeLanguageServer(t, w, cli)
	if err := os.WriteFile(filepath.Join(repo, "add.zig"), []byte("fn add(a: i32, b: i32) i32 {\n    return a + b;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := cli.CodeNavDefine(repo, "zig", `{"command": ["attn-no-such-zls"], "extensions": [".zig"]}`)
	if err != nil || set.Key != "code_navigation_example.test/calc_zig" {
		t.Fatalf("nav set answered %+v, %v; want the key for the repository's origin", set, err)
	}
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD~1"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	appendReviewBody(t, document, "# Add in two languages\n\n### calc.go:3-6\n\n### add.zig:1-3\n")
	if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
		t.Fatalf("open: %v", err)
	}

	tile := awaitNavigation(app, document, "go", "ready")
	zig := navigation(tile.Review, "zig")
	if zig.State != "missing" || !strings.Contains(protocol.Deref(zig.Detail), "attn-no-such-zls is not in") {
		t.Errorf("zig navigation is %+v, want missing, naming the program it looked for", zig)
	}
	if len(tile.Review.Stops) != 2 || len(tile.Review.Stops[1].Rows) != 3 {
		t.Errorf("the review's stops are %+v, want both, with the zig file's three lines", tile.Review.Stops)
	}
	asked := requestCodeNav(app, document, "add.zig", 1, 3, "definition")
	if asked.Success || !strings.Contains(protocol.Deref(asked.Error), "attn-no-such-zls is not in") {
		t.Errorf("asking about a zig name answered %+v, want a refusal naming the missing program", asked)
	}
	if _, err := cli.CodeNavDefine(repo, "zig", `{"command": ["gopls"], "extensions": [".zig"]}`); err != nil {
		t.Fatalf("nav set: %v", err)
	}
	if asked := requestCodeNav(app, document, "add.zig", 1, 3, "definition"); !asked.Success {
		t.Errorf("after nav set named a program that is there, asking about a zig name answered %q, want an answer from it", protocol.Deref(asked.Error))
	}
	file := requestReviewFile(app, document, "add.zig")
	if !file.Success || len(file.Rows) != 3 {
		t.Errorf("the file view of the zig file answered %+v, want its three lines", file)
	}

	status, err := cli.CodeNavStatus(repo)
	if err != nil {
		t.Fatalf("nav status: %v", err)
	}
	var listed []string
	for _, language := range status.Languages {
		listed = append(listed, language.Language+" "+language.Source)
	}
	if !strings.Contains(strings.Join(listed, ","), "zig setting") || !strings.Contains(strings.Join(listed, ","), "go built-in") {
		t.Errorf("nav status lists languages %v, want go built-in and zig from its setting", listed)
	}
	if _, err := cli.CodeNavDefine(repo, "zig", ""); err != nil {
		t.Fatalf("nav rm: %v", err)
	}
	if _, err := cli.CodeNavDefine(repo, "zig", ""); err == nil || !strings.Contains(err.Error(), "no definition of zig is set") {
		t.Errorf("removing zig twice answered %v, want a refusal saying none is set", err)
	}
}

func TestAnAnswerOutsideTheRepositoryOpensThatFileAndNoOtherOutsideFile(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	linkFakeLanguageServer(t, w, cli)
	library := w.Path("library")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
	printer := filepath.Join(library, "print.go")
	if err := os.WriteFile(printer, []byte("package fmt\n\nfunc Println(a ...any) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "secret.go"), []byte("package fmt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, "say.go", "package calc\n\nfunc Say() {\n\tfmt.Println(Add(1, 2))\n}\n")
	if _, err := cli.CodeNavDefine(repo, "go", `{"command": ["gopls"], "extensions": [".go"], "settings": {"search": ["`+library+`"]}}`); err != nil {
		t.Fatalf("nav set: %v", err)
	}
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD~2"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	appendReviewBody(t, document, "# Say the sum\n\n### calc.go:3-6\n\n### say.go:3-5\n")
	if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	awaitNavigation(app, document, "go", "ready")

	refused := requestReviewFile(app, document, printer)
	if refused.Success {
		t.Fatalf("review_file of an outside file before any answer named it answered %+v, want a refusal", refused)
	}
	definition := requestCodeNav(app, document, "say.go", 4, strings.Index("\tfmt.Println(Add(1, 2))", "Println"), "definition")
	if len(definition.Locations) != 1 || !definition.Locations[0].Outside || definition.Locations[0].Path != printer {
		t.Fatalf("the definition of Println answered %+v (error %s), want %s marked outside", definition.Locations, protocol.Deref(definition.Error), printer)
	}
	file := requestReviewFile(app, document, printer)
	if !file.Success || len(file.Rows) != 3 || file.Rows[2].Text != "func Println(a ...any) {}" || file.Rows[2].Kind != " " {
		t.Errorf("review_file of the answered outside file answered %+v, want its three lines unchanged", file)
	}
	if other := requestReviewFile(app, document, filepath.Join(library, "secret.go")); other.Success {
		t.Errorf("review_file of an outside file no answer named answered %+v, want a refusal", other)
	}
	if inside := requestCodeNav(app, document, printer, 3, 5, "references"); !inside.Success {
		t.Errorf("asking from inside the answered outside file answered %s, want the uses", protocol.Deref(inside.Error))
	}
}

func TestAPrepareRunsBeforeTheServerStartsAndAgainOnlyOnceItsInputsChange(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	commitFile(t, repo, "go.mod", "module calc\n")
	linkFakeLanguageServer(t, w, cli)
	ran := w.Path("prepared.log")
	definition := `{"command": ["gopls"], "extensions": [".go"], "roots": ["go.mod"], "prepare": {"command": ["sh", "-c", "echo ran >> ` + ran + `"], "inputs": ["*.mod"]}}`
	if _, err := cli.CodeNavDefine(repo, "go", definition); err != nil {
		t.Fatalf("nav set: %v", err)
	}
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD~2"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	w.awaitTimer(daemon.CodeNavIdleStopTimer)
	appendReviewBody(t, document, "# Name the sum\n\n### calc.go:3-6\n\n### use.go:3-5\n\n## Not shown\n\n- go.mod: the module\n")
	prepared := func() int {
		content, _ := os.ReadFile(ran)
		return strings.Count(string(content), "ran")
	}
	showUntilReady := func() protocol.DesktopTileContentMessage {
		t.Helper()
		if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
			t.Fatalf("open: %v", err)
		}
		return awaitNavigation(app, document, "go", "ready")
	}
	closeAndStop := func(shown protocol.DesktopTileContentMessage) {
		t.Helper()
		if closed := desktopAction(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, DesktopID: shown.DesktopID, LeafID: shown.TileID}); !closed.Success {
			t.Fatalf("closing the review's tile failed: %s", protocol.Deref(closed.Error))
		}
		w.awaitTimer(daemon.CodeNavIdleStopTimer)
		w.advance(300 * time.Second)
		if servers := runningServers(t, cli); len(servers) != 0 {
			t.Fatalf("after the idle stop the servers are %+v, want none", servers)
		}
	}

	shown := showUntilReady()
	if got := prepared(); got != 1 {
		t.Fatalf("the server started after the prepare ran %d times, want once", got)
	}
	closeAndStop(shown)
	shown = showUntilReady()
	if got := prepared(); got != 1 {
		t.Errorf("a start with the prepare's inputs unchanged ran it again (%d runs), want it left alone", got)
	}
	closeAndStop(shown)
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module calc\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	showUntilReady()
	if got := prepared(); got != 2 {
		t.Errorf("a start after go.mod changed ran the prepare %d times in all, want twice", got)
	}
}

func TestAServerThatRejectsInitializeFailsAtOnceWithItsReasonAndLeavesNoProcess(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	linkFakeLanguageServer(t, w, cli)
	if _, err := cli.CodeNavDefine(repo, "go", `{"command": ["gopls"], "extensions": [".go"], "settings": {"reject": "unknown option: staticcheck"}}`); err != nil {
		t.Fatalf("nav set: %v", err)
	}
	document := w.Path("reviews", "calc.md")
	if err := os.MkdirAll(filepath.Dir(document), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(document, []byte("---\nattn: review\nrepository: "+repo+"\nbase: HEAD~1\nhead: HEAD\n---\n# Calc\n\n### calc.go:3-6\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var pids []int
	for press := 1; press <= 3; press++ {
		asked := requestCodeNav(app, document, "use.go", 4, 9, "definition")
		if asked.Success || !strings.Contains(protocol.Deref(asked.Error), "unknown option: staticcheck") {
			t.Fatalf("question %d answered %+v, want the server's initialize error", press, asked)
		}
		for _, server := range runningServers(t, cli) {
			pids = append(pids, int(server.Pid))
		}
	}
	for _, pid := range pids {
		if !processGone(pid) {
			t.Errorf("the server %d that rejected initialize still runs; a failed server must have exited (servers seen: %v)", pid, pids)
		}
	}
}

func requestCodeNavSetup(app *testworld.Peer, setup protocol.CodeNavSetupMessage) protocol.CodeNavSetupResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	setup.Cmd, setup.RequestID = protocol.CmdCodeNavSetup, protocol.Ptr(id)
	return testworld.Request(app, setup, protocol.EventCodeNavSetupResult, func(r protocol.CodeNavSetupResultMessage) bool { return protocol.Deref(r.RequestID) == id })
}

func TestTheSetupKeyStartsAnAgentWithTheBriefAndAPassingNavCheckMakesTheShownReviewReady(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD~1"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	appendReviewBody(t, document, "# Name the sum\n\n## Twice uses Add\n\n### use.go:3-5\n")
	if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	shown := awaitNavigation(app, document, "go", "missing")
	missing := navigation(shown.Review, "go")
	if missing.File != "use.go" || !strings.Contains(protocol.Deref(missing.Detail), "gopls") {
		t.Fatalf("a review with no gopls shows go as %+v, want missing because of gopls, for use.go", missing)
	}

	w.ExitAtNextBoot(1, "Error: not logged in")
	refused := requestCodeNavSetup(app, protocol.CodeNavSetupMessage{DesktopID: shown.DesktopID, Document: document, Repository: shown.Review.Repository, Language: "go", File: missing.File, State: missing.State, Detail: missing.Detail})
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "before its first turn") || !strings.Contains(protocol.Deref(refused.Error), "not logged in") {
		t.Errorf("a setup whose agent exits at launch answered %+v (error %s), want the launch failure with the agent's screen", refused, protocol.Deref(refused.Error))
	}

	setup := requestCodeNavSetup(app, protocol.CodeNavSetupMessage{DesktopID: shown.DesktopID, Document: document, Repository: shown.Review.Repository, Language: "go", File: missing.File, State: missing.State, Detail: missing.Detail})
	if !setup.Success || protocol.Deref(setup.SessionID) == "" {
		t.Fatalf("setup answered %+v (error %s), want the session it started", setup, protocol.Deref(setup.Error))
	}
	session := string(protocol.Deref(setup.SessionID))
	started, err := cli.DelegationStatus(protocol.Deref(setup.RequestID))
	if err == nil {
		started, err = cli.AwaitDelegation(started, func(*protocol.DelegationOperation) {})
	}
	if err != nil || started.State != protocol.DelegationOperationStateCompleted {
		t.Fatalf("the setup delegation ended %+v (%v) failure %+v, want completed", started, err, started.Failure)
	}
	linked := testworld.Await(app, protocol.EventDesktopTileContent, func(m protocol.DesktopTileContentMessage) bool {
		return m.Path == document && string(protocol.Deref(navigation(m.Review, "go").SetupSessionID)) == session
	})
	if got := navigation(linked.Review, "go").State; got != "missing" {
		t.Errorf("while setup runs go is %s, want still missing", got)
	}
	seed := protocol.Deref(started.SeedID)
	if opening := w.Launched(session).Prompted(); !strings.Contains(opening, seed) {
		t.Errorf("the setup agent's opening does not name its seed %s:\n%s", seed, opening)
	}
	shownSeed, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatalf("seed show: %v", err)
	}
	for _, want := range []string{"Set up code navigation for go in " + shown.Review.Repository, protocol.Deref(missing.Detail), "attn skill --reference code-navigation", "attn nav check use.go:<line>"} {
		if !strings.Contains(shownSeed.Seed.Body, want) {
			t.Errorf("the setup agent's brief lacks %q:\n%s", want, shownSeed.Seed.Body)
		}
	}
	if host := protocol.Deref(sessionOfDelegate(t, w, session).Host); host != "" {
		t.Errorf("the setup agent runs on %q, want the review's machine, the home", host)
	}

	linkFakeLanguageServer(t, w, cli)
	if _, err := cli.CodeNavCheck(repo, "use.go", 4); err != nil {
		t.Fatalf("nav check: %v", err)
	}
	ready := awaitNavigation(app, document, "go", "ready")
	if got := string(protocol.Deref(navigation(ready.Review, "go").SetupSessionID)); got != session {
		t.Errorf("the ready review names setup session %q, want %q", got, session)
	}

	setSetting(t, app, "code_review_enabled", "false")
	off := requestCodeNavSetup(app, protocol.CodeNavSetupMessage{DesktopID: shown.DesktopID, Document: document, Repository: shown.Review.Repository, Language: "go", File: "use.go", State: "missing"})
	if off.Success || !strings.Contains(protocol.Deref(off.Error), "code_review_enabled") {
		t.Errorf("with code review off setup answered %+v, want a refusal naming code_review_enabled", off)
	}
}

func TestAReviewOfALanguageWithNoDefinitionOffersSetupUntilOneIsSet(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	setSetting(t, app, "code_review_enabled", "true")
	repo := reviewRepo(t)
	commitFile(t, repo, "Calc.java", "class Calc {\n  int twice(int a) {\n    return a + a;\n  }\n}\n")
	runGit(t, repo, "remote", "add", "origin", "https://example.test/calc.git")
	document := w.Path("reviews", "calc.md")
	if _, err := cli.ReviewStart(document, repo, "HEAD~1"); err != nil {
		t.Fatalf("review start: %v", err)
	}
	appendReviewBody(t, document, "# Twice\n\n## Calc twice\n\n### Calc.java:2-4\n")
	if _, err := cli.OpenMarkdown(document, "s1"); err != nil {
		t.Fatalf("open: %v", err)
	}
	shown := awaitNavigation(app, document, "java", "missing")
	if java := navigation(shown.Review, "java"); protocol.Deref(java.Detail) != "no definition" || java.File != "Calc.java" {
		t.Errorf("a review of Java shows %+v, want java missing with no definition, for Calc.java", java)
	}
	if len(shown.Review.Stops) != 1 || protocol.Deref(shown.Review.Stops[0].Language) != "java" {
		t.Errorf("the review's stops are %+v, want one java stop", shown.Review.Stops)
	}

	fake := fakeagent.InstallLanguageServer(t, testworld.FakeAgentBinary(t), w.Path("installed"))
	if _, err := cli.CodeNavDefine(repo, "java", fmt.Sprintf(`{"command": [%q], "extensions": [".java"]}`, fake)); err != nil {
		t.Fatalf("nav set: %v", err)
	}
	awaitNavigation(app, document, "java", "ready")
}

func TestNavCheckPassesOnlyOnADefinitionElsewhereThanTheAskedName(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "code_review_enabled", "true")
	repo := navigatedRepo(t)
	linkFakeLanguageServer(t, w, cli)

	if result, err := cli.CodeNavCheck(repo, "calc.go", 3); err == nil {
		t.Errorf("nav check on Add's own declaration passed with %+v, want a failure: the server only named the asked place", result.Location)
	}
	result, err := cli.CodeNavCheck(repo, "use.go", 4)
	if err != nil {
		t.Fatalf("nav check on a use of Add: %v", err)
	}
	if got := fmt.Sprintf("%s -> %s:%d", protocol.Deref(result.Name), result.Location.Path, result.Location.Line); got != "Add -> calc.go:3" {
		t.Errorf("nav check resolved %s, want Add -> calc.go:3", got)
	}
}
