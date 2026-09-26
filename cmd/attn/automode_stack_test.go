package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type autoModeProposed struct {
	Proposal struct {
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	} `json:"proposal"`
}

type autoModeDenials struct {
	Denials []struct {
		CreatedAt string `json:"created_at"`
		SessionID string `json:"session_id"`
		Rule      string `json:"rule"`
		Signature string `json:"signature"`
		Reason    string `json:"reason"`
	} `json:"denials"`
}

func awaitAgentAvailable(app *testworld.Peer, agent fakeagent.Harness) {
	app.T.Helper()
	key := string(agent) + "_available"
	if app.Initial.Settings[key] == "true" {
		return
	}
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings[key] == "true" })
}

func requireInOrder(t *testing.T, what, line string, fields ...string) {
	t.Helper()
	rest := line
	for _, field := range fields {
		i := strings.Index(rest, field)
		if i < 0 {
			t.Errorf("%s does not show %q after the fields before it:\n%s", what, field, line)
			return
		}
		rest = rest[i+len(field):]
	}
}

func TestAutoModeRecordsRuleProposalsFromTheirTokensAndListsDenials(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Pi))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"denials", "--limit", "0"}, want: `automode denials: --limit wants a positive number, got "0"`},
		{args: []string{"denials", "--limit=many"}, want: `automode denials: --limit wants a positive number, got "many"`},
		{args: []string{"denials", "5"}, want: `automode denials: unexpected argument "5"`},
		{args: []string{"rule", "add", "--json"}, want: "automode rule add: name the command tokens"},
		{args: []string{"rule", "add", "--decision", "prompt"}, want: "automode rule add: name the command tokens"},
		{args: []string{"rule", "add", "--sandbox", "bypass"}, want: "automode rule add: name the command tokens"},
	} {
		refused := s.Attn(append([]string{"automode"}, tc.args...)...)
		if refused.Code != 2 || refused.Stdout != "" || !strings.HasPrefix(refused.Stderr, tc.want) {
			t.Errorf("attn automode %s exited %d with stderr %q, want 2 and %q", strings.Join(tc.args, " "), refused.Code, refused.Stderr, tc.want)
		}
	}

	s.Start()
	requireStdout(t, s.Attn("automode", "denials"), "no denials recorded\n")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--json", "git", "push", "origin"}, want: "allow, bypass sandbox: git push origin"},
		{args: []string{"git", "fetch", "origin", "--json"}, want: "allow, bypass sandbox: git fetch origin"},
		{args: []string{"--json", "--decision", "prompt", "--justification", "rewrites history", "git", "rebase"}, want: "prompt, inherit sandbox: git rebase"},
		{args: []string{"--json", "--sandbox=inherit", "cargo", "test"}, want: "allow, inherit sandbox: cargo test"},
	} {
		var proposed autoModeProposed
		s.Attn(append([]string{"automode", "rule", "add"}, tc.args...)...).JSON(t, &proposed)
		if proposed.Proposal.Kind != "rule" || proposed.Proposal.Summary != tc.want {
			t.Errorf("attn automode rule add %s proposed %+v, want a rule %q", strings.Join(tc.args, " "), proposed.Proposal, tc.want)
		}
	}
	recorded := s.Attn("automode", "rule", "add", "--sandbox", "inherit", "go", "vet")
	requireStdout(t, recorded, "recorded proposal ", ": rule allow, inherit sandbox: go vet\n",
		"This changed nothing yet. Promote it in the attn app to put it in force.\n")
	shown := s.Run(testworld.Invocation{Args: []string{"automode", "show"}, Dir: s.Dir})
	requireStdout(t, shown, "pending proposals (promote them in the attn app):\n",
		"allow, bypass sandbox: git push origin", "allow, bypass sandbox: git fetch origin",
		"prompt, inherit sandbox: git rebase", "allow, inherit sandbox: cargo test", "allow, inherit sandbox: go vet")

	app := s.App()
	awaitAgentAvailable(app, fakeagent.Pi)
	fetcher := s.Spawn(app, fakeagent.Pi, s.Path("shop"))
	pusher := s.Spawn(app, fakeagent.Pi, s.Path("blog"))
	s.Launched(fetcher).Deny(fakeagent.Denial{Tool: "bash", Action: "bash: curl https://example.com", Reason: "the user never asked to reach that host", Rule: "classifier-2a"})
	s.Launched(pusher).Deny(fakeagent.Denial{Tool: "bash", Action: "bash: git push --force", Reason: "force pushes rewrite shared history", Rule: "classifier-2a"})

	var listed autoModeDenials
	s.Attn("automode", "denials", "--json").JSON(t, &listed)
	if len(listed.Denials) != 2 || listed.Denials[0].SessionID != pusher || listed.Denials[1].SessionID != fetcher {
		t.Fatalf("automode denials --json = %+v, want both denials newest first", listed.Denials)
	}
	table := s.Attn("automode", "denials")
	requireStdout(t, table)
	rows := strings.Split(strings.TrimSuffix(table.Stdout, "\n"), "\n")
	if len(rows) != 2 {
		t.Fatalf("automode denials printed %d lines, want a row per denial:\n%s", len(rows), table.Stdout)
	}
	for i, d := range listed.Denials {
		requireInOrder(t, "denial row", rows[i], d.CreatedAt, d.SessionID, d.Rule, d.Signature, d.Reason)
	}

	spaced := s.Attn("automode", "denials", "--limit", "1")
	joined := s.Attn("automode", "denials", "--limit=1")
	requireStdout(t, spaced, pusher)
	if strings.Contains(spaced.Stdout, fetcher) || joined.Code != 0 || joined.Stdout != spaced.Stdout {
		t.Errorf("--limit 1 printed:\n%s\n--limit=1 exited %d and printed:\n%s\nwant the newest denial alone from both", spaced.Stdout, joined.Code, joined.Stdout)
	}
}
