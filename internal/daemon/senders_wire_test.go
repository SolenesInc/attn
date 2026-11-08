package daemon_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type pairOutcome struct {
	token *protocol.PairedSenderToken
	err   error
}

func requestSenderPairing(t *testing.T, cli *client.Client, name string) (protocol.SenderPairingRequest, <-chan pairOutcome) {
	t.Helper()
	printed := make(chan protocol.SenderPairingRequest, 1)
	outcome := make(chan pairOutcome, 1)
	go func() {
		token, err := cli.SenderPair(name, func(r protocol.SenderPairingRequest) { printed <- r })
		outcome <- pairOutcome{token, err}
	}()
	select {
	case r := <-printed:
		return r, outcome
	case o := <-outcome:
		t.Fatalf("pairing %s ended before asking the user: %v", name, o.err)
	}
	return protocol.SenderPairingRequest{}, nil
}

func decideSenderPairing(t *testing.T, app *testworld.Peer, pairingID string, allow bool) protocol.SenderPairDecideResultMessage {
	t.Helper()
	id := uuid.NewString()
	return testworld.Request(app, protocol.SenderPairDecideMessage{Cmd: protocol.CmdSenderPairDecide, RequestID: id, PairingID: pairingID, Allow: allow}, protocol.EventSenderPairDecideResult, func(r protocol.SenderPairDecideResultMessage) bool { return r.RequestID == id })
}

func authoringAs(token *string, profileID string) []any {
	captureID, attachmentID := uuid.NewString(), uuid.NewString()
	return []any{
		protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, ProfileID: &profileID, SenderToken: token, CaptureID: captureID, AttachmentID: attachmentID, Name: "notes.txt", DataBase64: base64.StdEncoding.EncodeToString([]byte("notes")), Final: true},
		protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, ProfileID: &profileID, SenderToken: token, CaptureID: captureID, AttachmentID: attachmentID},
		protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, ProfileID: &profileID, SenderToken: token, CaptureID: uuid.NewString(), Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("target"))}, Content: "As the user", AttachmentIds: []string{}},
	}
}

func TestOnlyAPairedSenderAuthorsQuickCapturesOnTheCommandSocket(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	registerSessions(t, w, cli, "target")
	profileID := app.SelectedProfile()

	for _, token := range []*string{nil, protocol.Ptr("not-a-paired-token")} {
		for _, msg := range authoringAs(token, profileID) {
			if _, err := cli.QuickCapture(msg); err == nil || !strings.Contains(err.Error(), "attn send pair") {
				t.Fatalf("%T with token %v: %v", msg, token, err)
			}
		}
	}

	if _, err := cli.WithCaller(protocol.TerminalID(w.Terminal("target"))).SenderPair("From an agent", func(protocol.SenderPairingRequest) { t.Error("an agent's pairing reached the user") }); err == nil || !strings.Contains(err.Error(), "attn refuses those") {
		t.Fatalf("pairing from inside a session: %v", err)
	}
	denied, deniedOutcome := requestSenderPairing(t, cli, "Denied")
	if _, err := cli.SenderPair("Second", func(protocol.SenderPairingRequest) { t.Error("a second pairing reached the user while one waited") }); err == nil || !strings.Contains(err.Error(), "one program at a time") {
		t.Fatalf("a second pairing while one waits: %v", err)
	}
	if r := decideSenderPairing(t, w.App(), denied.ID, true); r.Success {
		t.Fatal("a client that is not the authenticated app allowed a sender")
	}
	shown := testworld.Request(app, protocol.SendersGetMessage{Cmd: protocol.CmdSendersGet}, protocol.EventSendersUpdated, func(m protocol.SendersUpdatedMessage) bool { return m.Request != nil })
	if *shown.Request != denied || len(denied.Code) != 4 {
		t.Fatalf("the app shows %+v; the program was told %+v", shown.Request, denied)
	}
	if r := decideSenderPairing(t, app, denied.ID, false); !r.Success {
		t.Fatal(r)
	}
	if o := <-deniedOutcome; o.err == nil || !strings.Contains(o.err.Error(), "denied") {
		t.Fatalf("denied pairing: %+v", o)
	}

	allowed, allowedOutcome := requestSenderPairing(t, cli, "Capture")
	if r := decideSenderPairing(t, app, allowed.ID, true); !r.Success {
		t.Fatal(r)
	}
	paired := <-allowedOutcome
	if paired.err != nil || paired.token.Token == "" || paired.token.Sender.Name != "Capture" {
		t.Fatalf("allowed pairing: %+v", paired)
	}
	taken, takenOutcome := requestSenderPairing(t, cli, "capture")
	if r := decideSenderPairing(t, app, taken.ID, true); r.Success || !strings.Contains(protocol.Deref(r.Error), `already has a sender named "capture"`) {
		t.Fatalf("allowing a name the profile already paired: %+v", r)
	}
	if r := decideSenderPairing(t, app, taken.ID, false); !r.Success {
		t.Fatalf("denying after the refused allow: %+v", r)
	}
	if o := <-takenOutcome; o.err == nil || !strings.Contains(o.err.Error(), "denied") {
		t.Fatalf("pairing a taken name: %+v", o)
	}
	for _, msg := range authoringAs(&paired.token.Token, profileID) {
		if _, err := cli.QuickCapture(msg); err != nil {
			t.Fatalf("paired %T: %v", msg, err)
		}
	}
	senders, err := cli.PairedSenders("")
	if err != nil || len(senders) != 1 || senders[0].ID != paired.token.Sender.ID || senders[0].LastUsedAt == nil {
		t.Fatalf("paired senders after use: %+v %v", senders, err)
	}

	if err := cli.UnpairSender("", "Capture"); err != nil {
		t.Fatal(err)
	}
	for _, msg := range authoringAs(&paired.token.Token, profileID) {
		if _, err := cli.QuickCapture(msg); err == nil || !strings.Contains(err.Error(), "no paired sender") {
			t.Fatalf("unpaired %T: %v", msg, err)
		}
	}
	if err := cli.UnpairSender("", "Capture"); err == nil || !strings.Contains(err.Error(), "attn send senders") {
		t.Fatalf("unpairing a sender twice: %v", err)
	}
}

func TestASessionMailboxNamesOneRunningSessionOfTheProfile(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	other := createProfile(w.App(), "Other senders").ID
	if err := w.InjectSession("elsewhere", "elsewhere", w.Path("elsewhere"), protocol.SessionAgentClaude, other); err != nil {
		t.Fatal(err)
	}
	registerSessions(t, w, w.Client(), "here")
	for _, c := range []struct {
		mailbox protocol.QuickCaptureMailbox
		refusal string
	}{
		{protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("elsewhere"))}, "belongs to another profile"},
		{protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("gone"))}, "is not running"},
		{protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession}, "requires session_id"},
		{protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("here")), MemberID: protocol.Ptr("fern")}, "cannot include member_id"},
		{protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, SessionID: protocol.Ptr(protocol.SessionID("here"))}, "cannot include session_id"},
	} {
		msg := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: uuid.NewString(), Mailbox: c.mailbox, Content: "Wrong mailbox", AttachmentIds: []string{}}
		if _, err := app.QuickCapture(msg); err == nil || !strings.Contains(err.Error(), c.refusal) {
			t.Fatalf("mailbox %+v: %v, want %q", c.mailbox, err, c.refusal)
		}
	}
	sent := sendQuickCapture(t, app, protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("here"))}, "Right mailbox")
	if r := quickCaptureRecord(t, app, sent.CaptureID); r.Mailbox.Kind != protocol.QuickCaptureMailboxKindSession || protocol.Deref(r.Mailbox.SessionID) != "here" {
		t.Fatalf("saved mailbox: %+v", r.Mailbox)
	}
}

func pairSenderInApp(t *testing.T, cli *client.Client, app *testworld.Peer, name string) *protocol.PairedSenderToken {
	t.Helper()
	request, outcome := requestSenderPairing(t, cli, name)
	if r := decideSenderPairing(t, app, request.ID, true); !r.Success {
		t.Fatalf("allowing %s: %+v", name, r)
	}
	paired := <-outcome
	if paired.err != nil {
		t.Fatalf("pairing %s: %v", name, paired.err)
	}
	return paired.token
}

func TestAPairedSenderBelongsToTheProfileTheAppShowed(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	registerSessions(t, w, cli, "target")
	home := app.SelectedProfile()
	homeSender := pairSenderInApp(t, cli, app, "Capture")

	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	if shown := testworld.Request(app, protocol.SendersGetMessage{Cmd: protocol.CmdSendersGet}, protocol.EventSendersUpdated, func(protocol.SendersUpdatedMessage) bool { return true }); len(shown.Senders) != 0 {
		t.Fatalf("the app on Side shows Home's senders: %+v", shown.Senders)
	}
	sideSender := pairSenderInApp(t, cli, app, "Capture")

	for profile, want := range map[string]string{home: homeSender.Sender.ID, side.ID: sideSender.Sender.ID} {
		if listed, err := cli.PairedSenders(profile); err != nil || len(listed) != 1 || listed[0].ID != want {
			t.Fatalf("senders of profile %s = %+v %v, want only %s", profile, listed, err, want)
		}
	}
	for _, msg := range authoringAs(&sideSender.Token, home) {
		if _, err := cli.QuickCapture(msg); err == nil || !strings.Contains(err.Error(), `"Side"`) || !strings.Contains(err.Error(), home) {
			t.Fatalf("Side's sender authoring %T in Home: %v", msg, err)
		}
	}
	if err := cli.UnpairSender(side.ID, homeSender.Sender.ID); err == nil {
		t.Fatal("Side unpaired Home's sender")
	}

	selectProfile(app, home)
	deleted := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: "delete-side", ProfileID: side.ID, ExpectedRevision: side.Revision}, "delete-side")
	if !deleted.Success {
		t.Fatalf("delete Side: %+v", deleted)
	}
	for _, msg := range authoringAs(&sideSender.Token, side.ID) {
		if _, err := cli.QuickCapture(msg); err == nil || !strings.Contains(err.Error(), "no paired sender") {
			t.Fatalf("a deleted profile's sender authoring %T: %v", msg, err)
		}
	}
	if listed, err := cli.PairedSenders(home); err != nil || len(listed) != 1 {
		t.Fatalf("Home's senders after deleting Side: %+v %v", listed, err)
	}
}

func TestAPairedSenderTokenNeverReachesTheDatabase(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	registerSessions(t, w, cli, "target")
	paired := pairSenderInApp(t, cli, app, "Capture")
	profileID := app.SelectedProfile()

	send := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, ProfileID: &profileID, SenderToken: &paired.Token, CaptureID: uuid.NewString(), Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("target"))}, Content: "Sent once", AttachmentIds: []string{}}
	for range 2 {
		if _, err := cli.QuickCapture(send); err != nil {
			t.Fatalf("sending, then retrying, capture %s: %v", send.CaptureID, err)
		}
	}
	changed := send
	changed.Content = "Sent twice"
	if _, err := cli.QuickCapture(changed); err == nil || !strings.Contains(err.Error(), "different payload") {
		t.Fatalf("reusing capture %s for other content: %v", send.CaptureID, err)
	}

	files, err := filepath.Glob(filepath.Join(w.Dir, "attn.db*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("database files in %s: %v %v", w.Dir, files, err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(paired.Token)) {
			t.Fatalf("%s holds the paired sender's token", file)
		}
	}
}
