package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/testworld"
)

const fakeGHScript = `#!/bin/sh
echo "$*" | tr '\n' ' ' >> %[1]q/calls
echo >> %[1]q/calls
case "$*" in
*PullRequestReadiness*) kind=readiness ;;
*PullRequestFeedback*) kind=feedback ;;
*) echo "unexpected query: $*" >&2; exit 1 ;;
esac
reply=%[1]q/$kind
for queued in %[1]q/$kind.[0-9]; do
  if [ -f "$queued" ]; then mv "$queued" %[1]q/$kind.used; reply=%[1]q/$kind.used; break; fi
done
if [ "$(head -c 5 "$reply")" = "FAIL " ]; then tail -c +6 "$reply" >&2; exit 1; fi
cat "$reply"
`

type fakeGitHub struct {
	t   *testing.T
	dir string
}

func installFakeGitHub(t *testing.T) fakeGitHub {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "gh"), []byte(fmt.Sprintf(fakeGHScript, dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	return fakeGitHub{t: t, dir: dir}
}

func (gh fakeGitHub) answer(file, body string) {
	gh.t.Helper()
	if err := os.WriteFile(filepath.Join(gh.dir, file), []byte(body), 0o644); err != nil {
		gh.t.Fatal(err)
	}
}

func (gh fakeGitHub) serves(state, mergeState string, feedback ...ghComment) {
	gh.t.Helper()
	gh.answer("readiness", pullRequestJSON(state, mergeState))
	gh.answer("feedback", feedbackJSON(feedback...))
	_ = os.Remove(filepath.Join(gh.dir, "calls"))
}

func (gh fakeGitHub) calls() string {
	gh.t.Helper()
	raw, _ := os.ReadFile(filepath.Join(gh.dir, "calls"))
	return string(raw)
}

func pullRequestJSON(state, mergeState string) string {
	return fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"number":7,"url":"https://github.com/acme/widgets/pull/7","state":%q,"headRefOid":"abcdef012345","mergeStateStatus":%q,"reactions":{"nodes":[]},"latestOpinionatedReviews":{"nodes":[]},"reviews":{"nodes":[]},"reviewRequests":{"nodes":[]},"commits":{"nodes":[]}}}}}`, state, mergeState)
}

type ghComment struct {
	id, author, body string
	at               time.Time
}

func feedbackJSON(comments ...ghComment) string {
	nodes := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		nodes = append(nodes, map[string]any{"id": c.id, "bodyText": c.body, "createdAt": c.at.UTC().Format(time.RFC3339), "author": map[string]string{"login": c.author}})
	}
	body, _ := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"comments": map[string]any{"nodes": nodes}, "reviews": map[string]any{"nodes": []any{}}, "reviewThreads": map[string]any{"nodes": []any{}},
	}}}})
	return string(body)
}

type prWaitJSON struct {
	Outcome     string   `json:"outcome"`
	Events      []string `json:"events"`
	Mode        string   `json:"mode"`
	State       string   `json:"state"`
	Health      string   `json:"health"`
	HealthError string   `json:"health_error"`
	Head        string   `json:"head"`
	PR          int      `json:"pr"`
}

func TestPRWaitReadyReportsEachActionableUpdateOnceAcrossInvocations(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	gh := installFakeGitHub(t)
	path := "PATH=" + filepath.Join(gh.dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	wait := func(args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: append([]string{"pr", "wait-ready"}, args...), Env: []string{path}})
	}
	now := time.Now()
	old := ghComment{id: "old", author: "ana", body: "looks fine so far", at: now.Add(-time.Hour)}
	fresh := ghComment{id: "new", author: "ben", body: "please rename the helper", at: now}

	prompttest.Equal(t, "pr-help", map[string]string{"attn pr --help": s.Attn("pr", "--help").Stdout})

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"7", "--repo", "acme/widgets", "--reviewer", "victor"}, want: "--reviewer is only valid with --mode formal-review"},
		{args: []string{"7"}, want: "--repo is required when the target is a number"},
		{args: []string{"7", "--repo", "acme/widgets", "--since", "yesterday"}, want: "--since must be an RFC3339 timestamp"},
	} {
		if refused := wait(tc.args...); refused.Code != 2 || !strings.Contains(refused.Stderr, tc.want) {
			t.Errorf("pr wait-ready %q exited %d with %q, want usage exit 2 saying %q", tc.args, refused.Code, refused.Stderr, tc.want)
		}
	}

	for _, tc := range []struct {
		args     []string
		mode     string
		hostname string
		number   string
	}{
		{args: []string{"7", "--repo", "acme/widgets"}, mode: "green", number: "7"},
		{args: []string{"https://ghe.example/acme/widgets/pull/8", "--mode", "codex"}, mode: "codex", hostname: "ghe.example", number: "8"},
		{args: []string{"9", "--repo", "ghe.example/acme/widgets", "--mode", "formal-review", "--reviewer", "victor"}, mode: "formal-review", hostname: "ghe.example", number: "9"},
	} {
		gh.serves("CLOSED", "CLEAN")
		closed := wait(append(tc.args, "--json")...)
		var out prWaitJSON
		closed.JSON(t, &out)
		if closed.Code != 5 || out.Outcome != "closed" || out.Mode != tc.mode {
			t.Errorf("pr wait-ready %q on a closed pull request exited %d with %+v, want outcome closed in %s mode", tc.args, closed.Code, out, tc.mode)
		}
		calls := gh.calls()
		if !strings.Contains(calls, "owner=acme") || !strings.Contains(calls, "name=widgets") || !strings.Contains(calls, "number="+tc.number) {
			t.Errorf("pr wait-ready %q asked gh:\n%s\nwant acme/widgets#%s", tc.args, calls, tc.number)
		}
		if asked := strings.Contains(calls, "--hostname"); asked != (tc.hostname != "") || (asked && !strings.Contains(calls, "--hostname "+tc.hostname)) {
			t.Errorf("pr wait-ready %q asked gh:\n%s\nwant hostname %q", tc.args, calls, tc.hostname)
		}
	}

	gh.serves("OPEN", "CLEAN", old)
	ready := wait("20", "--repo", "acme/widgets")
	if ready.Code != 0 || !strings.HasPrefix(ready.Stdout, "ready: GitHub reports the pull request ready") || strings.Contains(ready.Stdout, old.body) {
		t.Errorf("the first wait on a green pull request exited %d with %q, want ready and the earlier comment taken as seen", ready.Code, ready.Stdout)
	}
	gh.serves("OPEN", "CLEAN", old, fresh)
	commented := wait("20", "--repo", "acme/widgets")
	if commented.Code != 4 || !strings.HasPrefix(commented.Stdout, "comment:") || !strings.Contains(commented.Stdout, "ben: please rename the helper") || strings.Contains(commented.Stdout, old.body) || strings.Contains(commented.Stdout, "ready:") {
		t.Errorf("the next wait exited %d with %q, want only the new comment and no second ready", commented.Code, commented.Stdout)
	}

	gh.serves("OPEN", "CLEAN")
	fifo := t.TempDir()
	killed := s.Run(testworld.Invocation{
		Binary: "/bin/sh",
		Args: []string{"-c", `fifo=$1/out; shift; mkfifo "$fifo" && exec 3<>"$fifo" 4>"$fifo" 3<&- && "$@" >&4; echo "attn exited $?" >&2`,
			"sh", fifo, testworld.AttnBinary(t), "pr", "wait-ready", "21", "--repo", "acme/widgets"},
		Env: []string{path},
	})
	if !strings.Contains(killed.Stderr, "attn exited 141") {
		t.Fatalf("a wait whose reader had gone ended with %q, want it killed by SIGPIPE as it printed", killed.Stderr)
	}
	if again := wait("21", "--repo", "acme/widgets"); again.Code != 0 || !strings.HasPrefix(again.Stdout, "ready:") {
		t.Errorf("a wait after one that died printing exited %d with %q, want the unprinted ready reported again", again.Code, again.Stdout)
	}

	gh.serves("OPEN", "CLEAN")
	gh.answer("readiness", "FAIL rate limited")
	outage := wait("30", "--repo", "acme/widgets")
	if outage.Code != 5 || !strings.HasPrefix(outage.Stdout, "monitoring_outage:") || !strings.Contains(outage.Stdout, "rate limited") {
		t.Errorf("a wait GitHub refused exited %d with %q, want the outage reported", outage.Code, outage.Stdout)
	}
	gh.serves("OPEN", "CLEAN")
	gh.answer("readiness.1", "FAIL rate limited")
	recovered := wait("30", "--repo", "acme/widgets", "--interval", "10ms")
	if recovered.Code != 0 || !strings.HasPrefix(recovered.Stdout, "ready:") || strings.Contains(recovered.Stdout, "monitoring_outage") || strings.Count(gh.calls(), "PullRequestReadiness") != 2 {
		t.Errorf("a wait through the same outage exited %d with %q after asking gh:\n%s\nwant silence while GitHub still failed, then ready", recovered.Code, recovered.Stdout, gh.calls())
	}

	gh.serves("OPEN", "CLEAN")
	gh.answer("feedback", "FAIL threads unavailable")
	delayed := wait("40", "--repo", "acme/widgets", "--json")
	var out prWaitJSON
	delayed.JSON(t, &out)
	if delayed.Code != 0 || out.Outcome != "ready" || out.Mode != "green" || out.State != "ready" || out.Head != "abcdef012345" || out.PR != 7 || out.Health != "delayed" || !strings.Contains(out.HealthError, "threads unavailable") {
		t.Errorf("a wait whose feedback GitHub refused exited %d with %+v, want ready with health delayed naming the error", delayed.Code, out)
	}

	since := now.Add(-time.Minute).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name          string
		number        string
		failFirstRead bool
	}{
		{name: "feedback read at once", number: "50"},
		{name: "feedback read after a failure", number: "51", failFirstRead: true},
	} {
		gh.serves("OPEN", "BLOCKED", old, fresh)
		if tc.failFirstRead {
			gh.answer("feedback.1", "FAIL threads unavailable")
		}
		cut := wait(tc.number, "--repo", "acme/widgets", "--since", since, "--interval", "10ms")
		if cut.Code != 4 || !strings.Contains(cut.Stdout, "comment: ") || !strings.Contains(cut.Stdout, "ben: please rename the helper") || strings.Contains(cut.Stdout, old.body) {
			t.Errorf("%s: pr wait-ready --since exited %d with %q, want only the comment made after the cutoff", tc.name, cut.Code, cut.Stdout)
		}
	}
}

func TestPRSessionCommandsRecordForgetAndWatchASessionsPullRequests(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	const url = "https://github.com/acme/widgets/pull/"

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"record", url + "1"}, want: "no session"},
		{args: []string{"ls"}, want: "no session"},
		{args: []string{"record", "--session", "s1"}, want: "needs exactly one pull request url"},
		{args: []string{"forget", "--session", "s1", "a", "b"}, want: "needs exactly one pull request url"},
		{args: []string{"watch", url + "1", "--session", "s1", "--mode", "codex", "--reviewer", "victor"}, want: "--reviewer is only valid with --mode formal-review"},
	} {
		if refused := s.Attn(append([]string{"pr"}, tc.args...)...); refused.Code != 2 || !strings.Contains(refused.Stderr, tc.want) {
			t.Errorf("pr %q exited %d with %q, want usage exit 2 saying %q", tc.args, refused.Code, refused.Stderr, tc.want)
		}
	}

	s.Start()
	id := s.Spawn(s.App(), fakeagent.Claude, s.Path("shop"))
	s.Launched(id)
	for i, args := range [][]string{
		{url + "1", "--session", id},
		{"--session", id, url + "2"},
		{"--session=" + id, url + "3"},
	} {
		recorded := s.Attn(append([]string{"pr", "record"}, args...)...)
		if want := fmt.Sprintf("recorded %s%d for session %s\n", url, i+1, id); recorded.Code != 0 || recorded.Stdout != want {
			t.Errorf("pr record %q exited %d with %q, want %q", args, recorded.Code, recorded.Stdout+recorded.Stderr, want)
		}
	}
	inSession := func(args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: append([]string{"pr"}, args...), Session: id})
	}
	listed := func(args ...string) []string {
		t.Helper()
		var prs []struct {
			URL       string `json:"url"`
			WatchMode string `json:"watch_mode"`
		}
		inSession(append(args, "--json")...).JSON(t, &prs)
		var got []string
		for _, pr := range prs {
			got = append(got, strings.TrimPrefix(pr.URL, url)+pr.WatchMode)
		}
		slices.Sort(got)
		return got
	}
	if got := listed("ls"); !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Errorf("pr ls in the session = %q, want the three recorded pull requests", got)
	}
	if forgot := inSession("forget", url+"2"); forgot.Code != 0 {
		t.Fatalf("pr forget exited %d: %s", forgot.Code, forgot.Stderr)
	}
	if got := listed("ls"); !slices.Equal(got, []string{"1", "3"}) {
		t.Errorf("pr ls after forgetting 2 = %q", got)
	}
	watched := inSession("watch", url+"1", "--mode", "formal-review", "--reviewer", "victor")
	if watched.Code != 0 || !strings.Contains(watched.Stdout, "watching "+url+"1 in formal-review mode for session "+id) {
		t.Errorf("pr watch exited %d with %q", watched.Code, watched.Stdout+watched.Stderr)
	}
	if got := listed("status"); !slices.Equal(got, []string{"1formal-review"}) {
		t.Errorf("pr status = %q, want only the watched pull request in formal-review mode", got)
	}
}
