package daemon_test

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func saveDraft(app *testworld.Peer, askID string, draft protocol.AskDraft) protocol.AskDraftSaveResultMessage {
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.AskDraftSaveMessage{
		Cmd: protocol.CmdAskDraftSave, RequestID: requestID, AskID: askID, Draft: draft,
	}, protocol.EventAskDraftSaveResult, func(r protocol.AskDraftSaveResultMessage) bool { return r.RequestID == requestID })
}

func shownAsk(asks []protocol.Ask, id string) *protocol.Ask {
	for i := range asks {
		if asks[i].ID == id {
			return &asks[i]
		}
	}
	return nil
}

func TestAHalfWrittenAnswerOutlivesARestartAndAnAmendmentAndGoesWithTheAnswer(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	seed := plantSeedAs(t, cli, asker, "Pick the cache store")
	moveDelegationSeed(t, cli, asker, seed, "tend", "")
	asked, err := cli.AskOpen(protocol.SessionID(asker), seed, cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	trusted := w.TrustedApp()
	owner := protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: asked.ID}
	shot := quickCapturePNG(t)
	ready := answerFile(t, trusted, owner, "Pasted screenshot.png", shot, true)
	half := answerFile(t, trusted, owner, "still uploading.png", shot[:8], false)

	draft := protocol.AskDraft{
		Generation:  2,
		Answers:     []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}, Condition: protocol.Ptr("only once staging has it")}},
		Note:        protocol.Ptr("Half of what I mean"),
		Attachments: []string{ready, half},
	}
	if saved := saveDraft(app, asked.ID, draft); !saved.Success {
		t.Fatalf("saving the draft = %+v", saved)
	}
	older := draft
	older.Generation, older.Note = 1, protocol.Ptr("an edit that arrived late")
	if refused := saveDraft(app, asked.ID, older); refused.Success || !protocol.Deref(refused.Stale) || refused.Generation != 2 {
		t.Fatalf("a save at generation 1 after 2 = %+v, want it refused as stale, naming generation 2", refused)
	}
	if shown, err := cli.AskShow(protocol.SessionID(asker), asked.ID); err != nil || shown.Ask.Draft != nil {
		t.Fatalf("the agent's ask show = %+v, %v; want the ask without the user's draft", shown, err)
	}

	if _, err := cli.AskAmend(protocol.SessionID(asker), asked.ID, "Redis already runs in staging.", w.Path("shop")); err != nil {
		t.Fatalf("amend: %v", err)
	}
	amended := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool {
		a := shownAsk(m.Asks, asked.ID)
		return a != nil && len(a.Parts) == 3
	})
	if a := shownAsk(amended.Asks, asked.ID); !reflect.DeepEqual(a.Draft, &draft) || a.Parts[1].Question == nil || a.Parts[1].Question.Key != "q1" {
		t.Fatalf("after the amendment the app reads the draft %+v and question %+v, want the draft as saved and the question still q1", a.Draft, a.Parts[1].Question)
	}

	w.restart()
	app = w.App()
	restored := shownAsk(app.Initial.Asks, asked.ID)
	if restored == nil || !reflect.DeepEqual(restored.Draft, &draft) {
		t.Fatalf("after a restart the app reads %+v, want the draft %+v", restored, draft)
	}
	files := []protocol.AskDraftFile{
		{Attachment: protocol.Attachment{ID: ready, Name: "Pasted screenshot.png", MediaType: "image/png", Bytes: len(shot)}, Ready: true},
		{Attachment: protocol.Attachment{ID: half, Name: "still uploading.png", Bytes: 8}},
	}
	if !reflect.DeepEqual(restored.DraftFiles, files) {
		t.Fatalf("the draft's files read %+v, want %+v", restored.DraftFiles, files)
	}

	if answered := answerWithFiles(w.TrustedApp(), asked.ID, "Postgres, then.", ready); !answered.Success {
		t.Fatalf("answering: %s", protocol.Deref(answered.Error))
	}
	replied := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool {
		a := shownAsk(m.Asks, asked.ID)
		return a != nil && a.Reply != nil
	})
	if a := shownAsk(replied.Asks, asked.ID); a.Draft != nil || a.DraftFiles != nil {
		t.Fatalf("the replied ask still carries the draft %+v and files %+v", a.Draft, a.DraftFiles)
	}
	later := draft
	later.Generation = 3
	if refused := saveDraft(app, asked.ID, later); refused.Success {
		t.Fatalf("a draft saved for an answered ask = %+v, want it refused", refused)
	}
}

func TestAClosedAskTakesNoDraft(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	draft := protocol.AskDraft{Generation: 1, Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Redis"}}}}
	if saved := saveDraft(app, asked.ID, draft); !saved.Success {
		t.Fatalf("saving the draft = %+v", saved)
	}
	if err := cli.AskClose(protocol.SessionID(asker), asked.ID, ""); err != nil {
		t.Fatalf("close: %v", err)
	}
	draft.Generation = 2
	if refused := saveDraft(app, asked.ID, draft); refused.Success || refused.Error == nil {
		t.Fatalf("a draft saved for a closed ask = %+v, want it refused", refused)
	}
}

func TestADraftThisBuildCannotReadLeavesItsAskReadableAndWritable(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	seed := plantSeedAs(t, cli, asker, "Pick the cache store")
	moveDelegationSeed(t, cli, asker, seed, "tend", "")
	asked, err := cli.AskOpen(protocol.SessionID(asker), seed, cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	if saved := saveDraft(app, asked.ID, protocol.AskDraft{Generation: 3, Note: protocol.Ptr("kept for later")}); !saved.Success {
		t.Fatalf("saving the draft = %+v", saved)
	}

	w.stop()
	db, err := sql.Open("sqlite3", filepath.Join(w.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE ask_drafts SET draft = ? WHERE ask_id = ?", `{"generation":3,"answers":{"q1":{"files":["f-1"]}}}`, asked.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w.start()

	app = w.App()
	shown := shownAsk(app.Initial.Asks, asked.ID)
	if shown == nil || shown.Draft != nil {
		t.Fatalf("after a later build's draft the app reads %+v, want the ask without a draft", shown)
	}
	if again := saveDraft(app, asked.ID, protocol.AskDraft{Generation: 4, Note: protocol.Ptr("written again")}); !again.Success {
		t.Fatalf("saving over the unreadable draft = %+v", again)
	}
}

func TestADraftKeepsTheFilesOfAFileQuestionThroughARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(asker)
	seed := plantSeedAs(t, cli, asker, "Ship the banner")
	moveDelegationSeed(t, cli, asker, seed, "tend", "")
	asked, err := cli.AskOpen(protocol.SessionID(asker), seed, screenshotAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	owner := protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: asked.ID}
	shot := quickCapturePNG(t)
	onQuestion := answerFile(t, w.TrustedApp(), owner, "Pasted screenshot.png", shot, true)
	draft := protocol.AskDraft{
		Generation: 1,
		Answers: []protocol.AskAnswer{
			{Key: "q1", Picked: []string{"Yes"}},
			{Key: "q2", Picked: []string{}, Attachments: []string{onQuestion}},
		},
	}
	if saved := saveDraft(app, asked.ID, draft); !saved.Success {
		t.Fatalf("saving the draft = %+v", saved)
	}

	w.restart()
	restored := shownAsk(w.App().Initial.Asks, asked.ID)
	if restored == nil || !reflect.DeepEqual(restored.Draft, &draft) {
		t.Fatalf("after a restart the app reads %+v, want the draft %+v", restored, draft)
	}
	want := []protocol.AskDraftFile{{Attachment: protocol.Attachment{ID: onQuestion, Name: "Pasted screenshot.png", MediaType: "image/png", Bytes: len(shot)}, Ready: true}}
	if !reflect.DeepEqual(restored.DraftFiles, want) {
		t.Fatalf("the draft's files read %+v, want the file question's %+v", restored.DraftFiles, want)
	}
}
