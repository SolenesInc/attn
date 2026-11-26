package daemon_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryTextAttnTypesToASessionWithToolsWritesItsCommandsAsCalls(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	plain := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(plain))}
	tools := listOfferedTools(t, agent)
	arguments := commandArguments(t, tools)

	var planted struct{ ID string }
	structuredAs(t, agent.mustCall(t, "seed_plant", map[string]any{"title": "Fix the parser", "body": "Make the parser accept a trailing comma.", "checks": []string{"The parser accepts [1, 2,]."}}), &planted)
	seed := planted.ID
	delegated := agent.mustCall(t, "delegate", map[string]any{"seed": seed, "cwd": w.Path("shop"), "model": "default"})
	var started protocol.DelegationOperation
	structuredAs(t, delegated, &started)
	if started.SessionID == "" {
		t.Fatalf("delegate answered %s", resultText(delegated))
	}

	seedValues := func(extra prompts.Values) prompts.Values {
		values := prompts.Values{"seed_id": "s-example", "seed_title": "Review the queue change", "agent_tools": "true"}
		for name, value := range extra {
			values[name] = value
		}
		return values
	}
	peer := prompts.Values{"origin": "ab12 (Builder)", "message": "Ship it.", "reply_to": "ab12", "agent_tools": "true"}
	texts := map[string]string{
		"a delegate's opening":                w.Launched(string(started.SessionID)).Prompted(),
		"delegation/seed with no checks":      prompts.RenderText("delegation", "brief", seedValues(nil)),
		"delegation/seed-checks":              prompts.RenderText("delegation", "brief", seedValues(prompts.Values{"check_ids": "C3 and the plot's shared C1"})),
		"a handover":                          prompts.RenderText("delegation", "handover", seedValues(prompts.Values{"check_ids": "C3"})),
		"session/garden-update":               prompts.RenderText("session", "garden-update", prompts.Values{"seed_id": "s-example", "change": "note.added", "after": "2711491", "agent_tools": "true"}),
		"session/garden-update for the whole": prompts.RenderText("session", "garden-update", prompts.Values{"seed_id": "s-example", "change": "note.added", "agent_tools": "true"}),
		"session/inbox-notification":          prompts.RenderText("session", "inbox-notification", prompts.Values{"agent_tools": "true"}),
		"session/peer-message-steer":          prompts.RenderText("session", "peer-message-steer", peer),
		"session/peer-message-interrupt":      prompts.RenderText("session", "peer-message-interrupt", peer),
		"session/tender-resume":               prompts.RenderText("session", "tender-resume", seedValues(prompts.Values{"note": "Merge once approved."})),
		"session/standby-request":             prompts.RenderText("session", "standby-request", prompts.Values{"until": "2026-11-26T09:00:00+01:00", "agent_tools": "true"}),
		"delegation/authorization-update":     prompts.RenderText("delegation", "authorization-update", seedValues(prompts.Values{"authorization": "Push to main.", "previous": "Ask first."})),
		"delegation/authorization-cleared":    prompts.RenderText("delegation", "authorization-cleared", seedValues(prompts.Values{"previous": "Push to main."})),
		"an automation's opening":             prompts.RenderText("automation", "opening", seedValues(prompts.Values{"brief": "Review the queue change.", "input_path": "/tmp/input", "check_ids": "C1", "local_review": "false", "has_target": "false"})),
	}
	for read, text := range texts {
		for _, match := range shownCommand.FindAllStringSubmatch(text, -1) {
			if offered := offeredToolSpelledAsCommand(tools, match[1]+match[2]); offered != "" {
				t.Errorf("%s teaches %q, which a script does with %s:\n%s", read, match[1]+match[2], offered, text)
			}
		}
		named := false
		for tool := range tools {
			named = named || strings.Contains(text, "`"+tool+"`")
		}
		if checkShownCalls(t, arguments, read, text) == 0 && !named {
			t.Errorf("%s shows no call:\n%s", read, text)
		}
	}
}

func TestTheRecipientsHarnessDecidesWhetherAGardenBellNamesACallOrACommand(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "agent_tools_enabled", "true")
		watcher := w.bubbleClaude(t, app, "watcher")
		watcher.reply("Ready. <!-- attn:state=waiting_input -->")
		registerSessions(t, w, cli, "planter", "worker")
		if err := w.InjectSession("codex", "codex", w.Path("codex"), "codex"); err != nil {
			t.Fatal(err)
		}
		seed := plantSeedAs(t, cli, "planter", "bell words")
		gardenNudgeWatch(t, cli, watcher.id, seed, false)
		gardenNudgeWatch(t, cli, "codex", seed, false)
		gardenNudgeNote(t, cli, "worker", seed, "please look", true)
		w.advance(0)

		if got := watcher.promptsContaining("Read them with `agent_inbox()` in `attn_read`."); got != 1 {
			t.Errorf("the Claude watcher with tools was rung with %q; want the doorbell that names agent_inbox()", watcher.prompts())
		}
		items := readInbox(t, cli, watcher.id, 0).Items
		if len(items) != 1 || !strings.Contains(items[0].Content, "Read what changed with `seed_show({seed: \""+seed+"\", since: ") {
			t.Errorf("the Claude watcher with tools holds %q; want the bell's read as a seed_show call", inboxContents(items))
		}
		codex := readInbox(t, cli, "codex", 0).Items
		if len(codex) != 1 || !strings.Contains(codex[0].Content, "Read what changed with `attn seed show "+seed+" --since ") {
			t.Errorf("a Codex watcher, whose harness takes no tools, holds %q; want the bell's read as an attn command", inboxContents(codex))
		}
	})
}

func TestABodyWrittenAsTheUpfrontTextSaysIsStoredAsMeant(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	connected, err := agent.connect(t)
	if err != nil {
		t.Fatal(err)
	}
	rule := "Put long text in a template literal, and in it write each \\ as \\\\, each ` as \\` and each ${ as \\${."
	listed, err := connected.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stated := slices.ContainsFunc(listed.Tools, func(tool *mcp.Tool) bool { return tool.Name == "attn_do" && strings.Contains(tool.Description, rule) })
	if !stated {
		t.Fatalf("attn_do's description does not state how to write long text, %q", rule)
	}

	meant := "## Repro\n\nRun `go test -run 'TestParse\\d+' ./parser` with ${ATTN_DATA_DIR} set.\nA tab is written \\t, and the path C:\\Users\\victor stays one.\n"
	written := strings.NewReplacer("\\", "\\\\", "`", "\\`", "${", "\\${").Replace(meant)
	text, failed := agent.script(t, "attn_do", "seed_plant({title: \"Parse the trailing comma\", body: `"+written+"`})")
	if failed {
		t.Fatalf("seed_plant with the body as the upfront text says to write it: %s", text)
	}
	id := regexp.MustCompile(`id: "(s-[a-z0-9]+)"`).FindStringSubmatch(text)
	if id == nil {
		t.Fatalf("seed_plant's ledger names no seed: %s", text)
	}
	var shown struct{ Seed struct{ Body string } }
	structuredAs(t, agent.mustCall(t, "seed_show", map[string]any{"seed": id[1]}), &shown)
	if shown.Seed.Body != meant {
		t.Errorf("the stored body is\n%q\nwant the text the agent meant\n%q", shown.Seed.Body, meant)
	}
}

func TestACallOfARetiredToolNameSaysHowToReachTheCommand(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	for name, wants := range map[string][]string{
		"seed_note":  {"attn_do", "seed_note({...})", "skill({command: \"seed_note\"})"},
		"seed_prime": {"attn_do", "skill()"},
		"frobnicate": {"attn_do", "skill()"},
	} {
		result, err := agent.tool(t, name, map[string]any{"seed": "s-example", "text": "Done."}, nil)
		answer := fmt.Sprint(err)
		if err == nil {
			answer = resultText(result)
		}
		for _, want := range wants {
			if !strings.Contains(answer, want) {
				t.Errorf("calling the tool %s answered %q; want it to name %s", name, answer, want)
			}
		}
	}
}

func TestSkillWithNoArgumentsOpensWithTheWayInOfItsCaller(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	cliWayIn := "`attn <command> --help` shows a command's syntax."
	toolsWayIn := "- Run attn's commands as functions in a script: `attn_read` runs those that only read, and `attn_do` runs any."

	called := resultText(agent.mustCall(t, "skill", map[string]any{}))
	if !strings.Contains(called, toolsWayIn) || strings.Contains(called, cliWayIn) {
		t.Errorf("skill() through the tools answered\n%s\nwant it to open with %q and not with %q", called, toolsWayIn, cliWayIn)
	}
	var shell string
	if err := w.Client().RunCommand("skill", protocol.SessionID(session), map[string]any{}, &shell); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shell, cliWayIn) || strings.Contains(shell, toolsWayIn) {
		t.Errorf("attn skill in a shell answered\n%s\nwant it to open with %q", shell, cliWayIn)
	}
}

var shownToolCall = regexp.MustCompile(`attn_do|attn_read|\b[a-z]+_[a-z_]+\(`)

func TestOnlyASessionLaunchedWithTheToolsIsToldToCallThem(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		dispatcher := w.Spawn(app, fakeagent.Claude, w.Path("dispatcher"))
		launch := func(name string) (*bubbleClaude, string) {
			seed := plantDelegationSeed(t, cli, dispatcher, name)
			request := delegateAtSeed(dispatcher, w.Path("dispatcher"), seed)
			request.Agent = protocol.Ptr("claude")
			delegated, err := cli.Delegate(request)
			if err != nil {
				t.Fatal(err)
			}
			tender := w.bootBubbleClaude(t, string(delegated.SessionID))
			tender.reply("Started. <!-- attn:state=waiting_input -->")
			return tender, seed
		}
		before, beforeSeed := launch("Launched before the tools")
		setSetting(t, app, "agent_tools_enabled", "true")
		with, withSeed := launch("Launched with the tools")

		textsTo := func(tender *bubbleClaude, seed, when string) map[string]string {
			texts := map[string]string{}
			typed := func(what string, act func()) {
				already := len(tender.prompts())
				act()
				w.advance(time.Second)
				synctest.Wait()
				texts[what] = strings.Join(tender.prompts()[already:], "\n")
			}
			typed("a bell's doorbell", func() {
				bell := plantSeedAs(t, cli, dispatcher, "bell "+when)
				gardenNudgeWatch(t, cli, tender.id, bell, false)
				gardenNudgeNote(t, cli, dispatcher, bell, "please look", true)
			})
			texts["the bell in the inbox"] = inboxContents(readInbox(t, cli, tender.id, 0).Items)
			tender.reply("Read it. <!-- attn:state=waiting_input -->")
			typed("a standby request", func() {
				app.Send(protocol.StandbySessionMessage{Cmd: protocol.CmdStandbySession, SessionID: protocol.SessionID(tender.id), Until: time.Now().Add(2 * time.Hour)})
			})
			tender.reply("Not yet. <!-- attn:state=waiting_input -->")
			typed("an authorization change", func() {
				if _, err := cli.WithGardenProfile("", protocol.SessionID(dispatcher)).SeedAuthorization(seed, "Ask me before pushing "+when+".", false); err != nil {
					t.Fatal(err)
				}
			})
			tender.reply("Noted. <!-- attn:state=waiting_input -->")
			return texts
		}
		judge := func(when string) {
			for what, text := range textsTo(before, beforeSeed, when) {
				if text == "" || shownToolCall.MatchString(text) || !strings.Contains(text, "attn ") {
					t.Errorf("%s: %s reached the session launched before the tools as\n%s\nwant attn commands and no call", when, what, text)
				}
			}
			for what, text := range textsTo(with, withSeed, when) {
				if !shownToolCall.MatchString(text) {
					t.Errorf("%s: %s reached the session launched with the tools as\n%s\nwant a call", when, what, text)
				}
			}
		}
		judge("once the setting is on")
		w.restart()
		cli = w.Client()
		app = w.App()
		before.cli, with.cli = cli, cli
		w.advance(0)
		judge("after a restart")

		reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(before.id), Cols: 100, Rows: 30},
			protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == before.id })
		if !reloaded.Success {
			t.Fatalf("reloading %s: %s", before.id, protocol.Deref(reloaded.Error))
		}
		relaunched := w.bootBubbleClaude(t, before.id)
		relaunched.reply("Back. <!-- attn:state=waiting_input -->")
		for what, text := range textsTo(relaunched, beforeSeed, "after a reload") {
			if !shownToolCall.MatchString(text) {
				t.Errorf("after a reload with the setting on: %s reached the relaunched session as\n%s\nwant a call", what, text)
			}
		}
	})
}

func TestAPeerTurnsReplyLineNamesACallOnlyForASessionWithTools(t *testing.T) {
	for _, launch := range []struct {
		name          string
		atLaunch, now bool
	}{{"launched with tools", true, true}, {"launched without", false, false}, {"launched before the setting turned on", false, true}} {
		tools := launch.atLaunch
		for _, turn := range []string{"steer", "interrupt"} {
			t.Run(turn+"/"+launch.name, func(t *testing.T) {
				w := newWorld(t, fakeagent.Claude)
				app, cli := w.App(), w.Client()
				setSetting(t, app, "agent_tools_enabled", strconv.FormatBool(launch.atLaunch))
				registerSessions(t, w, cli, "lead")
				session, run := moddedClaude(t, w, app)
				setSetting(t, app, "agent_tools_enabled", strconv.FormatBool(launch.now))
				workingOn(t, app, run, session, "refactor the cache")

				sendPeerMessage(t, cli, "lead", session, "New code logs through slog.", turn)
				modEvent(t, run, fakeagent.ModEventHeld, "slog")
				var typed []string
				if turn == "steer" {
					typed = run.ModToolBoundary()
				} else {
					run.ModAbortLands()
					typed = []string{run.Prompted()}
				}
				want := "reply: attn agent msg "
				if tools {
					want = "reply: agent_msg({target: "
				}
				if len(typed) != 1 || !strings.Contains(typed[0], "New code logs through slog.") || !strings.Contains(typed[0], want) {
					t.Fatalf("the %s reached the Claude session as %q, want its reply line to read %q", turn, typed, want)
				}
			})
		}
	}
}
