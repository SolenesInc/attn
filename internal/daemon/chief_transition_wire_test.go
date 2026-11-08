package daemon_test

import (
	"os"
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

func TestALaunchAsChiefIsRefusedAndNamesTheChiefMember(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("chief-work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	id := protocol.SessionID(uuid.NewString())
	refused := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: id, Agent: string(fakeagent.Claude), Cwd: cwd,
		ProfileID: app.SelectedProfile(), Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30, ChiefOfStaff: protocol.Ptr(true),
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == id })
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "attn crew wake chief") {
		t.Fatalf("a launch as Chief = %+v, want it refused with the way to the Chief member", refused)
	}
	for _, session := range w.App().Initial.Sessions {
		if session.ID == id {
			t.Fatalf("the refused launch left session %+v", session)
		}
	}
	if chief := profileChief(t, w); chief.BindingSession != nil {
		t.Fatalf("the refused launch woke the Chief in %s", *chief.BindingSession)
	}
}

func TestTheRetiredChiefSettingsRefuseWritesAndNameTheChiefsOwnSettings(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for key, value := range map[string]string{
		"chief_model_claude":       "claude-opus-fake",
		"chief_effort_codex":       "low",
		"chief_context_window_cap": "180000",
	} {
		ack := gardenAdvisorSetSetting(app, key, value)
		if protocol.Deref(ack.Success) || !strings.Contains(protocol.Deref(ack.Error), "no longer controls the Chief") {
			t.Errorf("set %s = %q answered %+v, want a refusal that names what controls the Chief", key, value, ack)
		}
		if got := w.App().Initial.Settings[key]; got == value {
			t.Errorf("the refused %s is in the settings as %q", key, got)
		}
	}
}

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
	if _, _, err := before.SetProfileChief(old); err != nil {
		t.Fatal(err)
	}
	legacy := who.ToChief(profiles[0].ID)
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
