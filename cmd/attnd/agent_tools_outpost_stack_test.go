package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type outpostTools struct {
	t             *testing.T
	home, outpost *testworld.Stack
	pair          *testworld.OutpostPair
	remote        *testworld.Peer
	seed          string
}

func newOutpostTools(t *testing.T, harness fakeagent.Harness) *outpostTools {
	t.Helper()
	home, outpost := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(harness))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	o := &outpostTools{t: t, home: home, outpost: outpost, pair: pair, remote: outpost.App()}
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	o.setTools("true")
	o.seed = plant(t, home, "Work done on the outpost").ID
	return o
}

func (o *outpostTools) setTools(value string) {
	o.t.Helper()
	mark := o.remote.EventCount()
	requireStdout(o.t, o.home.Attn("settings", "set", "agent_tools_enabled", value), value)
	testworld.AwaitSince(o.remote, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["agent_tools_enabled"] == value
	})
}

func (o *outpostTools) spawn(harness fakeagent.Harness, dir string) (string, *fakeagent.Run) {
	o.t.Helper()
	id := o.home.Spawn(o.pair.App, harness, dir, func(m *protocol.SpawnSessionMessage) { m.Host = &o.pair.Host })
	return id, o.outpost.LaunchedCarrying(o.pair.App.Terminal(id))
}

func (o *outpostTools) newestNote() protocol.SeedNote {
	o.t.Helper()
	shown := showSeed(o.t, o.home, "", o.seed)
	if len(shown.Notes) == 0 {
		o.t.Fatalf("seed %s has no notes at the home", o.seed)
	}
	return shown.Notes[0]
}

func (o *outpostTools) requireNoteBy(session, text string) {
	o.t.Helper()
	if note := o.newestNote(); note.Body != text || stackAuthorSession(note) != protocol.SessionID(session) {
		o.t.Fatalf("the home's newest note on %s is %q by %s, want %q by %s", o.seed, note.Body, stackAuthorSession(note), text, session)
	}
}

func (o *outpostTools) restartOutpost() {
	o.t.Helper()
	mark := len(o.pair.App.Log())
	o.outpost.Stop()
	outpostReachSince(o.pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
	mark = o.pair.App.EventCount()
	o.outpost.Start()
	stopRetries := o.home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	outpostReachSince(o.pair.App, mark, "outpost", protocol.OutpostReachReachable)
}

type toolEndpoint struct{ url, bearer string }

type toolAnswer struct {
	IsError    bool
	Text       string
	Structured json.RawMessage
}

func claudeToolEndpoint(t *testing.T, claude *fakeagent.Run) toolEndpoint {
	t.Helper()
	var raw string
	for i, arg := range claude.Argv {
		if arg == "--mcp-config" && i+1 < len(claude.Argv) {
			raw = claude.Argv[i+1]
		}
	}
	var config struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("claude launched as %q, want --mcp-config with the attn server: %v", claude.Argv, err)
	}
	server := config.MCPServers["attn"]
	authorization := os.Expand(server.Headers["Authorization"], func(name string) string {
		for _, entry := range claude.Env {
			if key, value, _ := strings.Cut(entry, "="); key == name {
				return value
			}
		}
		return ""
	})
	bearer, _ := strings.CutPrefix(authorization, "Bearer ")
	if server.URL == "" || bearer == "" {
		t.Fatalf("--mcp-config %s with Authorization %q names no attn server with a credential", raw, authorization)
	}
	return toolEndpoint{url: server.URL, bearer: bearer}
}

func (e toolEndpoint) at(stack *testworld.Stack) toolEndpoint {
	return toolEndpoint{url: "http://" + stack.WSAddr + "/mcp", bearer: e.bearer}
}

func (e toolEndpoint) post(t *testing.T, tool string, args map[string]any) (int, []byte) {
	t.Helper()
	status, reply, err := e.send(tool, args)
	if err != nil {
		t.Fatal(err)
	}
	return status, reply
}

func (e toolEndpoint) send(tool string, args map[string]any) (int, []byte, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	reply, err := io.ReadAll(resp.Body)
	return resp.StatusCode, reply, err
}

func (e toolEndpoint) call(t *testing.T, command string, args map[string]any) toolAnswer {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	status, reply := e.post(t, "attn_do", map[string]any{"script": fmt.Sprintf(`try { return JSON.stringify({returned: %s(%s)}) } catch (e) {
	if (!(e instanceof Refusal)) throw e;
	return JSON.stringify({refused: e.code + ": " + e.message + (e.fix ? "\nFix: " + e.fix : "")});
}`, command, encoded)})
	var answer struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(reply, &answer); status != http.StatusOK || err != nil || len(answer.Result.Content) != 1 {
		t.Fatalf("%s at %s answered %d %q (%v)", command, e.url, status, reply, err)
	}
	text := answer.Result.Content[0].Text
	if answer.Result.IsError {
		return toolAnswer{IsError: true, Text: text}
	}
	var ran struct {
		Returned json.RawMessage
		Refused  string
	}
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&ran); err != nil {
		t.Fatalf("a script calling %s at %s answered %q: %v", command, e.url, text, err)
	}
	if ran.Refused != "" {
		return toolAnswer{IsError: true, Text: ran.Refused}
	}
	result := toolAnswer{Text: string(ran.Returned), Structured: ran.Returned}
	_ = json.Unmarshal(ran.Returned, &result.Text)
	return result
}

func (e toolEndpoint) structured(t *testing.T, tool string, args map[string]any, into any) {
	t.Helper()
	answer := e.call(t, tool, args)
	if answer.IsError {
		t.Fatalf("%s %v refused: %s", tool, args, answer.Text)
	}
	if err := json.Unmarshal(answer.Structured, into); err != nil {
		t.Fatalf("%s answered %s: %v", tool, answer.Structured, err)
	}
}

func TestAClaudeOnAnOutpostRunsHomeToolsAtTheHomeAsItsSessionAndHereToolsWithoutTheBridge(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	shop := o.outpost.Path("shop")
	gitRepo(t, shop)
	sub := filepath.Join(shop, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	head, err := exec.Command("git", "-C", shop, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	full := strings.TrimSpace(string(head))
	session, claude := o.spawn(fakeagent.Claude, shop)
	tools := claudeToolEndpoint(t, claude)
	if want := "http://" + o.outpost.WSAddr + "/mcp"; tools.url != want {
		t.Fatalf("the outpost's claude was given the tools at %s, want %s, its own daemon's", tools.url, want)
	}

	var noted protocol.SeedNoteResult
	tools.structured(t, "seed_note", map[string]any{"seed": o.seed, "text": "Noted through the outpost's tools"}, &noted)
	o.requireNoteBy(session, "Noted through the outpost's tools")

	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(shop, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.md", "the plan\n")
	var attached, retried protocol.SeedArtifactResult
	tools.structured(t, "seed_attach", map[string]any{"seed": o.seed, "path": "plan.md", "mode": "move"}, &attached)
	if _, err := os.Stat(filepath.Join(shop, "plan.md")); !os.IsNotExist(err) {
		t.Errorf("plan.md is still in the outpost session's folder after a move: %v", err)
	}
	tools.structured(t, "seed_attach", map[string]any{"seed": o.seed, "path": "plan.md", "mode": "move"}, &retried)
	if protocol.Deref(retried.OperationID) != protocol.Deref(attached.OperationID) || !protocol.Deref(retried.Recovered) {
		t.Errorf("seed_attach of plan.md again after the move answered operation %q recovered %v, want the first call's operation %q recovered", protocol.Deref(retried.OperationID), protocol.Deref(retried.Recovered), protocol.Deref(attached.OperationID))
	}

	tools.structured(t, "seed_check_add", map[string]any{"seed": o.seed, "statement": "C1=Retries pass"}, &struct{}{})
	write("retry.log", "3 passes\n")
	var reported protocol.SeedCheckResult
	tools.structured(t, "seed_check_report", map[string]any{"seed": o.seed, "id": "C1", "summary": "Passed", "evidence": []any{"retry.log"}, "commit": full[:7]}, &reported)
	if len(reported.Attached) != 1 || reported.Attached[0].Filename != "retry.log" {
		t.Errorf("seed_check_report attached %+v, want retry.log copied from the outpost session's folder", reported.Attached)
	}
	reportedCommit := func() string {
		t.Helper()
		for _, check := range showSeed(t, o.home, "", o.seed).Seed.Checks {
			if check.ID == "C1" && check.Report != nil {
				return protocol.Deref(check.Report.Commit)
			}
		}
		return ""
	}
	if got := reportedCommit(); got != full {
		t.Errorf("C1's report names commit %q after the tool named %s, want %s from the outpost's repository", got, full[:7], full)
	}
	write("tool-copy.md", "copied by the tool\n")
	var copied protocol.SeedArtifactResult
	tools.structured(t, "seed_attach", map[string]any{"seed": o.seed, "path": "tool-copy.md", "mode": "copy"}, &copied)
	if protocol.Deref(copied.SourcePath) != filepath.Join(shop, "tool-copy.md") {
		t.Errorf("seed_attach copied %q, want %s from the outpost session's folder", protocol.Deref(copied.SourcePath), filepath.Join(shop, "tool-copy.md"))
	}
	if gone := tools.call(t, "seed_attach", map[string]any{"seed": o.seed, "path": "gone.md", "mode": "copy"}); !gone.IsError || !strings.Contains(gone.Text, `"gone.md" is not found in `+shop+", the folder the session started in; pass an absolute path") {
		t.Errorf("seed_attach of a missing relative file answered error=%v %q, want the hint naming %s", gone.IsError, gone.Text, shop)
	}

	write("cli-move.md", "moved by the CLI\n")
	write("cli-copy.md", "copied by the CLI\n")
	write("cli.log", "4 passes\n")
	for _, args := range [][]string{
		{"seed", "attach", o.seed, "--path", "../cli-move.md", "--move"},
		{"seed", "attach", o.seed, "--path", "../cli-copy.md", "--copy"},
		{"seed", "check", "report", o.seed, "C1", "-m", "Passed again", "--evidence", "../cli.log", "--commit", full[:9]},
	} {
		if ran := o.outpost.Run(testworld.Invocation{Args: args, Terminal: o.pair.App.Terminal(session), Dir: sub}); ran.Code != 0 {
			t.Fatalf("attn %q in %s exited %d: %s", args, sub, ran.Code, ran.Stderr)
		}
	}
	if got := reportedCommit(); got != full {
		t.Errorf("C1's report names commit %q after the CLI named %s in %s, want %s", got, full[:9], sub, full)
	}
	other := o.outpost.Path("other")
	otherHead := repoWithOwnHead(t, other)
	if ran := o.outpost.Run(testworld.Invocation{Args: []string{"seed", "check", "report", o.seed, "C1", "-m", "Passed in other", "--evidence", "https://example.test/run", "--commit", "HEAD"}, Terminal: o.pair.App.Terminal(session), Dir: sub, Env: []string{"GIT_DIR=" + filepath.Join(other, ".git")}}); ran.Code != 0 {
		t.Fatalf("attn seed check report in %s with GIT_DIR naming %s exited %d: %s", sub, other, ran.Code, ran.Stderr)
	}
	if got := reportedCommit(); got != otherHead {
		t.Errorf("C1's report names commit %q after the outpost's CLI in %s named HEAD with GIT_DIR naming %s, want %s", got, sub, other, otherHead)
	}
	for name, kept := range map[string]bool{"tool-copy.md": true, "cli-move.md": false, "cli-copy.md": true} {
		if _, err := os.Stat(filepath.Join(shop, name)); (err == nil) != kept {
			t.Errorf("%s on the outpost after its attach: %v, want it kept %v", name, err, kept)
		}
	}
	shown := showSeed(t, o.home, "", o.seed)
	for _, name := range []string{"plan.md", "retry.log", "tool-copy.md", "cli-move.md", "cli-copy.md", "cli.log"} {
		if !slices.ContainsFunc(shown.Artifacts, func(a protocol.SeedArtifact) bool { return a.Filename == name }) {
			t.Errorf("the home's seed has artifacts %+v, want %s from the outpost", shown.Artifacts, name)
		}
	}

	o.restartOutpost()
	tools.structured(t, "seed_note", map[string]any{"seed": o.seed, "text": "After the outpost restarted"}, &noted)
	o.requireNoteBy(session, "After the outpost restarted")

	disconnectOutpost(o.pair)
	guide := tools.call(t, "seed_guide", map[string]any{})
	if guide.IsError || guide.Text == "" {
		t.Errorf("seed_guide, a here tool, with the bridge down answered error=%v %q, want the guide", guide.IsError, guide.Text)
	}
	refused := tools.call(t, "seed_note", map[string]any{"seed": o.seed, "text": "With the bridge down"})
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if want := "home_unreachable: " + name + " is unreachable; this needs it now\nFix: Nothing ran. Make the same call again once " + name + " is reachable."; !refused.IsError || refused.Text != want {
		t.Errorf("seed_note with the bridge down answered error=%v %q, want %q", refused.IsError, refused.Text, want)
	}
	t.Logf("seed_note with the bridge down: %s", refused.Text)
	o.requireNoteBy(session, "After the outpost restarted")
}

const largestIntegersAScriptHolds = `{"record":{"id":9007199254740991,"parent":{"id":-9007199254740991}}}`

func TestAnAutomationRunToolOnAnOutpostGivesTheHomesAgentItsInputsIntegersExactly(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude)), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	o := &outpostTools{t: t, home: home, outpost: outpost, pair: pair, remote: outpost.App()}
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	o.setTools("true")
	shop := outpost.Path("shop")
	if err := os.MkdirAll(shop, 0o755); err != nil {
		t.Fatal(err)
	}
	_, claude := o.spawn(fakeagent.Claude, shop)
	tools := claudeToolEndpoint(t, claude)
	spec := fmt.Sprintf("api_version: attn.dev/automations/v1alpha1\nname: Check\ntrigger: {type: manual}\nprompt: Check the record.\nlaunch: {driver: claude}\nlocation: {type: directory, path: %q}\n", home.Dir)
	var applied protocol.AutomationDefinitionSummary
	tools.structured(t, "automation_apply", map[string]any{"definition": spec}, &applied)
	var run protocol.AutomationRunSummary
	tools.structured(t, "automation_run", map[string]any{"id": applied.ID, "input": json.RawMessage(largestIntegersAScriptHolds)}, &run)
	requireSameJSONNumbers(t, home.Launched(string(protocol.Deref(run.SessionID))), filepath.Join(home.Dir, "automation", "occurrences", run.ID+".json"), largestIntegersAScriptHolds)
}

func TestAnOutpostAgentsCredentialIsRefusedByTheHomeAndByAnotherOutpost(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	other := testworld.NewStack(t)
	other.Start()
	o.pair.Enroll("other", "other-outpost", other)
	_, claude := o.spawn(fakeagent.Claude, o.outpost.Path("shop"))
	tools := claudeToolEndpoint(t, claude)

	if answer := tools.call(t, "seed_show", map[string]any{"seed": o.seed}); answer.IsError {
		t.Fatalf("the outpost refused its own agent's credential: %s", answer.Text)
	}
	for name, elsewhere := range map[string]*testworld.Stack{"the home": o.home, "another outpost": other} {
		status, reply := tools.at(elsewhere).post(t, "attn_read", map[string]any{"script": "return seed_show({seed: '" + o.seed + "'})"})
		if status != http.StatusUnauthorized || !strings.Contains(string(reply), "not signed by this daemon's agent key") {
			t.Errorf("%s answered the outpost agent's credential with %d %q, want 401 naming its agent key", name, status, reply)
		}
	}
}

func TestTurningTheToolsOffAtTheHomeRefusesTheNextCallOfAnAgentRunningOnAnOutpost(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	session, claude := o.spawn(fakeagent.Claude, o.outpost.Path("shop"))
	tools := claudeToolEndpoint(t, claude)
	var noted protocol.SeedNoteResult
	tools.structured(t, "seed_note", map[string]any{"seed": o.seed, "text": "While the tools are on"}, &noted)

	o.setTools("false")
	refused := tools.call(t, "seed_note", map[string]any{"seed": o.seed, "text": "After the home turned them off"})
	if !refused.IsError || !strings.HasPrefix(refused.Text, "setting_off: ") || !strings.Contains(refused.Text, "agent_tools_enabled") {
		t.Errorf("seed_note after the home turned the tools off answered error=%v %q, want the setting's refusal", refused.IsError, refused.Text)
	}
	o.requireNoteBy(session, "While the tools are on")

	o.setTools("true")
	tools.structured(t, "seed_note", map[string]any{"seed": o.seed, "text": "On again"}, &noted)
	o.requireNoteBy(session, "On again")
}

func TestACLICommandFromASharedCodexThreadNoSessionHoldsOnAnOutpostRunsInTheThreadsProfile(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Codex)
	profile := o.pair.App.SelectedProfile()
	side := testworld.Request(o.pair.App,
		protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"},
		protocol.EventProfileActionResult,
		func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !side.Success {
		t.Fatalf("create a second profile: %+v", side)
	}
	shop := o.outpost.Path("shop")
	o.outpost.SetCodexFolderTrust(shop, "trusted")
	session, codex := o.spawn(fakeagent.Codex, shop)
	o.pair.App.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(o.pair.App, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	server := o.outpost.CodexServer()
	child := server.SpawnSubagent(codex.ConversationID)
	server.CloseThread(child)
	o.pair.App.TypeLine(session, "/rename Closed child processed")
	codex.Prompted()
	testworld.AwaitSession(o.pair.App, session, func(s protocol.Session) bool { return s.Label == "Closed child processed" })

	env := []string{"ATTN_INSIDE_APP=1", "ATTN_TERMINAL_ID=", "ATTN_SESSION_ID=", "ATTN_CODEX_SHARED_PROFILE=" + profile, "CODEX_THREAD_ID=" + child}
	var caller protocol.PresenceResult
	presence := o.outpost.Run(testworld.Invocation{Args: []string{"presence", "--json"}, Env: env})
	if presence.Code != 0 {
		t.Fatalf("presence from thread %s: %+v", child, presence)
	}
	presence.JSON(t, &caller)
	if caller.SessionID != "" {
		t.Fatalf("closed subagent %s still resolves to session %s", child, caller.SessionID)
	}
	listed := o.outpost.Run(testworld.Invocation{Args: []string{"seed", "ls"}, Env: env})
	if listed.Code != 0 || !strings.Contains(listed.Stdout, "Work done on the outpost") {
		t.Errorf("attn seed ls from thread %s, which no session holds, answered %+v; want profile %s's seed listed", child, listed, profile)
	}
}

func TestThePresenceToolOnAnOutpostNamesTheHomesSeedsAndRefusesWhileTheHomeIsDown(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	session, claude := o.spawn(fakeagent.Claude, o.outpost.Path("work"))
	tools := claudeToolEndpoint(t, claude)
	tools.structured(t, "seed_tend", map[string]any{"seed": o.seed}, &protocol.SeedTransitionResult{})
	var present protocol.PresenceResult
	tools.structured(t, "presence", map[string]any{}, &present)
	if present.SessionID != protocol.SessionID(session) || !slices.Equal(present.SeedIds, []string{o.seed}) {
		t.Fatalf("presence on the outpost answered %+v, want session %s tending %s", present, session, o.seed)
	}

	o.home.Stop()
	if answer := tools.call(t, "presence", map[string]any{}); !answer.IsError || !strings.Contains(answer.Text, "unreachable") {
		t.Errorf("presence on the outpost while the home is down answered %+v, want a refusal naming the unreachable home", answer)
	}
}

func TestTheSettingsToolOnAnOutpostSavesAHomeSettingAtTheHomeAndAMachineSettingHere(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	shop := o.outpost.Path("shop")
	if err := os.MkdirAll(shop, 0o755); err != nil {
		t.Fatal(err)
	}
	_, claude := o.spawn(fakeagent.Claude, shop)
	tools := claudeToolEndpoint(t, claude)

	var entry protocol.SettingEntry
	tools.structured(t, "settings_set", map[string]any{"key": "theme", "value": "dark"}, &entry)
	if protocol.Deref(entry.Value) != "dark" {
		t.Errorf("settings_set theme on the outpost answered %+v, want dark", entry)
	}
	requireStdout(t, o.home.Attn("settings", "get", "theme"), "dark")
	tools.structured(t, "settings_set", map[string]any{"key": "worktree_sweep_enabled", "value": "false"}, &entry)
	if protocol.Deref(entry.Value) != "false" {
		t.Errorf("settings_set worktree_sweep_enabled on the outpost answered %+v, want false", entry)
	}
	requireStdout(t, o.outpost.Attn("settings", "get", "worktree_sweep_enabled"), "false")
	if got := o.home.Attn("settings", "get", "worktree_sweep_enabled"); got.Code != 0 || strings.TrimSpace(got.Stdout) != "true" {
		t.Errorf("the outpost's machine setting reached the home: %+v", got)
	}
}
