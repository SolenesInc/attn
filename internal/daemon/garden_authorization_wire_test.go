package daemon_test

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const authorizationTyped = "authorization for seed"

func TestADispatchersAuthorizationChangeIsTypedIntoTheTenderOnceItCanTakeInput(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "auto_approve_enabled", "false")
		dispatcher := w.Spawn(app, fakeagent.Claude, w.Path("dispatcher"))
		seed := plantDelegationSeed(t, cli, dispatcher, "Search endpoint")
		request := delegateAtSeed(dispatcher, w.Path("dispatcher"), seed)
		request.Agent, request.Authorization = protocol.Ptr("claude"), protocol.Ptr("You may land on main.")
		delegated, err := cli.Delegate(request)
		if err != nil {
			t.Fatal(err)
		}
		tender := w.bootBubbleClaude(t, string(delegated.SessionID))
		tender.reply("Started on the endpoint. <!-- attn:state=waiting_input -->")
		terminal := protocol.TerminalID(w.Terminal(tender.id))
		if err := cli.UpdateState(terminal, protocol.StatePendingApproval); err != nil {
			t.Fatal(err)
		}
		w.advance(0)

		asDispatcher := cli.WithGardenProfile("", protocol.SessionID(dispatcher))
		first, err := asDispatcher.SeedAuthorization(seed, "Ask me before pushing.", false)
		if err != nil {
			t.Fatal(err)
		}
		if u := first.Update; !first.Changed || u == nil || u.TypedAt != nil || !protocol.Deref(u.NeedsApproval) || protocol.Deref(u.Recipient.SessionID) != delegated.SessionID {
			t.Fatalf("setting the words while the tender needs approval returned %+v; want a change waiting for %s, which needs approval", first, delegated.SessionID)
		}
		if _, err := asDispatcher.SeedAuthorization(seed, "Ask me before landing.", false); err != nil {
			t.Fatal(err)
		}
		w.advance(15 * time.Minute)
		if got := tender.promptsContaining(authorizationTyped); got != 0 {
			t.Fatalf("the update was typed over a pending approval: %q", tender.prompts())
		}

		w.restart()
		cli = w.Client()
		tender.cli = cli
		asDispatcher = cli.WithGardenProfile("", protocol.SessionID(dispatcher))
		w.advance(0)
		if err := cli.UpdateStateFromHookEvidence(terminal, protocol.StateWorking, "", "", ""); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		want := `The user changed the authorization for seed ` + seed + ` "Search endpoint", set by dispatcher:
"Ask me before landing."
It replaces: "You may land on main."
If this applies to work you delegated, update those seeds with attn seed authorization.`
		if typed := typedAuthorizations(tender); len(typed) != 1 || typed[0] != want {
			t.Fatalf("after the restart and the approval the tender was typed %q; want only the newer words:\n%s", typed, want)
		}
		shown, err := cli.SeedShow("", seed)
		if err != nil {
			t.Fatal(err)
		}
		if a := shown.Seed.Authorization; a == nil || a.Text != "Ask me before landing." || a.Update == nil || a.Update.TypedAt == nil || protocol.Deref(a.Update.Recipient.SessionID) != delegated.SessionID {
			t.Fatalf("the seed shows authorization %+v; want the newer words typed into %s", a, delegated.SessionID)
		}

		again, err := asDispatcher.SeedAuthorization(seed, "Ask me before landing.", false)
		if err != nil {
			t.Fatal(err)
		}
		if again.Changed || again.Update == nil || again.Update.TypedAt == nil {
			t.Fatalf("the same words again returned %+v; want no change with the last typed update", again)
		}

		if err := cli.UpdateState(terminal, protocol.StatePendingApproval); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if _, err := asDispatcher.SeedAuthorization(seed, "You may release.", false); err != nil {
			t.Fatal(err)
		}
		self, err := cli.WithGardenProfile("", delegated.SessionID).SeedAuthorization(seed, "The user said I may push this branch.", false)
		if err != nil {
			t.Fatal(err)
		}
		if !self.Changed || !protocol.Deref(self.CallerTends) || self.Update != nil {
			t.Fatalf("the tender changing its own seed returned %+v; want a change that types nothing", self)
		}
		if err := cli.UpdateStateFromHookEvidence(terminal, protocol.StateWorking, "", "", ""); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if typed := typedAuthorizations(tender); len(typed) != 1 {
			t.Fatalf("the dispatcher's change was typed after the tender replaced it with its own words: %q", typed)
		}

		registerSessions(t, w, cli, "outsider")
		_, err = cli.WithGardenProfile("", "outsider").SeedAuthorization(seed, "You may release.", false)
		if err == nil || !strings.Contains(err.Error(), "the sessions above") || !strings.Contains(err.Error(), "the chief or the user can set its authorization") {
			t.Fatalf("a session outside the delegation chain got %v; want the refusal with the rule", err)
		}

		cleared, err := asDispatcher.SeedAuthorization(seed, "", true)
		if err != nil {
			t.Fatal(err)
		}
		if a := cleared.Seed.Authorization; !cleared.Changed || a == nil || a.Text != "" || a.Update == nil || a.Update.TypedAt == nil {
			t.Fatalf("clearing returned %+v; want the words gone and the cleared form typed", cleared)
		}
		wantCleared := `The user removed the authorization for seed ` + seed + ` "Search endpoint", set by dispatcher. These words no longer apply: "The user said I may push this branch."
If this applies to work you delegated, update those seeds with attn seed authorization.`
		w.advance(time.Hour)
		if typed := typedAuthorizations(tender); len(typed) != 2 || typed[1] != wantCleared {
			t.Fatalf("the tender was typed %q; want the earlier update, then the cleared form:\n%s", typed, wantCleared)
		}
	})
}

func TestASharedCodexDelegateReceivesItsDispatchersAuthorizationChange(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "codex_shared_enabled", "true")
	cwd := registerDelegationCaller(t, w, cli, "caller")
	w.SetCodexFolderTrust(cwd, "trusted")
	seed := plantDelegationSeed(t, cli, "caller", "Search endpoint")
	delegated, err := cli.Delegate(delegateAtSeed("caller", cwd, seed))
	if err != nil {
		t.Fatal(err)
	}
	codex := w.Launched(string(delegated.SessionID))
	codex.Prompted()
	codex.Reply("Started on the endpoint. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, string(delegated.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	if _, err := cli.WithGardenProfile("", "caller").SeedAuthorization(seed, "Ask me before pushing.", false); err != nil {
		t.Fatal(err)
	}
	want := `The user changed the authorization for seed ` + seed + ` "Search endpoint", set by caller:
"Ask me before pushing."
If this applies to work you delegated, update those seeds with attn seed authorization.`
	if got := w.CodexServer().Prompted(codex.ConversationID); got != want {
		t.Fatalf("the shared Codex delegate was typed %q; want\n%s", got, want)
	}
	shown, err := cli.SeedShow("", seed)
	if err != nil {
		t.Fatal(err)
	}
	if a := shown.Seed.Authorization; a == nil || a.Update == nil || a.Update.TypedAt == nil {
		t.Fatalf("the seed shows authorization %+v; want the update typed", a)
	}
}

func TestTheUserChangesATendedSeedsAuthorizationFromTheApp(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		dispatcher := w.Spawn(app, fakeagent.Claude, w.Path("dispatcher"))
		seed := plantDelegationSeed(t, cli, dispatcher, "Search endpoint")
		request := delegateAtSeed(dispatcher, w.Path("dispatcher"), seed)
		request.Agent = protocol.Ptr("claude")
		delegated, err := cli.Delegate(request)
		if err != nil {
			t.Fatal(err)
		}
		tender := w.bootBubbleClaude(t, string(delegated.SessionID))
		tender.reply("Started on the endpoint. <!-- attn:state=waiting_input -->")
		w.advance(0)

		fromApp := func(requestID string, msg protocol.SeedAuthorizationMessage) protocol.SeedAuthorizationResultMessage {
			msg.Cmd, msg.RequestID, msg.SeedID = protocol.CmdSeedAuthorization, protocol.Ptr(requestID), seed
			return testworld.Request(app, msg, protocol.EventSeedAuthorizationResult, func(m protocol.SeedAuthorizationResultMessage) bool { return m.RequestID == requestID })
		}

		set := fromApp("set", protocol.SeedAuthorizationMessage{Text: protocol.Ptr("You may land on main.")})
		if !set.Success || set.Result == nil || !set.Result.Changed {
			t.Fatalf("the user setting the words from the app got %+v; want a change", set)
		}
		if a := set.Result.Seed.Authorization; a == nil || a.Text != "You may land on main." || a.SetBy.Ref != "user" {
			t.Fatalf("the seed holds authorization %+v; want the words set by the user", a)
		}
		w.advance(0)
		want := `The user changed the authorization for seed ` + seed + ` "Search endpoint":
"You may land on main."
If this applies to work you delegated, update those seeds with attn seed authorization.`
		if typed := typedAuthorizations(tender); len(typed) != 1 || typed[0] != want {
			t.Fatalf("the tender was typed %q; want\n%s", typed, want)
		}
		tender.reply("Read the new authorization. <!-- attn:state=waiting_input -->")
		w.advance(0)

		cleared := fromApp("clear", protocol.SeedAuthorizationMessage{Clear: protocol.Ptr(true)})
		if !cleared.Success || cleared.Result == nil || !cleared.Result.Changed {
			t.Fatalf("the user clearing the words from the app got %+v; want a change", cleared)
		}
		if a := cleared.Result.Seed.Authorization; a == nil || a.Text != "" || a.SetBy.Ref != "user" || a.Update == nil || protocol.Deref(a.Update.Recipient.SessionID) != delegated.SessionID {
			t.Fatalf("after the clear the seed holds authorization %+v; want no words, cleared by the user, with the removal sent to %s", a, delegated.SessionID)
		}
		w.advance(0)
		wantCleared := `The user removed the authorization for seed ` + seed + ` "Search endpoint". These words no longer apply: "You may land on main."
If this applies to work you delegated, update those seeds with attn seed authorization.`
		if typed := typedAuthorizations(tender); len(typed) != 2 || typed[1] != wantCleared {
			t.Fatalf("the tender was typed %q; want the set, then the cleared form:\n%s", typed, wantCleared)
		}
	})
}

func TestTheAppSeesAWaitingAuthorizationUpdateTypedOnceTheTenderTakesIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "auto_approve_enabled", "false")
		dispatcher := w.Spawn(app, fakeagent.Claude, w.Path("dispatcher"))
		seed := plantDelegationSeed(t, cli, dispatcher, "Search endpoint")
		request := delegateAtSeed(dispatcher, w.Path("dispatcher"), seed)
		request.Agent = protocol.Ptr("claude")
		delegated, err := cli.Delegate(request)
		if err != nil {
			t.Fatal(err)
		}
		tender := w.bootBubbleClaude(t, string(delegated.SessionID))
		tender.reply("Started on the endpoint. <!-- attn:state=waiting_input -->")
		terminal := protocol.TerminalID(w.Terminal(tender.id))
		if err := cli.UpdateState(terminal, protocol.StatePendingApproval); err != nil {
			t.Fatal(err)
		}
		w.advance(0)

		set := testworld.Request(app, protocol.SeedAuthorizationMessage{
			Cmd: protocol.CmdSeedAuthorization, RequestID: protocol.Ptr("set"), SeedID: seed, Text: protocol.Ptr("You may land on main."),
		}, protocol.EventSeedAuthorizationResult, func(m protocol.SeedAuthorizationResultMessage) bool { return m.RequestID == "set" })
		if u := set.Result.Update; u == nil || u.TypedAt != nil || !protocol.Deref(u.NeedsApproval) {
			t.Fatalf("setting the words while the tender needs approval returned %+v; want an update waiting on the approval", set.Result)
		}
		w.advance(0)
		mark := len(app.Log())

		if err := cli.UpdateStateFromHookEvidence(terminal, protocol.StateWorking, "", "", ""); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		pushed := testworld.AwaitSince(app, mark, protocol.EventGardenSeedsChanged, func(m protocol.GardenSeedsChangedMessage) bool {
			for _, changed := range m.Seeds {
				if changed.ID == seed && changed.Authorization != nil && changed.Authorization.Update != nil && changed.Authorization.Update.TypedAt != nil {
					return true
				}
			}
			return false
		})
		if len(typedAuthorizations(tender)) != 1 {
			t.Fatalf("the app was pushed %+v before the tender was typed the update", pushed)
		}
	})
}

func typedAuthorizations(agent *bubbleClaude) []string {
	synctest.Wait()
	var typed []string
	for _, prompt := range agent.prompts() {
		if strings.Contains(prompt, authorizationTyped) {
			typed = append(typed, prompt)
		}
	}
	return typed
}
