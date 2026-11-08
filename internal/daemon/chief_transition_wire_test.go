package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
	"github.com/victorarias/attn/internal/who"
)

func TestAnUpgradedChiefWithNoRecordedModelRunsOnTheHarnessDefault(t *testing.T) {
	w, _ := upgradedFromTodaysChief(t, t.TempDir(), store.LaunchIntent{})
	day := wakeCrew(t, w.Client(), "chief", "")
	run := w.Launched(string(day.SessionID))
	if model, pinned := flagValue(run.Argv, "--model"); pinned {
		t.Fatalf("the upgraded Chief launched with --model %s, want the harness default", model)
	}
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	setSetting(t, w.App(), "default_model_claude", "claude-sonnet-fake")
	crewHandoff(t, w.Client(), string(day.SessionID), "Next day.", false)
	next := wakeCrew(t, w.Client(), "chief", "")
	if model, _ := flagValue(w.Launched(string(next.SessionID)).Argv, "--model"); model != "claude-sonnet-fake" {
		t.Fatalf("with default_model_claude set the Chief launched with --model %q, want claude-sonnet-fake", model)
	}
}

func upgradedWithChiefAsk(t *testing.T, answeredBeforeUpgrade bool) (*testworld.World, string) {
	t.Helper()
	prepared := prepareWorld(t, fakeagent.Claude)
	before, err := store.NewWithDB(filepath.Join(prepared.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := before.ListProfiles(false)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles before the upgrade = %+v, %v", profiles, err)
	}
	old := protocol.SessionID("todays-chief")
	if err := before.AddChecked(&protocol.Session{ID: old, Label: "Chief", Agent: protocol.SessionAgentClaude, Directory: t.TempDir(), ProfileID: profiles[0].ID, State: protocol.SessionStateWorking}); err != nil {
		t.Fatal(err)
	}
	if err := before.SetLaunchIntent(old, store.LaunchIntent{Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	key, err := who.ParseMemberKey("chief-upgraded")
	if err != nil {
		t.Fatal(err)
	}
	legacy := who.Member(key).Address()
	askID := uuid.NewString()
	if err := before.AddAsk(store.Ask{ID: askID, SessionID: old, To: legacy, Subject: "Cache", Markdown: cacheAsk, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if answeredBeforeUpgrade {
		answer := inbox.Item{ID: uuid.NewString(), To: legacy, Kind: inbox.AskAnswer, Source: askID, Text: wokenAnswer}
		if err := before.AnswerAsk(askID, protocol.AskReply{Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}}}, answer, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := before.Close(); err != nil {
		t.Fatal(err)
	}
	execOnWorldDB(t, prepared.Dir, `UPDATE profiles SET chief_session_id = ?, chief_member = ? WHERE id = ?`, string(old), key.String(), profiles[0].ID)
	execOnWorldDB(t, prepared.Dir, `INSERT INTO crew_members(member_key, profile_id, name) VALUES (?, ?, 'Chief')`, key.String(), profiles[0].ID)
	return prepared, askID
}

func awaitWokenChiefAnswer(t *testing.T, w *world) {
	t.Helper()
	synctest.Wait()
	chief := profileChief(t, w)
	if chief.BindingSession == nil {
		t.Fatal("the answer to today's Chief's ask did not wake the Chief member")
	}
	run := w.bootBubbleClaude(t, string(*chief.BindingSession))
	synctest.Wait()
	if got := run.promptsContaining("Postgres"); got != 1 {
		t.Fatalf("the woken Chief got the answer %d times, want once: %s", got, strings.Join(run.prompts(), "\n"))
	}
}

func TestAnAnswerToTodaysChiefsAskWakesTheUpgradedChief(t *testing.T) {
	prepared, askID := upgradedWithChiefAsk(t, false)
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true, terms: testworld.NewTerminals(t)}
		w.start()
		answerAsk(t, w.App(), askID)
		awaitWokenChiefAnswer(t, w)
	})
}

func TestAnAnswerQueuedForTodaysChiefWakesTheChiefAfterTheUpgrade(t *testing.T) {
	prepared, _ := upgradedWithChiefAsk(t, true)
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true, terms: testworld.NewTerminals(t)}
		w.start()
		awaitWokenChiefAnswer(t, w)
	})
}
