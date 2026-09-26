package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/fakeagent"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestPiReceivesTheEffectiveAutoModeConfigAtSpawn(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	awaitAgentAvailable(app, string(fakeagent.Pi))

	if !promoteProposal(app, proposeAmendment(t, cli, automode.KindRule, ruleValue(t, automode.DecisionAllow, "", "git", "push"), "").ID).Success {
		t.Fatal("promoting the rule failed")
	}
	if _, err := cli.AutoModeEnvSlot("remote_targets", []string{"payments-prod"}); err != nil {
		t.Fatalf("set the remote targets: %v", err)
	}

	plain := w.Launched(w.Spawn(app, fakeagent.Pi, w.Path("scratch")))
	cfg := launchedAutoModeConfig(t, plain)
	if got := autoModeRuleLines(automode.StripShippedRules(cfg.Rules)); !slices.Equal(got, []string{"allow git push"}) {
		t.Errorf("rules beside the shipped ones = %v, want the promoted one", got)
	}
	if got := cfg.Environment.Slots["remote_targets"]; !slices.Equal(got, []string{"payments-prod"}) {
		t.Errorf("remote_targets = %v", got)
	}
	if !cfg.EnabledDefault || cfg.ApprovalPolicy != automode.PolicyOnRequest || cfg.SandboxMode != automode.SandboxWorkspaceWrite {
		t.Errorf("enabled %t, policy %q/%q, want the defaults", cfg.EnabledDefault, cfg.ApprovalPolicy, cfg.SandboxMode)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(plain.AutoMode, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"enabled_default", "approval_policy", "sandbox_mode", "rules", "network", "environment", "legacy_patterns"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("the auto mode config pi received has no %q", key)
		}
	}

	repo := w.Path("widgets")
	autoModeGitRepo(t, repo, "git@github.com:acme/widgets.git")
	writeAutoModeRepositoryRules(t, repo, `{"rules":[{"pattern":["go","test"],"decision":"allow","sandbox":"bypass"}]}`)
	cfg = launchedAutoModeConfig(t, w.Launched(w.Spawn(app, fakeagent.Pi, repo)))
	if got := autoModeRuleLines(automode.StripShippedRules(cfg.Rules)); !slices.Contains(got, "allow git push") || !slices.Contains(got, "allow go test") {
		t.Errorf("rules beside the shipped ones = %v, want the promoted rule and the repository's", got)
	}
	if i := slices.IndexFunc(cfg.Rules, func(r automode.Rule) bool { return r.Describe() == "go test" }); i < 0 || cfg.Rules[i].Sandbox != automode.RuleSandboxBypass {
		t.Errorf("the repository rule lost its sandbox bypass: %+v", cfg.Rules)
	}
	if got := cfg.Environment.Slots["trusted_repo"]; len(got) != 2 || got[1] != "github.com/acme/widgets" {
		t.Errorf("trusted_repo = %v, want the repository root and its origin identity", got)
	}
	if got := cfg.Environment.Slots["repo_visibility"]; len(got) != 0 {
		t.Errorf("repo_visibility = %v before any lookup answered", got)
	}

	broken := w.Path("broken")
	autoModeGitRepo(t, broken, "")
	writeAutoModeRepositoryRules(t, broken, `{"rules":[{"pattern":[]}]}`)
	refused := refuseSpawnLikeTheApp(w, app, fakeagent.Pi, broken)
	rulesPath := filepath.Join(attngit.CanonicalizePath(broken), automode.RepositoryRulesFile)
	if !strings.Contains(protocol.Deref(refused.Error), rulesPath) || !strings.Contains(protocol.Deref(refused.Error), "rule 1") {
		t.Errorf("spawning over invalid repository rules = %+v, want it to name %s and rule 1", refused, rulesPath)
	}

	if _, err := cli.AutoModeEnvSlot("trusted_repo", []string{"github.com/acme/only-this"}); err != nil {
		t.Fatalf("set the trusted repo: %v", err)
	}
	cfg = launchedAutoModeConfig(t, w.Launched(w.Spawn(app, fakeagent.Pi, repo)))
	if got := cfg.Environment.Slots["trusted_repo"]; !slices.Equal(got, []string{"github.com/acme/only-this"}) {
		t.Errorf("trusted_repo = %v, want only what the user named", got)
	}
}

func TestAPiSessionKeepsItsAutoModeChoiceAndPolicyPairWhenRespawned(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app := w.App()
	awaitAgentAvailable(app, string(fakeagent.Pi))

	for _, tc := range []struct {
		name            string
		autoMode        *bool
		policy, sandbox string
		yolo            bool
		wantEnabled     bool
		wantPair        string
	}{
		{name: "off", autoMode: protocol.Ptr(false), wantPair: "on-request/workspace-write"},
		{name: "on", autoMode: protocol.Ptr(true), wantEnabled: true, wantPair: "on-request/workspace-write"},
		{name: "unchosen", wantEnabled: true, wantPair: "on-request/workspace-write"},
		{name: "pair", policy: automode.PolicyUntrusted, sandbox: automode.SandboxReadOnly, wantEnabled: true, wantPair: "untrusted/read-only"},
		{name: "policy-only", policy: automode.PolicyNever, wantEnabled: true, wantPair: "never/workspace-write"},
		{name: "sandbox-only", sandbox: automode.SandboxDangerFullAccess, wantEnabled: true, wantPair: "on-request/danger-full-access"},
		{name: "yolo", yolo: true, policy: automode.PolicyUntrusted, sandbox: automode.SandboxReadOnly, wantEnabled: true, wantPair: "never/danger-full-access"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := w.Spawn(app, fakeagent.Pi, w.Path(tc.name), func(m *protocol.SpawnSessionMessage) {
				m.AutoMode = tc.autoMode
				if tc.policy != "" {
					m.ApprovalPolicy = protocol.Ptr(tc.policy)
				}
				if tc.sandbox != "" {
					m.SandboxMode = protocol.Ptr(tc.sandbox)
				}
				if tc.yolo {
					m.YoloMode = protocol.Ptr(true)
				}
			})
			check := func(when string, run *fakeagent.Run) {
				t.Helper()
				cfg := launchedAutoModeConfig(t, run)
				if pair := cfg.ApprovalPolicy + "/" + cfg.SandboxMode; cfg.EnabledDefault != tc.wantEnabled || pair != tc.wantPair {
					t.Errorf("%s: auto mode %t with %s, want %t with %s", when, cfg.EnabledDefault, pair, tc.wantEnabled, tc.wantPair)
				}
				if run.Yolo {
					t.Errorf("%s: pi never advertised yolo but was handed the yolo flag", when)
				}
			}
			check("at spawn", w.Launched(session))

			app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
			testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
			reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: session, Cols: 100, Rows: 30},
				protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == session })
			if !reloaded.Success {
				t.Fatalf("respawning %s: %s", session, protocol.Deref(reloaded.Error))
			}
			check("respawned", w.Launched(session))
		})
	}
}

func TestASpawnWithAPolicyPairTheAgentCannotHonourIsRefused(t *testing.T) {
	w := newWorld(t, fakeagent.Pi, fakeagent.Codex)
	app := w.App()
	awaitAgentAvailable(app, string(fakeagent.Pi))

	for _, tc := range []struct {
		name            string
		agent           fakeagent.Harness
		policy, sandbox string
		want            []string
	}{
		{name: "unknown-policy", agent: fakeagent.Pi, policy: "sometimes",
			want: []string{`approval policy "sometimes" is not one of`, "untrusted, on-request, never"}},
		{name: "unknown-sandbox", agent: fakeagent.Pi, sandbox: "read-write",
			want: []string{`sandbox mode "read-write" is not one of`, "read-only, workspace-write, danger-full-access"}},
		{name: "no-auto-mode", agent: fakeagent.Codex, policy: automode.PolicyNever,
			want: []string{`agent "codex" does not support a per-session approval policy or sandbox mode`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused := refuseSpawnLikeTheApp(w, app, tc.agent, w.Path(tc.name), func(m *protocol.SpawnSessionMessage) {
				if tc.policy != "" {
					m.ApprovalPolicy = protocol.Ptr(tc.policy)
				}
				if tc.sandbox != "" {
					m.SandboxMode = protocol.Ptr(tc.sandbox)
				}
			})
			for _, want := range tc.want {
				if !strings.Contains(protocol.Deref(refused.Error), want) {
					t.Errorf("refusal %q does not name %q", protocol.Deref(refused.Error), want)
				}
			}
		})
	}
}

func launchedAutoModeConfig(t *testing.T, run *fakeagent.Run) automode.Config {
	t.Helper()
	if len(run.AutoMode) == 0 {
		t.Fatalf("pi for session %s was launched without an auto mode config", run.SessionID)
	}
	var cfg automode.Config
	if err := json.Unmarshal(run.AutoMode, &cfg); err != nil {
		t.Fatalf("decode the auto mode config pi received: %v", err)
	}
	return cfg
}

func autoModeRuleLines(rules []automode.Rule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.Decision+" "+rule.Describe())
	}
	return out
}

func refuseSpawnLikeTheApp(w *world, app *testworld.Peer, agent fakeagent.Harness, cwd string, opts ...func(*protocol.SpawnSessionMessage)) protocol.SpawnResultMessage {
	app.T.Helper()
	refused, workspaceID, paneID := w.RequestSpawn(app, agent, cwd, opts...)
	if refused.Success {
		app.T.Fatalf("spawning %s in %s was accepted", agent, cwd)
	}
	closed := testworld.Request(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	}, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
		return r.Action == protocol.CmdWorkspaceLayoutClosePane && protocol.Deref(r.PaneID) == paneID
	})
	if !closed.Success {
		app.T.Fatalf("closing the pane of the refused spawn: %s", protocol.Deref(closed.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool {
		return e.Workspace.ID == workspaceID
	})
	view := w.App().Initial
	if slices.ContainsFunc(view.Sessions, func(s protocol.Session) bool { return s.ID == refused.ID }) {
		app.T.Errorf("the refused spawn %s left a session behind", refused.ID)
	}
	for _, workspace := range view.Workspaces {
		if workspace.Layout == nil {
			continue
		}
		if slices.ContainsFunc(workspace.Layout.Panes, func(p protocol.WorkspaceLayoutPane) bool { return protocol.Deref(p.SessionID) == refused.ID }) {
			app.T.Errorf("the refused spawn %s left a pane in workspace %s", refused.ID, workspace.ID)
		}
	}
	return refused
}

func autoModeGitRepo(t *testing.T, dir, origin string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q", "-b", "main")
	if origin != "" {
		runGit(t, dir, "remote", "add", "origin", origin)
	}
}

func writeAutoModeRepositoryRules(t *testing.T, repo, rules string) {
	t.Helper()
	path := filepath.Join(repo, automode.RepositoryRulesFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
}
