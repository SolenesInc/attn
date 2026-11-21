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
	if got, offered := app.Initial.Settings["sigma_available"]; offered {
		t.Fatalf("with sigma_enabled off the settings offer sigma_available=%q, want no sigma", got)
	}
	refused, _, _ := w.RequestSpawn(app, fakeagent.Sigma, w.Path("shop"))
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "not available") {
		t.Fatalf("a Sigma launch with sigma_enabled off = %+v, want it refused as not available", refused)
	}

	setSetting(t, app, "sigma_enabled", "true")
	settings := pluginDriverSettings(app, "sigma")
	if settings["sigma_cap_model_discovery"] != "true" || settings["sigma_cap_launch_instructions"] != "true" {
		t.Errorf("Sigma's capabilities = model discovery %q, launch instructions %q, want both offered", settings["sigma_cap_model_discovery"], settings["sigma_cap_launch_instructions"])
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
	if got := server.Prompted(conversation); got.Author == "" || got.AuthorID != got.Author || got.Author == "attn" {
		t.Fatalf("the user's feedback reached Sigma from %q (id %q), want the user's name as both", got.Author, got.AuthorID)
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
		updated := testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
			return m.Settings["sigma_available"] != ""
		})
		if got := updated.Settings["sigma_available"]; got != "false" {
			t.Fatalf("sigma_available = %q with no sigma executable, want false", got)
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

func TestTheSettingsSayWhatSigmaTakesBesideTheOtherBuiltInHarnesses(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "sigma_enabled", "true")
	settings := pluginDriverSettings(app, "sigma")
	want := map[string]map[string]string{
		"claude": {"executable_setting": "true", "model_pin": "true", "effort_pin": "true", "launch_instructions": "true", "context_window_cap": "true", "automation": "true"},
		"codex":  {"executable_setting": "true", "model_pin": "true", "effort_pin": "true", "launch_instructions": "true", "context_window_cap": "true", "automation": "true"},
		"sigma":  {"executable_setting": "true", "model_pin": "true", "effort_pin": "true", "launch_instructions": "true", "context_window_cap": "false", "automation": "true"},
	}
	for agent, capabilities := range want {
		for capability, value := range capabilities {
			if got := settings[agent+"_cap_"+capability]; got != value {
				t.Errorf("%s_cap_%s = %q, want %q", agent, capability, got, value)
			}
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
			initialized, err := agentToolsCaller{w: w, bearer: agentCredential(t, w, "s.sigma"), headers: map[string]string{"Sigma-Root-Conversation": on.ConversationID}}.connect(t, "")
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
				if strings.Contains(entry, "s.sigma.") || strings.Contains(entry, "ATTN_AGENT_CREDENTIAL") {
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
