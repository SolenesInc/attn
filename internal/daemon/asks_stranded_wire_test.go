package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/testworld"
)

func awaitAskStranded(t *testing.T, app *testworld.Peer, askID string, stranded bool) {
	t.Helper()
	testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool {
		for _, a := range m.Asks {
			if a.ID == askID {
				return protocol.Deref(a.Stranded) == stranded
			}
		}
		return false
	})
}

func answerToChief(app *testworld.Peer, askID string) protocol.AskAnswerResultMessage {
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: requestID, AskID: askID,
		Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}},
		ToChief: protocol.Ptr(true),
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == requestID })
}

func TestAnAskIsStrandedWhileNoAgentWouldTakeItsAnswer(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone-a", app.SelectedProfile())
	phone.welcomed()
	cwd := registerDelegationCaller(t, w, cli, "caller")
	seed := plantDelegationSeed(t, cli, "caller", "Migrate the store")
	delegated, err := cli.Delegate(delegateAtSeed("caller", cwd, seed))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(delegated.SessionID))
	asked, err := cli.AskOpen(delegated.SessionID, "", cacheAsk, "", cwd)
	if err != nil {
		t.Fatalf("the delegate asks: %v", err)
	}
	if asked.Stranded != nil {
		t.Fatalf("an ask its tender holds reads %+v, want it not stranded", asked)
	}
	phoneStranded := func(stranded bool) {
		t.Helper()
		phone.showsAsks(func(asks []homegateway.HomeGatewayAsk) bool {
			return len(asks) == 1 && asks[0].ID == asked.ID && protocol.Deref(asks[0].Stranded) == stranded
		})
	}
	phoneStranded(false)

	moveDelegationSeed(t, cli, "caller", seed, "park", "")
	awaitAskStranded(t, app, asked.ID, true)
	phoneStranded(true)

	moveDelegationSeed(t, cli, "caller", seed, "tend", "")
	awaitAskStranded(t, app, asked.ID, false)
	phoneStranded(false)

	if closed := closeFromApp(app, "caller"); !closed.Accepted {
		t.Fatalf("the user's close of the tender: %+v", closed)
	}
	awaitAskStranded(t, app, asked.ID, true)

	moveDelegationSeed(t, cli, string(delegated.SessionID), seed, "tend", "")
	awaitAskStranded(t, app, asked.ID, false)
	setSetting(t, app, "garden.tender_resume", "off")
	if _, err := standbyAs(t, cli, delegated, false); err != nil {
		t.Fatalf("the tender's standby --stay: %v", err)
	}
	if closed := closeFromApp(app, string(delegated.SessionID)); !closed.Accepted {
		t.Fatalf("the user's close of the tender on standby: %+v", closed)
	}
	awaitAskStranded(t, app, asked.ID, true)
	setSetting(t, app, "garden.tender_resume", "any")
	awaitAskStranded(t, app, asked.ID, false)
	phoneStranded(false)
}

func TestAStrandedAnswerGoesToTheChiefAndStaysOnTheSeed(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	chief := startConfiguredChief(t, w, cli)
	cwd := registerDelegationCaller(t, w, cli, "caller")
	seed := plantDelegationSeed(t, cli, "caller", "Migrate the store")
	delegated, err := cli.Delegate(delegateAtSeed("caller", cwd, seed))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(string(delegated.SessionID))
	asked, err := cli.AskOpen(delegated.SessionID, "", cacheAsk, "", cwd)
	if err != nil {
		t.Fatalf("the delegate asks: %v", err)
	}
	if held := answerToChief(app, asked.ID); held.Success || !strings.Contains(protocol.Deref(held.Error), "not stranded") {
		t.Fatalf("Send to Chief while the delegate holds the ask got %+v, want it refused", held)
	}

	moveDelegationSeed(t, cli, "caller", seed, "park", "")
	awaitAskStranded(t, app, asked.ID, true)
	if sent := answerToChief(app, asked.ID); !sent.Success {
		t.Fatalf("Send to Chief failed: %s", protocol.Deref(sent.Error))
	}
	typed := chief.Prompted()
	for _, want := range []string{"would reach no agent", "nobody tends " + seed, "Migrate the store", asked.ID, "Answer: Postgres (the recommended choice)"} {
		if !strings.Contains(typed, want) {
			t.Errorf("the Chief was typed %q, missing %q", typed, want)
		}
	}
	notes, err := cli.SeedNotes("", seed, client.SeedNotesQuery{Limit: 1})
	if err != nil || len(notes.Notes) != 1 || !strings.Contains(notes.Notes[0].Body, "Answer: Postgres") {
		t.Fatalf("the seed's newest note is %+v (%v), want the answer", notes, err)
	}
	if strings.Contains(notes.Notes[0].Body, "would reach no agent") {
		t.Errorf("the seed noted %q, want the answer without the Chief's header", notes.Notes[0].Body)
	}

	chief.Reply("Noted. <!-- attn:state=idle -->")
	requestID := uuid.NewString()
	followed := testworld.Request(app, protocol.AskFollowUpMessage{
		Cmd: protocol.CmdAskFollowUp, RequestID: requestID, AskID: asked.ID, Text: "Redis after all.", ToChief: protocol.Ptr(true),
	}, protocol.EventAskFollowUpResult, func(r protocol.AskFollowUpResultMessage) bool { return r.RequestID == requestID })
	if !followed.Success || protocol.Deref(followed.Holder) != chiefSession(t, cli) {
		t.Fatalf("the follow-up to the Chief got %+v, want it typed into the Chief", followed)
	}
	if typed := chief.Prompted(); !strings.Contains(typed, "would reach no agent") || !strings.Contains(typed, "> Redis after all.") {
		t.Errorf("the Chief was typed %q, want the follow-up under the Chief's header", typed)
	}
}

func TestSendToChiefIsRefusedWhenTheProfileHasNoChief(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	homes := filepath.Dir(profileChief(t, w).HomeDir)
	if err := os.Chmod(homes, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(homes, 0o755) })
	side := createProfile(app, "Side")
	if err := w.InjectSession("asker", "asker", w.Path("asker"), "claude", side.ID); err != nil {
		t.Fatal(err)
	}
	seed := plantSeedAs(t, cli, "asker", "Migrate the store")
	moveDelegationSeed(t, cli, "asker", seed, "tend", "")
	asked, err := cli.AskOpen("asker", seed, cacheAsk, "", w.Path("asker"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	selectProfile(app, side.ID)
	if closed := closeFromApp(app, "asker"); !closed.Accepted {
		t.Fatalf("the user's close of the asker: %+v", closed)
	}
	awaitAskStranded(t, app, asked.ID, true)
	if refused := answerToChief(app, asked.ID); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "no Chief") {
		t.Fatalf("Send to Chief in a profile without a Chief got %+v, want it refused", refused)
	}
}
