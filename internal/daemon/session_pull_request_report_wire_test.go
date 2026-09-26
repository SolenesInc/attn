package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAReportedPullRequestLandsOnItsSessionOnceUntilItIsForgotten(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	panes := spawnPanes(w, w.App(), w.Path("s1"), w.Path("s2"))
	s1, s2 := panes[0].session, panes[1].session
	app := w.App()
	const url = "https://github.com/victorarias/attn/pull/71"

	recordPullRequest(t, cli, s1, url)
	recorded := testworld.AwaitSession(app, s1, func(s protocol.Session) bool { return len(s.PullRequests) == 1 }).PullRequests[0]
	if recorded.Repository != "github.com/victorarias/attn" || recorded.Number != 71 || recorded.State != "open" {
		t.Errorf("the recorded pull request = %+v, want github.com/victorarias/attn#71 open", recorded)
	}
	if recorded.CIStatus != nil || recorded.ReviewStatus != nil || recorded.StatusFetchedAt != nil {
		t.Errorf("the recorded pull request = %+v, want no status before GitHub is asked", recorded)
	}
	recordPullRequest(t, cli, s1, url)
	for _, refused := range []struct {
		name, session, url string
		wants              []string
	}{
		{"a url that is not a pull request", s1, "https://github.com/victorarias/attn/issues/12", []string{"pull request url"}},
		{"a session the daemon never heard of", "typo", url, []string{"unknown session", "typo"}},
	} {
		err := cli.RecordPullRequestCreated(refused.session, refused.url)
		for _, want := range refused.wants {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s = %v, want a refusal naming %q", refused.name, err, want)
			}
		}
	}
	recordPullRequest(t, cli, s2, "https://ghe.example.test/acme/widget/pull/12")
	testworld.AwaitSession(app, s2, func(s protocol.Session) bool {
		return len(s.PullRequests) == 1 && s.PullRequests[0].Repository == "ghe.example.test/acme/widget"
	})

	if err := cli.ForgetSessionPullRequest(s1, url); err != nil {
		t.Fatalf("forget: %v", err)
	}
	testworld.AwaitSession(app, s1, func(s protocol.Session) bool { return len(s.PullRequests) == 0 })
	if updates := sessionUpdatesOf(app, s1); len(updates) != 2 {
		t.Errorf("s1 reached the app in %d updates, want one for the report and one for the forget", len(updates))
	}
	if err := cli.ForgetSessionPullRequest(s1, url); err == nil || !strings.Contains(err.Error(), "no pull request") {
		t.Errorf("forgetting twice = %v, want it to say there is no such pull request", err)
	}

	seen := map[string][]int{}
	for _, session := range w.App().Initial.Sessions {
		seen[session.ID] = pullNumbers(session)
	}
	if len(seen[s1]) != 0 || !slices.Equal(seen[s2], []int{12}) {
		t.Errorf("a new app sees pull requests %v, want only s2's #12", seen)
	}
}

func TestPullRequestCommandsAHubForwardsLandOnTheOwningSession(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	s1 := spawnPanes(w, w.App(), w.Path("s1"))[0].session
	app := w.App()
	token, err := os.ReadFile(filepath.Join(w.Dir, config.ClientTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	hubPeer := w.Connect(protocol.ClientHelloMessage{
		Cmd: protocol.CmdClientHello, ClientKind: "hub", Version: "protocol-" + protocol.ProtocolVersion,
		Capabilities: []string{protocol.CapabilityWorkspaceSessions}, ClientToken: protocol.Ptr(strings.TrimSpace(string(token))),
	}, nil)
	testworld.Await[protocol.InitialStateMessage](hubPeer, protocol.EventInitialState, nil)
	const url = "https://github.com/victorarias/attn/pull/71"

	hubPeer.Send(protocol.PullRequestCreatedMessage{Cmd: protocol.CmdPullRequestCreated, ID: s1, URL: url})
	testworld.AwaitSession(app, s1, func(s protocol.Session) bool { return len(s.PullRequests) == 1 && s.PullRequests[0].Number == 71 })
	hubPeer.Send(protocol.PullRequestForgetMessage{Cmd: protocol.CmdPullRequestForget, ID: s1, URL: url})
	testworld.AwaitSession(app, s1, func(s protocol.Session) bool { return len(s.PullRequests) == 0 })
}
