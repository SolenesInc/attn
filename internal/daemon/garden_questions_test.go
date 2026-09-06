package daemon

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func questionChange(t *testing.T, d *Daemon, sessionID, seedID string, action garden.QuestionAction, body string) protocol.Response {
	t.Helper()
	msg := protocol.SeedQuestionMessage{
		Cmd: protocol.CmdSeedQuestion, SeedID: seedID, Verb: string(action),
	}
	if sessionID != "" {
		msg.SourceSessionID = protocol.Ptr(sessionID)
	}
	if body != "" {
		msg.Body = protocol.Ptr(body)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedQuestion(c, &msg) })
}

func growingQuestionSeed(t *testing.T) (*Daemon, protocol.Seed) {
	t.Helper()
	d := newGardenDaemon(t)
	d.store.SetSetting(SettingGardenNeedsHumanEnabled, "true")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Choose the contract"})
	return d, move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
}

func TestGardenQuestionAskAnswerRoundTrip(t *testing.T) {
	d, seed := growingQuestionSeed(t)
	ask := questionChange(t, d, "sess-a", seed.ID, garden.QuestionAsk, "Which contract wins?")
	if !ask.Ok {
		t.Fatalf("ask: %s", protocol.Deref(ask.Error))
	}
	asked := ask.SeedQuestionResult.Seed
	if asked.Question == nil || asked.Question.Text != "Which contract wins?" || asked.Question.Status != garden.QuestionOpen {
		t.Fatalf("question = %+v", asked.Question)
	}
	if asked.Status != garden.StatusGrowing || asked.TenderSession != "sess-a" {
		t.Fatalf("ask moved the seed: %+v", asked)
	}
	second := questionChange(t, d, "sess-a", seed.ID, garden.QuestionAsk, "Something else?")
	if second.Ok || !strings.Contains(protocol.Deref(second.Error), asked.Question.ID) || !strings.Contains(protocol.Deref(second.Error), asked.Question.Text) {
		t.Fatalf("second ask = %+v", second)
	}

	answer := questionChange(t, d, "", seed.ID, garden.QuestionAnswer, "Keep the current wire contract.")
	if !answer.Ok {
		t.Fatalf("answer: %s", protocol.Deref(answer.Error))
	}
	if answer.SeedQuestionResult.Seed.Question != nil {
		t.Fatalf("answer left question = %+v", answer.SeedQuestionResult.Seed.Question)
	}
	note := answer.SeedQuestionResult.Note
	if note == nil || note.Kind != garden.NoteKindAnswer || !strings.Contains(note.Body, "Which contract wins?") || !strings.Contains(note.Body, "Keep the current wire contract.") {
		t.Fatalf("answer note = %+v", note)
	}
	if unread, err := d.store.HasUnreadAgentMailboxItems("sess-a"); err != nil || !unread {
		t.Fatalf("tender doorbell unread=%v err=%v", unread, err)
	}
	shown := show(t, d, seed.ID)
	if shown.Seed.Question != nil || len(shown.Notes) == 0 || shown.Notes[0].Kind != garden.NoteKindAnswer {
		t.Fatalf("show after answer = %+v", shown)
	}
}

func TestGardenQuestionAskDismissRoundTrip(t *testing.T) {
	d, seed := growingQuestionSeed(t)
	if resp := questionChange(t, d, "sess-a", seed.ID, garden.QuestionAsk, "Should we add a cache?"); !resp.Ok {
		t.Fatalf("ask: %s", protocol.Deref(resp.Error))
	}
	dismiss := questionChange(t, d, "", seed.ID, garden.QuestionDismiss, "Fix the query first.")
	if !dismiss.Ok {
		t.Fatalf("dismiss: %s", protocol.Deref(dismiss.Error))
	}
	note := dismiss.SeedQuestionResult.Note
	if note == nil || note.Kind != garden.NoteKindDismiss || !strings.Contains(note.Body, "Should we add a cache?") || !strings.Contains(note.Body, "Fix the query first.") {
		t.Fatalf("dismiss note = %+v", note)
	}
	if dismiss.SeedQuestionResult.Seed.Question != nil {
		t.Fatalf("dismiss left question = %+v", dismiss.SeedQuestionResult.Seed.Question)
	}
}

func TestGardenQuestionAskWithdrawAndClearRoundTrip(t *testing.T) {
	d, seed := growingQuestionSeed(t)
	if resp := questionChange(t, d, "sess-a", seed.ID, garden.QuestionAsk, "Which color?"); !resp.Ok {
		t.Fatalf("ask: %s", protocol.Deref(resp.Error))
	}
	addGardenSession(t, d, "sess-b")
	wrong := questionChange(t, d, "sess-b", seed.ID, garden.QuestionWithdraw, "")
	if wrong.Ok || !strings.Contains(protocol.Deref(wrong.Error), "sess-a") {
		t.Fatalf("withdraw by another session = %+v", wrong)
	}
	withdrawn := questionChange(t, d, "sess-a", seed.ID, garden.QuestionWithdraw, "")
	if !withdrawn.Ok || withdrawn.SeedQuestionResult.Seed.Question == nil || withdrawn.SeedQuestionResult.Seed.Question.Status != garden.QuestionWithdrawn {
		t.Fatalf("withdraw = %+v", withdrawn)
	}
	cleared := questionChange(t, d, "", seed.ID, garden.QuestionClear, "")
	if !cleared.Ok || cleared.SeedQuestionResult.Seed.Question != nil {
		t.Fatalf("clear = %+v", cleared)
	}
}

func TestGardenQuestionAskRefusesWhenFeatureIsOff(t *testing.T) {
	d := newGardenDaemon(t)
	if got := d.settingsWithAgentAvailability()[SettingGardenNeedsHumanEnabled]; got != "false" {
		t.Fatalf("default setting = %v, want false", got)
	}
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Choose the contract"})
	resp := questionChange(t, d, "sess-a", seed.ID, garden.QuestionAsk, "Which one?")
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), SettingGardenNeedsHumanEnabled) {
		t.Fatalf("flag-off ask = %+v", resp)
	}
	if show(t, d, seed.ID).Seed.Question != nil {
		t.Fatal("flag-off ask persisted a question")
	}
}

func TestGardenQuestionProjectionPagesPastTheGardenSnapshotLimit(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	base := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	oldestID := "s-000000"
	for i := 0; i < docstore.MaxLimit+1; i++ {
		id := fmt.Sprintf("s-%06x", i)
		seed := garden.Seed{
			ID: id, Title: id, Status: garden.StatusGrowing, StepSlug: id,
			Question: &garden.Question{
				ID: fmt.Sprintf("q-%06x", i), Text: "Which path?",
				AskedAt: formatGardenTime(base.Add(time.Duration(i) * time.Second)),
				Status:  garden.QuestionOpen,
			},
		}
		body, encodeErr := seed.Encode()
		if encodeErr != nil {
			t.Fatalf("encode seed %s: %v", id, encodeErr)
		}
		if _, err := d.store.PutDocument(*schema, id, body, base.Add(time.Duration(i)*time.Second), nil); err != nil {
			t.Fatalf("put seed %s: %v", id, err)
		}
	}

	snapshot := d.seedsForBroadcast()
	if len(snapshot) != docstore.MaxLimit {
		t.Fatalf("bounded snapshot = %d seeds, want %d", len(snapshot), docstore.MaxLimit)
	}
	for _, seed := range snapshot {
		if seed.ID == oldestID {
			t.Fatalf("bounded snapshot unexpectedly includes oldest seed %s", oldestID)
		}
	}

	questions := d.questionSeedsForBroadcast()
	if len(questions) != docstore.MaxLimit+1 {
		t.Fatalf("question projection = %d seeds, want %d", len(questions), docstore.MaxLimit+1)
	}
	if questions[len(questions)-1].ID != oldestID {
		t.Fatalf("oldest projected question = %s, want %s", questions[len(questions)-1].ID, oldestID)
	}
}
