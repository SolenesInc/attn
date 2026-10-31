package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func applyCrew(t *testing.T, cli *client.Client, member string) *protocol.CrewApplyResult {
	t.Helper()
	applied, err := cli.CrewApply(member)
	if err != nil {
		t.Fatalf("apply %s: %v", member, err)
	}
	return applied
}

func switchCrewHarness(t *testing.T, cli *client.Client, member, agent string) *protocol.CrewApplyResult {
	t.Helper()
	setCrew(t, cli, member, protocol.CrewSetMessage{Agent: protocol.Ptr(agent)})
	applied := applyCrew(t, cli, member)
	if applied.Outcome != protocol.CrewApplyOutcomeLetterRequested {
		t.Fatalf("applying %s's switch to %s = %+v, want its letter requested", member, agent, applied)
	}
	return applied
}

func TestApplyingAModelChangeReloadsTheMemberInPlaceWithItsConversation(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "trellis", "")
	first := w.Launched(string(day.SessionID))
	letters := crewLetters(t, w, "trellis")

	setCrew(t, cli, "trellis", protocol.CrewSetMessage{Model: protocol.Ptr("claude-sonnet-fake"), Effort: protocol.Ptr("high")})
	applied := applyCrew(t, cli, "trellis")
	if applied.Outcome != protocol.CrewApplyOutcomeReloaded || protocol.Deref(applied.SessionID) != day.SessionID {
		t.Fatalf("apply = %+v, want session %s reloaded in place", applied, day.SessionID)
	}
	again := w.Launched(string(day.SessionID))
	if !again.Resumed || again.ConversationID != first.ConversationID {
		t.Fatalf("the reload ran resumed=%v conversation=%s, want conversation %s kept", again.Resumed, again.ConversationID, first.ConversationID)
	}
	if model, effort := crewLaunchFlag(again.Argv, "--model"), crewLaunchFlag(again.Argv, "--effort"); model != "claude-sonnet-fake" || effort != "high" {
		t.Fatalf("the reload launched --model %q --effort %q, want the saved claude-sonnet-fake and high", model, effort)
	}
	charter := filepath.Join(crewHome(w, "trellis"), crew.CharterFileName)
	if items := readInbox(t, cli, string(day.SessionID), 0).Items; len(items) != 1 || items[0].Content != prompts.RenderText("crew", "settings-applied", prompts.Values{"charter_path": charter}) {
		t.Fatalf("after the reload trellis's inbox = %+v, want the request to reread its charter", items)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != day.SessionID {
		t.Fatalf("after the reload trellis is bound to %q, want %s", got, day.SessionID)
	}
	if after := crewLetters(t, w, "trellis"); len(after) != len(letters) {
		t.Fatalf("an in-place reload filed letters: %q, want %q", after, letters)
	}

	t.Run("any later reload keeps the saved settings", func(t *testing.T) {
		setCrew(t, cli, "trellis", protocol.CrewSetMessage{Effort: protocol.Ptr("medium")})
		reloaded, err := cli.AgentReload([]string{"trellis"}, day.SessionID, false)
		if err != nil || len(reloaded.Outcomes) != 1 || !reloaded.Outcomes[0].Success {
			t.Fatalf("agent reload trellis = %+v, %v", reloaded, err)
		}
		if relaunched := w.Launched(string(day.SessionID)); crewLaunchFlag(relaunched.Argv, "--effort") != "medium" || relaunched.ConversationID != first.ConversationID {
			t.Fatalf("agent reload ran effort %q in conversation %s, want medium in %s", crewLaunchFlag(relaunched.Argv, "--effort"), relaunched.ConversationID, first.ConversationID)
		}
	})
}

func TestApplyingAHarnessChangeTurnsTheLetterIntoASessionOnTheNewHarness(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	before := crewSessionCount(t, cli)

	applied := switchCrewHarness(t, cli, "trellis", "codex")
	if protocol.Deref(applied.SessionID) != day.SessionID {
		t.Fatalf("apply = %+v, want the letter asked of %s", applied, day.SessionID)
	}
	items := readInbox(t, cli, string(day.SessionID), 0).Items
	if want := prompts.RenderText("crew", "switch-requested", prompts.Values{"agent": "codex"}); len(items) != 1 || items[0].Content != want {
		t.Fatalf("trellis's inbox = %+v, want the request to file its letter", items)
	}
	if got := crewSessionCount(t, cli); got != before {
		t.Fatalf("asking for the letter changed the sessions from %d to %d", before, got)
	}
	if !protocol.Deref(crewRosterMember(t, cli, "trellis").SwitchPending) {
		t.Fatal("the roster does not show trellis's pending switch")
	}

	letter := "Dear next trellis,\n\n#901 is waiting on review.\n"
	handed := crewHandoff(t, cli, string(day.SessionID), letter, false)
	successor := protocol.Deref(handed.SessionID)
	if protocol.Deref(handed.Outcome) != protocol.CrewDayCloseSuccessor || handed.SuccessorError != nil || successor == "" || successor == day.SessionID {
		t.Fatalf("handoff = %+v, want a successor", handed)
	}
	if body, err := os.ReadFile(handed.Path); err != nil || string(body) != letter {
		t.Fatalf("the filed letter holds %q (%v), want the member's prose untouched", body, err)
	}
	if letters := crewLetters(t, w, "trellis"); letters[0] != filepath.Base(handed.Path) {
		t.Fatalf("trellis's letters = %q, want the new one freshest", letters)
	}
	next := w.Launched(string(successor))
	if next.Harness != fakeagent.Codex || next.Resumed || crewLaunchFlag(next.Argv, "--model") != "" {
		t.Fatalf("the successor launched %s resumed=%v argv %q, want a fresh codex on its default model", next.Harness, next.Resumed, next.Argv)
	}
	if !strings.Contains(strings.Join(next.Argv, "\n"), "crew member of this attn home") {
		t.Errorf("the codex successor launched without its crew priming: argv %q", next.Argv)
	}
	if got, want := next.Prompted(), prompts.RenderText("crew", "successor", prompts.Values{}); got != want {
		t.Errorf("the successor was asked %q, want the successor prompt", got)
	}
	if got := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession); got != successor {
		t.Fatalf("roster binding = %q, want the successor %s", got, successor)
	}
	if primed := crewPriming(t, cli, string(successor)); !strings.Contains(primed, "#901 is waiting on review.") {
		t.Errorf("the successor's priming does not carry the letter just filed:\n%s", primed)
	}

	t.Run("the switch is spent: the successor's own letter ends the day", func(t *testing.T) {
		slept := crewHandoff(t, cli, string(successor), "Signing off on codex.", false)
		if protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep || slept.SessionID != nil {
			t.Fatalf("handoff = %+v, want sleep with no successor", slept)
		}
	})

	t.Run("an asleep member is told its next wake applies the settings", func(t *testing.T) {
		count := crewSessionCount(t, cli)
		asleep := applyCrew(t, cli, "trellis")
		if asleep.Outcome != protocol.CrewApplyOutcomeAsleep || asleep.SessionID != nil || !strings.Contains(asleep.Detail, "next wake") {
			t.Fatalf("apply = %+v, want the asleep no-op", asleep)
		}
		if got := crewSessionCount(t, cli); got != count {
			t.Fatalf("applying to an asleep member changed the sessions from %d to %d", count, got)
		}
	})

	t.Run("saving the old harness back without applying cancels the switch", func(t *testing.T) {
		day := wakeCrew(t, cli, "alder", "")
		w.Launched(string(day.SessionID))
		switchCrewHarness(t, cli, "alder", "codex")
		setCrew(t, cli, "alder", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
		if protocol.Deref(crewRosterMember(t, cli, "alder").SwitchPending) {
			t.Fatal("the roster still shows a switch to the harness alder already runs on")
		}
		if slept := crewHandoff(t, cli, string(day.SessionID), "Done for today.", false); protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep {
			t.Fatalf("handoff after saving the old harness back = %+v, want sleep", slept)
		}
	})

	t.Run("switching back before the letter reloads in place and cancels the switch", func(t *testing.T) {
		day := wakeCrew(t, cli, "keel", "")
		first := w.Launched(string(day.SessionID))
		switchCrewHarness(t, cli, "keel", "codex")
		setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
		if back := applyCrew(t, cli, "keel"); back.Outcome != protocol.CrewApplyOutcomeReloaded {
			t.Fatalf("apply after switching back = %+v, want an in-place reload", back)
		}
		if again := w.Launched(string(day.SessionID)); again.ConversationID != first.ConversationID {
			t.Fatalf("the reload ran conversation %s, want %s", again.ConversationID, first.ConversationID)
		}
		if slept := crewHandoff(t, cli, string(day.SessionID), "Staying on claude.", false); protocol.Deref(slept.Outcome) != protocol.CrewDayCloseSleep {
			t.Fatalf("handoff after the cancelled switch = %+v, want sleep", slept)
		}
	})
}

func TestOnlyTheMemberItsChainAndTheChiefMayApplyItsSettings(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	if err := w.InjectSession("errand", "errand", w.Path("errand"), protocol.SessionAgentClaude); err != nil {
		t.Fatal(err)
	}
	day := wakeCrew(t, cli, "trellis", "")
	w.Launched(string(day.SessionID))
	_, err := cli.WithGardenProfile("", "errand").CrewApply("trellis")
	crewErrorContains(t, err, "delegation-chain rule")
	applied, err := cli.WithGardenProfile("", day.SessionID).CrewApply("trellis")
	if err != nil || applied.Outcome != protocol.CrewApplyOutcomeReloaded {
		t.Fatalf("trellis applying its own settings = %+v, %v", applied, err)
	}
}
