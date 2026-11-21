package daemon_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/testworld"
)

func answerFile(t *testing.T, app *testworld.Peer, owner protocol.AttachmentOwner, name string, data []byte, final bool) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := app.Attachment(protocol.AttachmentPutMessage{Cmd: protocol.CmdAttachmentPut, Owner: owner, AttachmentID: id, Name: name, DataBase64: base64.StdEncoding.EncodeToString(data), Final: final}); err != nil {
		t.Fatalf("upload %s: %v", name, err)
	}
	return id
}

func answerWithFiles(app *testworld.Peer, askID, note string, files ...string) protocol.AskAnswerResultMessage {
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: requestID, AskID: askID,
		Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}}, Note: protocol.Ptr(note), Attachments: files,
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == requestID })
}

func TestAnAnswerCarriesItsFilesToTheAgentAndTheSeedNote(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "asks_enabled", "true")
	setSetting(t, app, "phone_access_enabled", "true")
	phone := connectPhone(t, w, "phone", app.SelectedProfile())
	phone.welcomed()
	asker := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := w.Launched(asker)
	seed := plantSeedAs(t, cli, asker, "Pick the cache store")
	moveDelegationSeed(t, cli, asker, seed, "tend", "")
	seedless, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	about, err := cli.AskOpen(protocol.SessionID(asker), seed, cacheAsk, "", w.Path("shop"))
	if err != nil {
		t.Fatal(err)
	}
	trusted := w.TrustedApp()
	owner := func(askID string) protocol.AttachmentOwner {
		return protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindAsk, ID: askID}
	}
	screenshot, notes := quickCapturePNG(t), []byte("cache hit rate 92%\n")
	if _, err := trusted.Attachment(protocol.AttachmentPutMessage{Cmd: protocol.CmdAttachmentPut, Owner: owner("a-nothere"), AttachmentID: uuid.NewString(), Name: "x.png", DataBase64: base64.StdEncoding.EncodeToString(screenshot), Final: true}); err == nil {
		t.Fatal("an upload for an ask that does not exist was accepted")
	}

	shot := answerFile(t, trusted, owner(seedless.ID), "Pasted screenshot.png", screenshot, true)
	text := answerFile(t, trusted, owner(seedless.ID), "hit-rate.txt", notes, true)
	half := answerFile(t, trusted, owner(seedless.ID), "still uploading.png", screenshot[:8], false)
	capture := uuid.NewString()
	captured := answerFile(t, trusted, ownedBy(capture), "captured.png", screenshot, true)
	theirs := answerFile(t, trusted, owner(about.ID), "for the other ask.png", screenshot, true)
	for file, refusal := range map[string]string{
		half:     half + ` ("still uploading.png") is staged`,
		captured: captured + " was not uploaded for this ask",
		theirs:   theirs + " was not uploaded for this ask",
	} {
		if refused := answerWithFiles(trusted, seedless.ID, "with a bad file", shot, file); refused.Success || !strings.Contains(protocol.Deref(refused.Error), refusal) {
			t.Errorf("answering with %s = %+v, want a refusal saying %q", file, refused, refusal)
		}
	}

	if answered := answerWithFiles(trusted, seedless.ID, "Numbers attached.", shot, text); !answered.Success {
		t.Fatalf("answering with two files failed: %s", protocol.Deref(answered.Error))
	}
	typed := agent.Prompted()
	for _, want := range []string{
		"> Numbers attached.",
		`File "Pasted screenshot.png" (image/png, `,
		"attn agent attachment " + shot + " --out <path>",
		`File "hit-rate.txt" (text/plain; charset=utf-8, 19 bytes)`,
		"attn agent attachment " + text + " --out <path>",
	} {
		if !strings.Contains(typed, want) {
			t.Errorf("the agent was typed %q, missing %q", typed, want)
		}
	}
	replied := testworld.Await(app, protocol.EventAsksUpdated, func(m protocol.AsksUpdatedMessage) bool {
		for _, a := range m.Asks {
			if a.ID == seedless.ID && a.Reply != nil {
				return true
			}
		}
		return false
	})
	for _, a := range replied.Asks {
		if a.ID == seedless.ID && (len(a.Reply.Attachments) != 2 || a.Reply.Attachments[0].Name != "Pasted screenshot.png" || a.Reply.Attachments[1].Name != "hit-rate.txt") {
			t.Errorf("the app shows the reply's files as %+v, want the screenshot then the text", a.Reply.Attachments)
		}
	}

	onPhone := phone.showsAsks(func(asks []homegateway.HomeGatewayAsk) bool {
		return slices.ContainsFunc(asks, func(a homegateway.HomeGatewayAsk) bool { return a.ID == seedless.ID && a.Reply != nil })
	})
	for _, a := range onPhone {
		if a.ID == seedless.ID && !slices.Equal(a.Reply.AttachmentNames, []string{"Pasted screenshot.png", "hit-rate.txt"}) {
			t.Errorf("the phone names the reply's files %v, want the screenshot then the text", a.Reply.AttachmentNames)
		}
	}

	addressee := cli.WithCaller(protocol.TerminalID(app.Terminal(asker)))
	for id, want := range map[string][]byte{shot: screenshot, text: notes} {
		saved := filepath.Join(w.Path("shop"), id)
		var result any
		if err := addressee.RunCommand("agent attachment", "", map[string]any{"attachment": id, "out": saved}, &result); err != nil {
			t.Fatalf("the addressee fetches %s: %v", id, err)
		}
		if data, err := os.ReadFile(saved); err != nil || !bytes.Equal(data, want) {
			t.Errorf("attn agent attachment %s wrote %d bytes, %v; want the uploaded file", id, len(data), err)
		}
	}
	if _, err := trusted.Attachment(protocol.AttachmentDiscardMessage{Cmd: protocol.CmdAttachmentDiscard, AttachmentID: shot}); err == nil || !strings.Contains(err.Error(), "already sent") {
		t.Errorf("removing a sent file = %v, want a refusal", err)
	}

	if answered := answerWithFiles(trusted, about.ID, "", theirs); !answered.Success {
		t.Fatalf("answering the seed's ask failed: %s", protocol.Deref(answered.Error))
	}
	listed, err := cli.SeedNotes("", seed, client.SeedNotesQuery{Limit: 1})
	if err != nil || len(listed.Notes) != 1 {
		t.Fatalf("the seed's notes are %+v, %v", listed, err)
	}
	if body := listed.Notes[0].Body; !strings.Contains(body, `File "for the other ask.png"`) || !strings.Contains(body, "attn agent attachment "+theirs+" --out <path>") {
		t.Errorf("the seed's answer note reads %q, want the file line", body)
	}
}
