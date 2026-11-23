package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSigmaIsOfferedOnlyWhileSigmaEnabledIsOn(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	initial := testworld.Await(app, protocol.EventHarnessCatalog, func(protocol.HarnessCatalogMessage) bool { return true })
	if got, ok := testworld.FindHarness(initial.Harnesses, "sigma"); !ok || got.Offered {
		t.Fatalf("with sigma_enabled off the catalog has sigma=%+v, want offered=false", got)
	}
	refused, _, _ := w.RequestSpawn(app, fakeagent.Sigma, w.Path("shop"))
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "not available") {
		t.Fatalf("a Sigma launch with sigma_enabled off = %+v, want it refused as not available", refused)
	}

	setSetting(t, app, "sigma_enabled", "true")
	sigma := awaitHarnessOffered(app, "sigma")
	if !sigma.Options.Discovery || !sigma.Options.LaunchInstructions {
		t.Errorf("Sigma's options = %+v, want discovery and launch_instructions offered", sigma.Options)
	}
	session := w.Spawn(app, fakeagent.Sigma, w.Path("shop"))
	view := w.Launched(session)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	setSetting(t, app, "sigma_enabled", "false")
	refused, _, _ = w.RequestSpawn(app, fakeagent.Sigma, w.Path("shop"))
	if refused.Success {
		t.Fatal("a second Sigma launch after sigma_enabled was turned off started, want it refused")
	}
	if r := submitSessionAnnotationFeedbackAs(app, session, "after-off", "still there?"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback to the open Sigma session after sigma_enabled was turned off = %+v, want it delivered", r)
	}
	if got := w.AppServer(fakeagent.Sigma).Prompted(view.ConversationID).Text; got != "still there?" {
		t.Fatalf("the open Sigma conversation took %q after sigma_enabled was turned off", got)
	}
}

func TestASigmaSessionRunsSigmasCommandLine(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	session := w.Spawn(app, fakeagent.Sigma, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Model = protocol.Ptr("openai/gpt-5.5")
		m.Effort = protocol.Ptr("high")
		m.YoloMode = protocol.Ptr(true)
		m.InitialPrompt = protocol.Ptr("find the flaky checkout test")
	})
	view := w.Launched(session)
	view.Prompted()
	guidance := w.AppServer(fakeagent.Sigma).Started(view.ConversationID).Instructions
	want := []string{"--conversation", view.ConversationID, "--auto", "--provider", "openai", "--model", "gpt-5.5", "--effort", "high", "--instructions", guidance, "--", "find the flaky checkout test"}
	at := slices.Index(view.Argv, "--conversation")
	if at < 0 || guidance == "" || !slices.Equal(view.Argv[at:], want) {
		t.Fatalf("Sigma ran %q, want the arguments %q", view.Argv, want)
	}
	if got, _ := envEntry(view.Env, "SIGMA_HOME"); got == "" {
		t.Errorf("Sigma's client ran without SIGMA_HOME, want the home its server serves")
	}
}

func TestMessagesAndTheCloseInterruptReachASigmaConversationWithAnIDAndNameAuthor(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	conversation := view.ConversationID
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if r := submitSessionAnnotationFeedbackAs(app, session, "feedback-1", "check the total"); !r.Success || r.Status != "delivered" {
		t.Fatalf("feedback: %+v", r)
	}
	tile, _ := envEntry(view.Env, "SIGMA_AUTHOR_ID")
	if got := server.Prompted(conversation); got.Author == "" || got.Author == "attn" || tile == "" || got.AuthorID != tile {
		t.Fatalf("the user's feedback reached Sigma from %q (id %q), want the user's name with the id the tile's Sigma acts as (%q), so the tile counts it as its own", got.Author, got.AuthorID, tile)
	}
	server.Reply(conversation, "Checked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	source := w.Spawn(app, shellHarness, w.Path("peer"))
	if _, err := w.Client().AgentMsg(session, protocol.SessionID(source), "review complete"); err != nil {
		t.Fatal(err)
	}
	if got := server.Prompted(conversation); got.Author != "attn" || got.AuthorID != "attn" {
		t.Fatalf("an agent message reached Sigma from %q (id %q), want attn", got.Author, got.AuthorID)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatal(protocol.Deref(closed.Error))
	}
	server.AwaitInterrupted(conversation)
}

func TestSigmaIsUnavailableWhenItsExecutableIsMissing(t *testing.T) {
	prepared := prepareWorld(t, fakeagent.Sigma)
	t.Setenv("ATTN_SIGMA_EXECUTABLE", filepath.Join(prepared.Dir, "sigma-missing"))
	inPreparedBubble(t, prepared, func(t *testing.T, w *world) {
		w.advance(0)
		app := w.App()
		setSetting(t, app, "sigma_enabled", "true")
		updated := testworld.Await(app, protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
			h, ok := testworld.FindHarness(m.Harnesses, "sigma")
			return ok && h.Offered
		})
		if got, _ := testworld.FindHarness(updated.Harnesses, "sigma"); got.Installation != protocol.HarnessInstallationMissing {
			t.Fatalf("sigma installation = %q with no sigma executable, want missing", got.Installation)
		}
	})
}

func TestAPluginCannotRegisterTheSigmaHarness(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	driver := dialDriver(t, w, "attn-sigma")
	err := driver.call("driver.register", map[string]any{"agent": "sigma", "capabilities": map[string]bool{"server_binding": true, "resume": true}, "server": serverDriverDeclaration(w)}, &driver.registered)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("a plugin registering sigma = %v, want it refused as reserved by attn", err)
	}
}

func TestASigmaSessionComesBackAfterSigmaEnabledIsTurnedOff(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	session := w.Spawn(app, fakeagent.Sigma, w.Path("shop"))
	first := w.Launched(session)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	setSetting(t, app, "sigma_enabled", "false")

	w.restartHoldingRevives()
	app = w.App()
	revived := attachRevive(app, session, 101, 37)
	if !revived.Success || !protocol.Deref(revived.Revived) {
		t.Fatalf("attach with revive after sigma_enabled was turned off answered %+v, want the open Sigma session back", revived)
	}
	if again := w.Launched(session); again.ConversationID != first.ConversationID {
		t.Fatalf("the revived Sigma TUI opened %s, want conversation %s", again.ConversationID, first.ConversationID)
	}
}

func TestAttnRolesInstallTheWorkflowSkillWhereSigmaAndItsClaudeProvidersLook(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Sigma)
	sigmaHome := os.Getenv("SIGMA_HOME")
	providerHome := filepath.Join(t.TempDir(), "claude-work")
	configureSigmaProvider := func(configDir string) {
		if err := os.MkdirAll(sigmaHome, 0o755); err != nil {
			t.Fatal(err)
		}
		config := "[providers.work]\ndriver = \"claude\"\nconfig_dir = \"" + configDir + "\"\n"
		if err := os.WriteFile(filepath.Join(sigmaHome, "config.toml"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configureSigmaProvider(providerHome)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	awaitAgentAvailable(app, "sigma")
	preferences := *loadDelegationPreferences(app).Preferences
	preferences.WorkflowSkillEnabled = true
	preferences.Enabled = true
	claudeSkill := filepath.Join(delegationWorkflowSkillIn(os.Getenv("ATTN_TOOL_HOME"), ".claude"), "SKILL.md")

	added := addAttnDelegationRoles(app, preferences)
	if !added.Success {
		t.Fatalf("adding Attn roles: %s", protocol.Deref(added.Error))
	}
	for _, skill := range []string{
		filepath.Join(sigmaHome, "skills", "attn-workflow", "SKILL.md"),
		claudeSkill,
		filepath.Join(providerHome, "skills", "attn-workflow", "SKILL.md"),
	} {
		if _, err := os.Stat(skill); err != nil {
			t.Errorf("attn-workflow is missing where Sigma looks: %v", err)
		}
	}

	configureSigmaProvider("claude-work")
	if err := os.RemoveAll(filepath.Dir(claudeSkill)); err != nil {
		t.Fatal(err)
	}
	if again := addAttnDelegationRoles(app, *added.Preferences); again.Success || !strings.Contains(protocol.Deref(again.Error), "relative config_dir") {
		t.Errorf("adding Attn roles with a relative Sigma provider folder answered %q, want Sigma's refusal", protocol.Deref(again.Error))
	}
	if _, err := os.Stat(claudeSkill); err != nil {
		t.Errorf("Sigma's relative provider folder kept attn-workflow from Claude: %v", err)
	}
}

func TestASigmaAutomationWithAnExecutableOverrideIsDeliveredAfterItsFirstAnswer(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	w.Launched(w.Spawn(app, fakeagent.Sigma, w.Path("shop")))
	server := w.AppServer(fakeagent.Sigma)
	folder := w.Path("check")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, w.Client(), fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: sigma, executable: %q}
location: {type: directory, path: %q}
`, filepath.Join(w.Dir, "bin", "sigma"), folder))
	awaitAutomationChanged(app, 1)
	receipt := runAutomationFromApp(app, "explicit-executable")
	if !receipt.Success || receipt.Run == nil {
		t.Fatalf("starting the automation answered %s", describeRunAnswer(receipt))
	}
	sigma := w.Launched(string(protocol.Deref(receipt.Run.SessionID)))
	if prompt := server.Prompted(sigma.ConversationID).Text; !strings.Contains(prompt, "Report the flaky tests.") {
		t.Fatalf("Sigma received %q, want the automation's prompt", prompt)
	}
	server.Reply(sigma.ConversationID, "Done.")
	server.Started(sigma.ConversationID)
	sigma.Exit(0)
	if run := awaitRunState(t, w, app, receipt.Run.ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" }); run.State != "delivered" {
		t.Fatalf("Sigma launched by its executable override answered its first turn and exited with code 0, and the run is %s with %q, want delivered", run.State, protocol.Deref(run.LastError))
	}
}

func TestASigmaAutomationStartsItsConversationWithApprovalsAuto(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "sigma_enabled", "true")
	folder := w.Path("check")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: sigma, model: openai/gpt-5.5, effort: high}
location: {type: directory, path: %q}
`, folder))
	awaitAutomationChanged(app, 1)
	run := testworld.Request(app, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "now"},
		protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("now"))
	if !run.Success || protocol.Deref(run.Run.SessionID) == "" {
		t.Fatalf("automation_run = %+v, want a delivered run with a session", run)
	}
	sigma := w.Launched(string(protocol.Deref(run.Run.SessionID)))
	if got := sigma.Prompted(); !strings.Contains(got, "Report the flaky tests.") {
		t.Errorf("sigma received %q, want the automation's prompt", got)
	}
	started := w.AppServer(fakeagent.Sigma).Started(sigma.ConversationID)
	if started.Approvals != "auto" || started.Provider != "openai" || started.Model != "gpt-5.5" || started.Effort != "high" {
		t.Errorf("the automation's conversation started with approvals %q and %s/%s at %s, want approvals auto and openai/gpt-5.5 at high", started.Approvals, started.Provider, started.Model, started.Effort)
	}
	if delivered := awaitRunState(t, w, app, run.Run.ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" }); delivered.State != "delivered" {
		t.Errorf("after Sigma's server reported the run working the run is %s with %q, want delivered", delivered.State, protocol.Deref(delivered.LastError))
	}
}

func TestTheCatalogSaysWhatSigmaTakesBesideTheOtherBuiltInHarnesses(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	catalog := testworld.Await(app, protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
		h, ok := testworld.FindHarness(m.Harnesses, "sigma")
		return ok && h.Offered && h.Installation == protocol.HarnessInstallationInstalled
	})
	for _, tc := range []struct {
		id         string
		contextCap bool
	}{
		{"claude", true},
		{"codex", true},
		{"sigma", false},
	} {
		h, ok := testworld.FindHarness(catalog.Harnesses, tc.id)
		if !ok {
			t.Fatalf("catalog missing %s", tc.id)
		}
		if !h.Options.ExecutableSetting || !h.Options.ModelPin || !h.Options.EffortPin || !h.Options.LaunchInstructions || h.Options.ContextCap != tc.contextCap || !h.Options.AutoApprove {
			t.Errorf("%s options = %+v, want executable_setting, model_pin, effort_pin, launch_instructions, auto_approve true and context_cap %v", tc.id, h.Options, tc.contextCap)
		}
	}

	session := w.Spawn(app, fakeagent.Sigma, w.Path("shop"))
	w.Launched(session)
	capped := testworld.Request(app, protocol.SetSessionContextWindowCapMessage{Cmd: protocol.CmdSetSessionContextWindowCap, SessionID: protocol.SessionID(session), Cap: 200000},
		protocol.EventSessionContextWindowCapResult, func(r protocol.SessionContextWindowCapResultMessage) bool { return string(r.SessionID) == session })
	if capped.Success {
		t.Errorf("a Sigma session accepted a context window cap Sigma cannot apply: %+v", capped)
	}
}

func TestASigmaSessionStartsWithAttnsToolsWhileAgentToolsAreOn(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run("direct="+strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Sigma)
			app := w.App()
			setSetting(t, app, "sigma_enabled", "true")
			setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
			seed := plantForTools(t, w)

			off := w.Launched(w.Spawn(app, fakeagent.Sigma, w.Path("shop")))
			server := w.AppServer(fakeagent.Sigma)
			todays := server.Started(off.ConversationID)
			if slices.Contains(off.Argv, "--mcp-server") || len(todays.MCPServers) != 0 {
				t.Fatalf("with agent_tools_enabled off Sigma ran %q and started with MCP servers %+v, want none", off.Argv, todays.MCPServers)
			}

			setSetting(t, app, "agent_tools_enabled", "true")
			server.AwaitMCPRegistration("attn", true)
			session := w.Spawn(app, fakeagent.Sigma, w.Path("shop"))
			on := w.Launched(session)
			started := server.Started(on.ConversationID)
			if len(started.MCPServers) != 1 || started.MCPServers[0].Name != "attn" || started.MCPServers[0].Description == "" {
				t.Fatalf("Sigma started the conversation with MCP servers %+v, want attn with its description", started.MCPServers)
			}
			credential := strings.TrimPrefix(server.MCPHeader("attn", "Authorization"), "Bearer ")
			initialized, err := agentToolsCaller{w: w, bearer: credential, headers: map[string]string{"Sigma-Root-Conversation": on.ConversationID}}.connect(t, "")
			if err != nil {
				t.Fatal(err)
			}
			intentMap := initialized.InitializeResult().Instructions
			if intentMap == "" || !strings.HasSuffix(started.Instructions, "\n\n"+strings.TrimSpace(intentMap)) {
				t.Errorf("Sigma started the conversation with instructions %q, want them to end with attn's intent map %q", started.Instructions, intentMap)
			}
			if taught := strings.Contains(todays.Instructions, seedCLITeaching); !taught || strings.Contains(started.Instructions, seedCLITeaching) {
				t.Errorf("the garden's CLI teaching was in Sigma's launch without the tools %t and with them %t, want only without", taught, strings.Contains(started.Instructions, seedCLITeaching))
			}
			for _, entry := range append(slices.Clone(on.Argv), on.Env...) {
				if strings.Contains(entry, credential) || strings.Contains(entry, "ATTN_AGENT_CREDENTIAL") {
					t.Errorf("Sigma's client was launched with a credential in %q", entry)
				}
			}

			called := server.CallTool(on.ConversationID, "seed_note", map[string]any{"seed": seed, "text": "From the Sigma session."})
			if called.Refused != "" || called.IsError {
				t.Fatalf("the Sigma conversation calling seed_note: refused %q, %s", called.Refused, called.Text())
			}
			if author, _ := newestNoteAuthor(t, w, seed); author != session {
				t.Errorf("the Sigma conversation's note is by %s, want its session %s", author, session)
			}
		})
	}
}

func TestForkInASigmaTerminalLeavesTheSessionItLeftOpenAndHidden(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	checkout, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	original := view.ConversationID
	terminal := app.Terminal(checkout)
	promptUntilWorking(app, checkout, view, server, "add a discount field")
	server.Reply(original, "Added. <!-- attn:state=waiting_input -->")
	awaitServerFeed(app, checkout, original, server, 500)
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	mark := len(app.Log())
	typeInto(app, terminal, "/fork\r")
	view.Prompted()
	typeInto(app, terminal, "add a coupon field instead\r")
	if got := view.Prompted(); got != "add a coupon field instead" {
		t.Fatalf("the TUI sent %q after /fork, want the prompt", got)
	}
	coupon := awaitTerminalSessionChange(app, mark, terminal, checkout)
	forks := server.Forks(original)
	if len(forks) != 1 {
		t.Fatalf("conversation %s has forks %q, want the one /fork made", original, forks)
	}
	fork := forks[0]
	if got := server.Prompted(fork); got.Text != "add a coupon field instead" {
		t.Fatalf("the fork %s took %+v, want the prompt typed after /fork", fork, got)
	}
	testworld.AwaitSession(app, coupon, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(fork, "Added the coupon. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, coupon, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if usage := awaitServerFeed(app, coupon, fork, server, 300); usage.TotalTokens != 300 {
		t.Errorf("the fork's session counts %d tokens, want only the fork's 300", usage.TotalTokens)
	}
	read, err := w.Client().SessionTranscript(coupon, "")
	if err != nil || len(read.Events) == 0 || !strings.HasPrefix(protocol.Deref(read.Events[len(read.Events)-1].Text), "Added the coupon.") {
		t.Fatalf("the transcript of %s = %+v, %v, want the fork's, ending in its reply", coupon, read.Events, err)
	}

	hidden := testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })
	if hidden.State != protocol.SessionStateWaitingInput {
		t.Errorf("the session the terminal left is %s, want it still waiting on the user", hidden.State)
	}
	if entry := ledgerShowOverTheWebSocket(app, checkout).Entry; entry == nil || protocol.Deref(entry.ClosedAt) != "" {
		t.Errorf("the ledger shows %+v, want %s live", entry, checkout)
	}
	workMark := len(app.Log())
	server.Work(original)
	testworld.AwaitSessionSince(app, workMark, checkout, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if shown := queriedSession(t, w.Client(), coupon); shown.State != protocol.SessionStateIdle {
		t.Errorf("work in the original conversation moved %s to %s, want the fork's session idle", coupon, shown.State)
	}
}

func TestChainedForkInASigmaTerminalFollowsToTheFinalFork(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	first, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	original := view.ConversationID
	terminal := app.Terminal(first)
	promptUntilWorking(app, first, view, server, "first prompt")
	server.Reply(original, "First reply. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	workMark := len(app.Log())
	app.TypeLine(first, "second prompt")
	view.Prompted()
	server.Prompted(original)
	testworld.AwaitSessionSince(app, workMark, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	waitMark := len(app.Log())
	server.Reply(original, "Second reply. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, waitMark, first, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	mark := len(app.Log())
	typeInto(app, terminal, "/fork\r")
	view.Prompted()
	typeInto(app, terminal, "/fork\r")
	view.Prompted()
	typeInto(app, terminal, "third branch\r")
	if got := view.Prompted(); got != "third branch" {
		t.Fatalf("the TUI sent %q after two /forks, want the prompt", got)
	}
	intermediate := awaitTerminalSessionChange(app, mark, terminal, first)
	moved := awaitTerminalSessionBeyond(app, mark, terminal, first, intermediate)
	firstForks := server.Forks(original)
	if len(firstForks) != 1 {
		t.Fatalf("original %s has forks %q, want one intermediate fork", original, firstForks)
	}
	secondForks := server.Forks(firstForks[0])
	if len(secondForks) != 1 {
		t.Fatalf("intermediate fork %s has forks %q, want the final fork", firstForks[0], secondForks)
	}
	finalFork := secondForks[0]
	if got := server.Prompted(finalFork); got.Text != "third branch" {
		t.Fatalf("the final fork %s took %+v, want the prompt typed after two /forks", finalFork, got)
	}
	testworld.AwaitSession(app, moved, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(finalFork, "Branched. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, moved, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if usage := awaitServerFeed(app, moved, finalFork, server, 250); usage.TotalTokens != 250 {
		t.Errorf("the chained fork's session counts %d tokens, want only the fork's 250", usage.TotalTokens)
	}
}

func TestAnIdleSigmaForkMovesTheTileWithNoInputAndAReloadReopensTheFork(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	first, tui, server := appServerSession(t, w, app, fakeagent.Sigma)
	terminal := app.Terminal(first)
	original := tui.ConversationID
	settleTwoSigmaPrompts(app, first, tui, server)

	mark := len(app.Log())
	typeInto(app, terminal, "/fork\r")
	tui.Prompted()
	forks := server.Forks(original)
	if len(forks) != 1 {
		t.Fatalf("the /fork made %v, want one fork", forks)
	}
	shown := awaitTerminalSessionChange(app, mark, terminal, first)
	if run := reloadShown(t, w, app, terminal, shown); run.ConversationID != forks[0] {
		t.Errorf("reload opened conversation %s, want the fork %s the tile showed", run.ConversationID, forks[0])
	}
}

func TestAForkUnderTheTilesAuthorThatItsTUINeverOpenedLeavesTheTile(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	first, tui, server := appServerSession(t, w, app, fakeagent.Sigma)
	terminal := app.Terminal(first)
	original := tui.ConversationID
	settleTwoSigmaPrompts(app, first, tui, server)

	server.ForkAs(original, "forked-then-quit", "attn-terminal:"+terminal)
	run := reloadShown(t, w, app, terminal, first)
	if shown := sessionShownIn(t, w, app, terminal); shown != first {
		t.Errorf("the tile shows %s after a fork its TUI never opened, want %s", shown, first)
	}
	if run.ConversationID != original {
		t.Errorf("reload opened conversation %s, want %s, which the tile still showed", run.ConversationID, original)
	}
}

func TestAnIdleSigmaForkMadeWhileAttnWasCutOffMovesTheTileWhenAttnReconnects(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	first, tui, server := appServerSession(t, w, app, fakeagent.Sigma)
	terminal := app.Terminal(first)
	original := tui.ConversationID
	settleTwoSigmaPrompts(app, first, tui, server)

	server.LoseNextSubmit(original)
	if lost := submitSessionAnnotationFeedbackAs(app, first, "cut-off", "check the shipping total"); lost.Success && lost.Status == "delivered" {
		t.Fatalf("feedback whose reply the server lost = %+v, want it not reported delivered", lost)
	}
	server.Prompted(original)
	mark := len(app.Log())
	typeInto(app, terminal, "/fork\r")
	tui.Prompted()
	forks := server.Forks(original)
	if len(forks) != 1 {
		t.Fatalf("the /fork made %v, want one fork", forks)
	}
	server.Reply(original, "The shipping total is right. <!-- attn:state=waiting_input -->")
	shown := awaitTerminalSessionChange(app, mark, terminal, first)
	if run := reloadShown(t, w, app, terminal, shown); run.ConversationID != forks[0] {
		t.Errorf("the tile moved to a session on conversation %s, want the fork %s", run.ConversationID, forks[0])
	}
}

func reloadShown(t *testing.T, w *world, app *testworld.Peer, terminal, session string) *fakeagent.Run {
	t.Helper()
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
	if !reloaded.Success {
		t.Fatalf("reload %s: %s", session, protocol.Deref(reloaded.Error))
	}
	testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == terminal })
	return w.Launched(session)
}

func awaitTerminalSessionBeyond(app *testworld.Peer, mark int, terminal string, left ...string) string {
	app.T.Helper()
	var shown string
	testworld.AwaitSince(app, mark, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
		for _, desktop := range e.Desktops {
			for _, pane := range desktop.Panes {
				if string(pane.RuntimeID) == terminal && !slices.Contains(left, string(pane.SessionID)) {
					shown = string(pane.SessionID)
					return true
				}
			}
		}
		return false
	})
	return shown
}

func settleTwoSigmaPrompts(app *testworld.Peer, session string, tui *fakeagent.Run, server *fakeagent.AppServer) {
	original := tui.ConversationID
	promptUntilWorking(app, session, tui, server, "review checkout tests")
	server.Reply(original, "The checkout tests are ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	mark := app.EventCount()
	app.TypeLine(session, "review taxes")
	tui.Prompted()
	server.Prompted(original)
	testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	mark = app.EventCount()
	server.Reply(original, "The tax tests are ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}
