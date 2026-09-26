package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func listedRows(t *testing.T, s *testworld.Stack, args ...string) []string {
	t.Helper()
	var page struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	listed := s.Attn(append([]string{"session", "list", "--json"}, args...)...)
	if listed.Code != 0 {
		t.Fatalf("session list %q exited %d: %s", args, listed.Code, listed.Stderr)
	}
	listed.JSON(t, &page)
	ids := []string{}
	for _, entry := range page.Entries {
		ids = append(ids, entry.ID)
	}
	slices.Sort(ids)
	return ids
}

func rowOf(table, id string) string {
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, id) {
			return line
		}
	}
	return ""
}

func TestSessionLedgerCommandsReadClosedSessionsAndBringThemBack(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{args: []string{"list", "--closed", "--all"}, want: []string{"--closed and --all ask for different lists"}},
		{args: []string{"list", "--limit", "-1"}, want: []string{"--limit -1 is not a number of rows"}},
		{args: []string{"list", "stray"}, want: []string{"takes no positional arguments"}},
		{args: []string{"list", "--last", "fortnight"}, want: []string{"today", "yesterday", "7d", "30d"}},
		{args: []string{"list", "--last", "7d", "--since", "2026-09-01T00:00:00Z"}, want: []string{"--last already names a window"}},
		{args: []string{"list", "--last", "7d", "--until", "2026-09-01T00:00:00Z"}, want: []string{"--last already names a window"}},
		{args: []string{"list", "--since", "yesterday"}, want: []string{"neither a date like 2026-09-05 nor an RFC3339 instant"}},
		{args: []string{"reopen"}, want: []string{"exactly one session id is required"}},
		{args: []string{"reopen", "--action", "reopen"}, want: []string{"exactly one session id is required"}},
		{args: []string{"reopen", "sess-1", "sess-2"}, want: []string{"exactly one session id is required"}},
		{args: []string{"reopen", "sess-1", "--action", "recreate_worktree"}, want: []string{
			string(protocol.SessionReopenActionReopen), string(protocol.SessionReopenActionRecreateWorktreeAndReopen),
			string(protocol.SessionReopenActionFetchRecreateAndReopen), string(protocol.SessionReopenActionStartFreshSamePlace),
			string(protocol.SessionReopenActionStartFreshElsewhere), string(protocol.SessionReopenActionStartFreshDefaultBranch),
		}},
	} {
		refused := s.Attn(append([]string{"session"}, tc.args...)...)
		if refused.Code != 2 {
			t.Errorf("session %q exited %d, want usage exit 2", tc.args, refused.Code)
		}
		requireLines(t, "session "+strings.Join(tc.args, " "), refused.Stderr, tc.want...)
	}

	s.Start()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: nil, want: "no live sessions — `attn session list --closed` reads the ones that ended"},
		{args: []string{"--closed"}, want: "no closed sessions yet"},
		{args: []string{"--all"}, want: "no sessions in the ledger yet"},
	} {
		if empty := s.Attn(append([]string{"session", "list"}, tc.args...)...); !strings.Contains(empty.Stdout, tc.want) {
			t.Errorf("session list %q on an empty daemon printed %q, want %q", tc.args, empty.Stdout, tc.want)
		}
	}

	app := s.App()
	repo := s.Path("shop")
	gitRepo(t, repo)
	source := s.Spawn(app, fakeagent.Claude, repo)
	s.Launched(source)
	started := s.Run(testworld.Invocation{Session: source, Args: []string{"delegate",
		"--brief", "Write the ledger", "--cwd", repo, "--new-worktree", "--branch", "feat/ledger", "--from", "main", "--model", "default"}})
	if started.Code != 0 {
		t.Fatalf("attn delegate exited %d: %s", started.Code, started.Stderr)
	}
	var worker delegated
	started.JSON(t, &worker)
	s.Launched(worker.SessionID).Prompted()

	scratch := s.Spawn(app, fakeagent.Claude, s.Path("blog"))
	s.Launched(scratch)
	if closed := testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: scratch},
		protocol.EventSessionCloseResult, func(r protocol.SessionCloseResultMessage) bool { return r.SessionID == scratch }); !closed.Accepted {
		t.Fatalf("closing %s was refused: %s", scratch, protocol.Deref(closed.Error))
	}
	if closed := s.Run(testworld.Invocation{Session: source, Args: []string{"agent", "close", worker.SessionID, "-m", "brief delivered"}}); closed.Code != 0 {
		t.Fatalf("attn agent close exited %d: %s", closed.Code, closed.Stderr)
	}
	if err := os.RemoveAll(worker.Directory); err != nil {
		t.Fatal(err)
	}

	live := s.Attn("session", "list").Stdout
	if header, _, _ := strings.Cut(live, "\n"); strings.Join(strings.Fields(header), " ") != "ID AGENT STATE WHEN CLOSED BY LABEL" || rowOf(live, source) == "" || rowOf(live, worker.SessionID) != "" {
		t.Errorf("session list printed:\n%s\nwant a table of the one live session", live)
	}
	closedTable := s.Attn("session", "list", "--closed").Stdout
	for id, closer := range map[string]string{worker.SessionID: source, scratch: "user"} {
		if row := strings.Fields(rowOf(closedTable, id)); len(row) < 3 || row[2] != "closed" || !slices.Contains(row[3:], closer) {
			t.Errorf("session list --closed shows %s as %q, want it closed by %s", id, row, closer)
		}
	}
	requireLines(t, "session list --closed", closedTable, worker.SessionID+" closed because: brief delivered")
	if strings.Contains(closedTable, "REOPEN") {
		t.Errorf("a list nobody asked to judge grew a REOPEN column:\n%s", closedTable)
	}

	paged := s.Attn("session", "list", "--closed", "--limit", "1").Stdout
	notice := regexp.MustCompile(`showing 1, 1 omitted, paginate with --before (\S+)`).FindStringSubmatch(paged)
	if notice == nil {
		t.Fatalf("a truncated page printed:\n%s\nwant a notice naming the --before cursor", paged)
	}
	next := listedRows(t, s, "--closed", "--limit", "1", "--before", notice[1])
	if len(next) != 1 || next[0] == notice[1] || !slices.Contains([]string{worker.SessionID, scratch}, next[0]) {
		t.Errorf("the page after %s holds %q, want the other closed session", notice[1], next)
	}
	if past := s.Attn("session", "list", "--closed", "--before", next[0]).Stdout; !strings.Contains(past, "no sessions past that page") {
		t.Errorf("paging past the last closed session printed %q", past)
	}

	judged := s.Attn("session", "list", "--all", "--reopen").Stdout
	if !strings.Contains(judged, "REOPEN") || !strings.Contains(rowOf(judged, worker.SessionID), string(protocol.SessionReopenActionRecreateWorktreeAndReopen)) {
		t.Errorf("session list --reopen printed:\n%s\nwant a REOPEN column naming what brings %s back", judged, worker.SessionID)
	}

	if got := listedRows(t, s, "--all", "--workspace", "workspace-blog"); !slices.Equal(got, []string{scratch}) {
		t.Errorf("session list --workspace workspace-blog = %q, want only %s", got, scratch)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := listedRows(t, s, "--all", "--repository", canonicalRepo); !slices.Equal(got, slices.Sorted(slices.Values([]string{source, worker.SessionID}))) {
		t.Errorf("session list --repository %s = %q, want the sessions that ran in it", repo, got)
	}
	tomorrow := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	if got := listedRows(t, s, "--all", "--until", tomorrow); len(got) != 3 {
		t.Errorf("session list --until tomorrow = %q, want all three sessions", got)
	}
	if later := s.Attn("session", "list", "--all", "--since", tomorrow).Stdout; !strings.Contains(later, "no sessions match those filters") {
		t.Errorf("session list --since tomorrow printed %q", later)
	}

	shown := s.Attn("session", "show", worker.SessionID).Stdout
	requireLines(t, "session show", shown,
		"state      closed", "branch     feat/ledger", "worktree   yes, of "+repo, "by "+source, "because    brief delivered",
		"reopen     no: its directory no longer exists; branch feat/ledger is still here, so the worktree can be put back",
		"place      directory missing, branch local",
		"attn session reopen "+worker.SessionID+" --action "+string(protocol.SessionReopenActionRecreateWorktreeAndReopen))

	reopened := s.Attn("session", "reopen", worker.SessionID, "--action", string(protocol.SessionReopenActionRecreateWorktreeAndReopen))
	if reopened.Code != 0 {
		t.Fatalf("session reopen exited %d: %s", reopened.Code, reopened.Stderr)
	}
	requireLines(t, "session reopen", reopened.Stdout, "recreated worktree "+worker.Directory, worker.SessionID+" reopened in "+worker.Directory)
	if _, err := os.Stat(worker.Directory); err != nil {
		t.Errorf("the reopen reported a recreated worktree it did not create: %v", err)
	}
	s.Launched(worker.SessionID)

	if err := os.RemoveAll(s.Path("blog")); err != nil {
		t.Fatal(err)
	}
	elsewhere := s.Path("elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	moved := s.Attn("session", "reopen", scratch, "--action", string(protocol.SessionReopenActionStartFreshElsewhere), "--cwd", elsewhere)
	if moved.Code != 0 {
		t.Fatalf("session reopen --cwd exited %d: %s", moved.Code, moved.Stderr)
	}
	requireLines(t, "session reopen --cwd", moved.Stdout, scratch+" reopened in "+elsewhere)
	s.Launched(scratch)
	if again := s.Attn("session", "reopen", worker.SessionID); again.Code != 0 || !strings.Contains(again.Stdout, worker.SessionID+" is already running") {
		t.Errorf("reopening a live session exited %d with %q, want it reported as running", again.Code, again.Stdout+again.Stderr)
	}
}

func TestSessionListWindowKeepsALiveSessionWhenTheDaemonRunsOutsideUTC(t *testing.T) {
	t.Parallel()
	const zone = "America/Los_Angeles"
	if _, err := time.LoadLocation(zone); err != nil {
		t.Fatalf("this machine has no tzdata for %s, so the daemon would run in UTC: %v", zone, err)
	}
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "TZ="+zone)
	s.Start()

	since := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	live := s.Spawn(s.App(), fakeagent.Claude, s.Path("shop"))
	s.Launched(live)
	until := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)

	if got := listedRows(t, s, "--since", since, "--until", until); !slices.Equal(got, []string{live}) {
		t.Errorf("session list --since %s --until %s = %q, want the session seen inside that window", since, until, got)
	}
}
