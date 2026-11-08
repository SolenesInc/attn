package daemon_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/config"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func toTheChief() protocol.QuickCaptureMailbox {
	return toMember("chief")
}

func toMember(id string) protocol.QuickCaptureMailbox {
	return protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: protocol.Ptr(id)}
}

func sendQuickCapture(t *testing.T, cli *testworld.Peer, mailbox protocol.QuickCaptureMailbox, body string, attachments ...string) protocol.QuickCaptureSendMessage {
	t.Helper()
	msg := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: uuid.NewString(), Mailbox: mailbox, Content: body, AttachmentIds: attachments}
	if attachments == nil {
		msg.AttachmentIds = []string{}
	}
	r, err := cli.QuickCapture(msg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Record.ID != msg.CaptureID || r.Record.Content != body {
		t.Fatalf("saved receipt = %+v", r)
	}
	return msg
}
func quickCaptureRecord(t *testing.T, cli *testworld.Peer, id string) *protocol.QuickCaptureRecord {
	t.Helper()
	r, err := cli.QuickCapture(protocol.QuickCaptureGetMessage{Cmd: protocol.CmdQuickCaptureGet, CaptureID: id})
	if err != nil {
		t.Fatal(err)
	}
	return r.Record
}

func quickCapturePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	im.Set(1, 1, color.NRGBA{R: 123, G: 45, B: 67, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func uploadQuickCapture(t *testing.T, cli *testworld.Peer, quickCapture, id string, data []byte, final bool) *protocol.QuickCaptureAttachmentPutResult {
	t.Helper()
	r, err := cli.QuickCapture(protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, CaptureID: quickCapture, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(data), Final: final})
	if err != nil {
		t.Fatal(err)
	}
	return r.Upload
}
func TestQuickCaptureFilesRequireFinalizationPreserveBytesAndDiscardOnlyDrafts(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	quickCapture, id := uuid.NewString(), uuid.NewString()
	data := quickCapturePNG(t)
	source := w.Dir + "/source.png"
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	uploadQuickCapture(t, app, quickCapture, id, data, false)
	msg := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: toTheChief(), AttachmentIds: []string{id}}
	if _, err := app.QuickCapture(msg); err == nil {
		t.Fatal("incomplete upload accepted")
	}
	a := uploadQuickCapture(t, app, quickCapture, id, data, true)
	if a.Attachment.Bytes != len(data) || a.Attachment.MediaType != "image/png" {
		t.Fatalf("metadata %+v", a)
	}
	if _, err := app.QuickCapture(msg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	r, err := app.QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: quickCapture, AttachmentID: id})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(r.Download.DataBase64)
	if err != nil || !bytes.Equal(decoded, data) || !r.Download.Eof {
		t.Fatalf("saved pixels changed: %v", err)
	}
	if _, err := app.QuickCapture(protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, CaptureID: quickCapture, AttachmentID: id}); err == nil {
		t.Fatal("discard deleted committed asset")
	}
	changed := append([]byte(nil), data...)
	changed[0] ^= 1
	if _, err := app.QuickCapture(protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, CaptureID: quickCapture, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(changed), Final: true}); err == nil {
		t.Fatal("conflicting replay accepted")
	}
	draft, draftID := uuid.NewString(), uuid.NewString()
	uploadQuickCapture(t, app, draft, draftID, data, false)
	if _, err := app.QuickCapture(protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, CaptureID: draft, AttachmentID: draftID}); err != nil {
		t.Fatal(err)
	}
	list, err := app.QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 100})
	if err != nil || len(list.List.DraftAssets) != 0 {
		t.Fatalf("discard leaves drafts %+v %v", list, err)
	}
	if _, err := app.QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: draft, AttachmentID: draftID}); err == nil {
		t.Fatal("discarded asset retrieved")
	}
}

func TestConcurrentIdenticalFileQuickCapturesReturnOneSavedIdentity(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	id, asset := uuid.NewString(), uuid.NewString()
	uploadQuickCapture(t, app, id, asset, quickCapturePNG(t), true)
	msg := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: id, Mailbox: toTheChief(), Content: "same request", AttachmentIds: []string{asset}}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := app.QuickCapture(msg); errs <- err }()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	list := quickCaptureRecord(t, app, id)
	if len(list.Attachments) != 1 {
		t.Fatalf("duplicate assets %+v", list)
	}
	result, err := app.QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 100})
	if err != nil || len(result.List.Items) != 1 {
		t.Fatalf("duplicate messages %+v %v", result, err)
	}
}
func TestAppQuickCaptureRequestsAreCorrelatedAndEmptyOrMalformedMailboxsRefused(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	quickCapture, asset := uuid.NewString(), uuid.NewString()
	req := protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, RequestID: protocol.Ptr("upload"), CaptureID: quickCapture, AttachmentID: asset, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(quickCapturePNG(t)), Final: true}
	r := testworld.Request(app, req, protocol.EventQuickCaptureResult, func(r protocol.QuickCaptureResultMessage) bool { return r.RequestID == "upload" })
	if !r.Success || r.Result.Upload.Attachment == nil {
		t.Fatalf("app upload %+v", r)
	}
	sent := testworld.Request(app, protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, RequestID: protocol.Ptr("save"), CaptureID: quickCapture, Mailbox: toTheChief(), AttachmentIds: []string{asset}}, protocol.EventQuickCaptureResult, func(r protocol.QuickCaptureResultMessage) bool { return r.RequestID == "save" })
	if !sent.Success || sent.Result.Record.ID != quickCapture || len(sent.Result.Record.Attachments) != 1 {
		t.Fatalf("app save %+v", sent)
	}
	for _, mailbox := range []protocol.QuickCaptureMailbox{{Kind: protocol.QuickCaptureMailboxKind("chief")}, {Kind: protocol.QuickCaptureMailboxKindCrewMember}, {Kind: protocol.QuickCaptureMailboxKind("invalid")}} {
		r := testworld.Request(app, protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, RequestID: protocol.Ptr("refuse"), CaptureID: uuid.NewString(), Mailbox: mailbox, AttachmentIds: []string{}}, protocol.EventQuickCaptureResult, func(r protocol.QuickCaptureResultMessage) bool { return r.RequestID == "refuse" })
		if r.Success || protocol.Deref(r.Error) == "" {
			t.Fatalf("invalid quick capture accepted %+v", r)
		}
	}
}
func TestUserAndPeerMessagesShareInboxWithoutChangingAuthorship(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli, app := w.Client(), w.TrustedApp()
	registerSessions(t, w, cli, "peer")
	wakeConfiguredChief(t, w, cli)
	chief := chiefSession(t, cli)
	capture := sendQuickCapture(t, app, toTheChief(), "user request")
	sendAgentMessage(t, cli, "peer", "chief", "peer suggestion")
	batch := readInbox(t, cli, string(chief), 0)
	if len(batch.Items) != 1 || batch.Items[0].Kind != "peer_message" || string(protocol.Deref(batch.Items[0].Sender).Ref) != "session:peer" || batch.Remaining != 0 {
		t.Fatalf("the inbox read %+v, want only the peer message, attributed to its sender, with the quick capture left for the ring", batch)
	}
	_, item, err := cli.AgentInboxEntry(capture.CaptureID, chief)
	if err != nil || item == nil || item.Kind != "quick_capture" || item.Sender != nil || item.Content != "user request" {
		t.Fatalf("the quick capture reads %+v (%v), want the user's words with no sender session", item, err)
	}
}

func TestQuickCaptureHistoryPagesWithoutReadingAndReportsMissingIdentity(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	var sent []string
	for _, body := range []string{"first", "second", "third"} {
		sent = append(sent, sendQuickCapture(t, app, toTheChief(), body).CaptureID)
	}
	page, err := app.QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.List.Items) != 2 || page.List.Items[0].ID != sent[2] || page.List.Items[1].ID != sent[1] || page.List.NextCursor == nil {
		t.Fatalf("first page %+v", page)
	}
	sendQuickCapture(t, app, toTheChief(), "arrived between pages")
	tail, err := app.QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 2, Cursor: page.List.NextCursor})
	if err != nil || len(tail.List.Items) != 1 || tail.List.Items[0].ID != sent[0] || tail.List.NextCursor != nil || tail.List.Items[0].ReadAt != nil {
		t.Fatalf("tail %+v %v", tail, err)
	}
	if _, err := app.QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 0}); err == nil {
		t.Fatal("zero page size accepted")
	}
	missing := uuid.NewString()
	if _, err := app.QuickCapture(protocol.QuickCaptureGetMessage{Cmd: protocol.CmdQuickCaptureGet, CaptureID: missing}); client.ErrorCode(err) != protocol.ErrorCodeQuickCaptureNotFound {
		t.Fatalf("missing identity %v code %q", err, client.ErrorCode(err))
	}
	result := testworld.Request(app, protocol.QuickCaptureGetMessage{Cmd: protocol.CmdQuickCaptureGet, RequestID: protocol.Ptr("missing"), CaptureID: missing}, protocol.EventQuickCaptureResult, func(r protocol.QuickCaptureResultMessage) bool { return r.RequestID == "missing" })
	if result.Success || protocol.Deref(result.ErrorCode) != protocol.ErrorCodeQuickCaptureNotFound {
		t.Fatalf("app missing %+v", result)
	}
}

func TestQuickCaptureHistoryRefusesTheAgentSocket(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	quickCapture, asset := uuid.NewString(), uuid.NewString()
	uploadQuickCapture(t, app, quickCapture, asset, quickCapturePNG(t), true)
	if _, err := app.QuickCapture(protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: toTheChief(), Content: "User request", AttachmentIds: []string{asset}}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []any{
		protocol.QuickCaptureGetMessage{Cmd: protocol.CmdQuickCaptureGet, CaptureID: quickCapture},
		protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 1},
	} {
		if _, err := cli.QuickCapture(msg); err == nil || !strings.Contains(err.Error(), "app-only") {
			t.Fatalf("agent history %T: %v", msg, err)
		}
	}
	if _, err := cli.QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: quickCapture, AttachmentID: asset}); err != nil {
		t.Fatalf("agent retrieval refused: %v", err)
	}
	if r := quickCaptureRecord(t, app, quickCapture); r.ReadAt != nil {
		t.Fatalf("agent mutated quick capture: %+v", r)
	}
}

func TestQuickCaptureAuthoringRequiresTheTrustedAppIdentity(t *testing.T) {
	w := newWorld(t)
	trusted := w.TrustedApp()
	quickCapture, asset := uuid.NewString(), uuid.NewString()
	uploadQuickCapture(t, trusted, quickCapture, asset, quickCapturePNG(t), true)
	hostToken, err := os.ReadFile(filepath.Join(w.Dir, "browser-host-token"))
	if err != nil {
		t.Fatal(err)
	}
	hello := protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: "tauri-app", Version: "protocol-" + protocol.ProtocolVersion, ClientToken: protocol.Ptr(config.ClientToken()), Capabilities: []string{}}
	peers := []*testworld.Peer{w.App()}
	p := w.Connect(hello, http.Header{"Origin": {"tauri://localhost"}})
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	peers = append(peers, p)
	hello.BrowserHostToken = protocol.Ptr(string(hostToken))
	p = w.Connect(hello, nil)
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	peers = append(peers, p)
	hello.ClientKind = "harness-observer"
	p = w.Connect(hello, http.Header{"Origin": {"tauri://localhost"}})
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	peers = append(peers, p)
	send := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: toTheChief(), Content: "Only the user", AttachmentIds: []string{asset}}
	for _, peer := range peers {
		for _, msg := range []any{send,
			protocol.QuickCaptureGetMessage{Cmd: protocol.CmdQuickCaptureGet, CaptureID: quickCapture},
			protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 1},
			protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, CaptureID: quickCapture, AttachmentID: asset},
			protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, CaptureID: quickCapture, AttachmentID: asset},
		} {
			if _, err := peer.QuickCapture(msg); client.ErrorCode(err) != protocol.ErrorCodeUnauthorizedClient || !strings.Contains(err.Error(), "authenticated attn app") {
				t.Fatalf("untrusted authoring %T: %v", msg, err)
			}
		}
		if _, err := peer.QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: quickCapture, AttachmentID: asset}); err != nil {
			t.Fatalf("retrieval blocked: %v", err)
		}
	}
	if _, err := trusted.QuickCapture(send); err != nil {
		t.Fatal(err)
	}
	if r := quickCaptureRecord(t, trusted, quickCapture); r.ReadAt != nil {
		t.Fatalf("untrusted quick capture mutation: %+v", r)
	}
}

func TestAQuickCaptureWaitsForTheChiefToWakeAndReadsAsTheUsers(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli, app := w.Client(), w.TrustedApp()
	msg := sendQuickCapture(t, app, toTheChief(), "Please investigate this")
	if r := quickCaptureRecord(t, app, msg.CaptureID); r.ReadAt != nil {
		t.Fatalf("a quick capture for the asleep Chief is read: %+v", r)
	}
	setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
	woken := wakeCrew(t, cli, "chief", "")
	run := w.Launched(string(woken.SessionID))
	run.Prompted()
	if typed := run.Prompted(); !strings.Contains(typed, msg.Content) {
		t.Fatalf("the woken Chief was typed %q, want the capture that waited for it", typed)
	}
	receipt := testworld.Await(app, protocol.EventQuickCaptureRead, func(e protocol.QuickCaptureReadMessage) bool { return e.CaptureID == msg.CaptureID })
	_, item, err := cli.AgentInboxEntry(msg.CaptureID, woken.SessionID)
	if err != nil || item == nil || item.Kind != "quick_capture" || item.Content != msg.Content || item.Sender != nil || item.Address != "member:chief" || item.ReadAt != receipt.ReadAt {
		t.Fatalf("user attribution: %+v %v", item, err)
	}
	if r := quickCaptureRecord(t, app, msg.CaptureID); r.ReadAt == nil || *r.ReadAt != item.ReadAt {
		t.Fatalf("history read receipt: %+v", r)
	}
	if replay, err := app.QuickCapture(msg); err != nil || replay.Record.ID != msg.CaptureID || replay.Record.ReadAt == nil {
		t.Fatalf("lost acknowledgement replay: %+v %v", replay, err)
	}
	changed := msg
	changed.Content = "conflicting reuse"
	if _, err := app.QuickCapture(changed); err == nil {
		t.Fatal("conflicting identity accepted")
	}
	other := sendQuickCapture(t, app, msg.Mailbox, msg.Content)
	if other.CaptureID == msg.CaptureID {
		t.Fatal("intentional identical text deduplicated")
	}
}

func TestQuickCapturesWakeAMemberWithoutSpendingItsWakeLimit(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		setSetting(t, app, "crew.wake_limit", "0")
		mailbox := protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: protocol.Ptr("trellis")}
		first := sendQuickCapture(t, app, mailbox, "first user request")
		synctest.Wait()
		dayID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if dayID == "" {
			t.Fatal("a quick capture did not wake the member under crew.wake_limit=0")
		}
		arrival := testworld.Await(app, protocol.EventBackgroundLaunch, func(e protocol.BackgroundLaunchMessage) bool { return e.SessionID == dayID })
		if arrival.RequestedBy != "a quick capture" {
			t.Fatalf("wake requested_by=%q", arrival.RequestedBy)
		}
		second := sendQuickCapture(t, app, mailbox, "second user request")
		day := w.bootBubbleClaude(t, string(dayID))
		day.reply("Ready. <!-- attn:state=idle -->")
		if got := day.promptsContaining(first.Content); got != 1 {
			t.Fatalf("the wake typed the first quick capture %d times, want once", got)
		}
		if got := day.promptsContaining(second.Content); got != 1 || day.promptsContaining(inboxDoorbell) != 0 {
			t.Fatalf("the wake typed the second quick capture %d times and rang %d times; want it typed once with the first and no doorbell", got, day.promptsContaining(inboxDoorbell))
		}
		for _, sent := range []protocol.QuickCaptureSendMessage{first, second} {
			if r := quickCaptureRecord(t, app, sent.CaptureID); r.ReadAt == nil {
				t.Fatalf("a typed quick capture is unread in history: %+v", r)
			}
		}
		if items := readInbox(t, cli, string(dayID), 0).Items; len(items) != 0 {
			t.Fatalf("typed quick captures stayed in the inbox: %+v", items)
		}
		if _, err := cli.CrewHandoff(dayID, "finished the quick captures", false); err != nil {
			t.Fatal(err)
		}

		third := sendQuickCapture(t, app, mailbox, "one more thing")
		synctest.Wait()
		nextID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if nextID == "" || nextID == dayID {
			t.Fatalf("a quick capture right after the member slept woke %q, want a new session at once", nextID)
		}
		next := w.bootBubbleClaude(t, string(nextID))
		next.reply("Ready. <!-- attn:state=idle -->")
		if got := next.promptsContaining(third.Content); got != 1 {
			t.Fatalf("the second wake typed the quick capture %d times, want once: %q", got, next.prompts())
		}
	})
}

func TestAWokenMemberIsTypedItsQuickCaptureMidTurnAndRungAtItsFirstPrompt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		registerSessions(t, w, cli, "sender")
		sent := sendAgentMessage(t, cli, "sender", "trellis", "the build is green")
		mailbox := protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: protocol.Ptr("trellis")}
		capture := sendQuickCapture(t, app, mailbox, "look at the flaky test")

		day := w.bootBubbleClaude(t, string(sent.TargetSessionID))
		if got := day.promptsContaining(capture.Content); got != 1 || day.promptsContaining(inboxDoorbell) != 0 {
			t.Fatalf("the member working on its wake was typed %q, want the quick capture alone", day.prompts())
		}
		day.reply("Ready. <!-- attn:state=idle -->")
		if got := day.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("the woken member's first prompt rang %d times, want the wake's doorbell once: %q", got, day.prompts())
		}
	})
}

func TestAQuickCaptureOutlastsFailedWakesUntilTheMemberIsUp(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		workDir := w.Path("trellis-work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		setCrew(t, cli, "trellis", protocol.CrewSetMessage{Cwd: protocol.Ptr(workDir)})
		if err := os.Remove(workDir); err != nil {
			t.Fatal(err)
		}
		mailbox := protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: protocol.Ptr("trellis")}
		sent := sendQuickCapture(t, app, mailbox, "look at this when you can")
		w.advance(30 * time.Minute)
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("a member whose launch directory is gone was woken in %s", *binding)
		}

		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		day := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "trellis", "").SessionID))
		day.reply("Ready. <!-- attn:state=idle -->")
		if got := day.promptsContaining(sent.Content); got != 1 {
			t.Fatalf("after the failed wakes the manual wake typed the quick capture %d times, want once: %q", got, day.prompts())
		}
	})
}

func TestQuickCapturesBelongToTheirProfileAcrossInboxReadsAndRestart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		a := w.TrustedApp()
		profileA := a.SelectedProfile()
		profileB := createProfile(w.App(), "Other quick captures").ID
		b := w.TrustedApp(profileB)
		cli := w.Client()
		chiefs := map[string]*bubbleClaude{}
		chiefMailboxes := map[string]protocol.QuickCaptureMailbox{}
		for _, profile := range []string{profileA, profileB} {
			scoped := cli.WithGardenProfile(profile, "")
			chiefMailboxes[profile] = toMember(chiefOfRoster(t, crewRoster(t, scoped)).ID)
			setCrew(t, scoped, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
			chiefs[profile] = w.bootBubbleClaude(t, string(wakeCrew(t, scoped, "chief", "").SessionID))
			chiefs[profile].reply("Ready. <!-- attn:state=idle -->")
		}
		chiefA, chiefB := protocol.SessionID(chiefs[profileA].id), protocol.SessionID(chiefs[profileB].id)
		quickCapture, file, draft := uuid.NewString(), uuid.NewString(), uuid.NewString()
		bytesA, bytesB := []byte("profile A file"), []byte("profile B file")
		uploadQuickCapture(t, a, quickCapture, file, bytesA, true)
		uploadQuickCapture(t, a, draft, file, bytesA, true)
		msg := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: chiefMailboxes[profileA], Content: "A request", AttachmentIds: []string{file}}
		if _, err := a.QuickCapture(msg); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		receipt := testworld.Await[protocol.QuickCaptureReadMessage](a, protocol.EventQuickCaptureRead, func(e protocol.QuickCaptureReadMessage) bool {
			return e.CaptureID == quickCapture && e.ProfileID == profileA
		})

		if _, err := b.QuickCapture(protocol.QuickCaptureGetMessage{Cmd: protocol.CmdQuickCaptureGet, CaptureID: quickCapture}); client.ErrorCode(err) != protocol.ErrorCodeQuickCaptureNotFound {
			t.Fatalf("other-profile get: %v", err)
		}
		if _, err := b.QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: quickCapture, AttachmentID: file}); err == nil {
			t.Fatal("other profile downloaded file")
		}
		if _, err := cli.WithGardenProfile(profileB, chiefB).QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: quickCapture, AttachmentID: file}); err == nil {
			t.Fatal("other-profile CLI downloaded file")
		}
		if _, _, err := cli.AgentInboxEntry(quickCapture, chiefB); err == nil {
			t.Fatal("other Chief read A quick capture")
		}
		list, err := b.QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 1})
		if err != nil || len(list.List.Items) != 0 || len(list.List.DraftAssets) != 0 {
			t.Fatalf("other-profile history: %+v %v", list, err)
		}
		if _, err := b.QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 1, Cursor: &quickCapture}); err == nil {
			t.Fatal("other profile used A cursor")
		}
		if _, err := b.QuickCapture(protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, CaptureID: draft, AttachmentID: file}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: draft, AttachmentID: file}); err != nil {
			t.Fatalf("other-profile discard deleted A file: %v", err)
		}
		synctest.Wait()
		for _, event := range b.Received() {
			if event.Event == protocol.EventQuickCaptureRead {
				t.Fatalf("other profile got quick capture event: %+v", event)
			}
		}
		uploadQuickCapture(t, b, quickCapture, file, bytesB, true)
		msg.Content = "B request"
		msg.Mailbox = chiefMailboxes[profileB]
		if _, err := b.QuickCapture(msg); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if _, itemB, err := cli.AgentInboxEntry(quickCapture, chiefB); err != nil || itemB == nil || itemB.Content != "B request" {
			t.Fatalf("B chief reads %+v, %v", itemB, err)
		}
		if r := quickCaptureRecord(t, a, quickCapture); r.ReadAt == nil || *r.ReadAt != receipt.ReadAt {
			t.Fatalf("B's read changed A's receipt: %+v, want read at %s", r, receipt.ReadAt)
		}
		_, itemA, err := cli.AgentInboxEntry(quickCapture, chiefA)
		if err != nil || itemA == nil || itemA.Content != "A request" {
			t.Fatalf("A chief reads %+v, %v", itemA, err)
		}
		if receipt.ReadAt != itemA.ReadAt {
			t.Fatalf("read receipt: %+v", receipt)
		}

		for range 2 {
			if _, _, err := cli.AgentInboxEntry(quickCapture, chiefA); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		readEvents := 0
		for _, event := range a.Received() {
			if event.Event == protocol.EventQuickCaptureRead {
				readEvents++
			}
		}
		if readEvents != 1 {
			t.Fatalf("re-reading emitted %d read events, want 1", readEvents)
		}

		w.restart()
		for _, owner := range []struct {
			profile string
			bytes   []byte
		}{{profileA, bytesA}, {profileB, bytesB}} {
			app := w.TrustedApp(owner.profile)
			r, err := app.QuickCapture(protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: quickCapture, AttachmentID: file})
			if err != nil {
				t.Fatal(err)
			}
			got, err := base64.StdEncoding.DecodeString(r.Download.DataBase64)
			if err != nil || !bytes.Equal(got, owner.bytes) {
				t.Fatalf("profile %s bytes=%q, %v", owner.profile, got, err)
			}
			if quickCaptureRecord(t, app, quickCapture).ReadAt == nil {
				t.Fatal("read receipt lost on restart")
			}
		}
		b = w.TrustedApp(profileB)
		if _, err := b.QuickCapture(protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, ProfileID: &profileA, CaptureID: uuid.NewString(), Mailbox: msg.Mailbox, Content: "queued A request", AttachmentIds: []string{}}); err == nil {
			t.Fatal("profile switch redirected queued quick capture")
		}
	})
}

func TestAQuickCaptureForTheChiefReachesItAwakeAndWakesItsNextDay(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, trusted, cli := w.App(), w.TrustedApp(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		setCrew(t, cli, "chief", protocol.CrewSetMessage{Agent: protocol.Ptr("claude")})
		chief := w.bootBubbleClaude(t, string(wakeCrew(t, cli, "chief", "").SessionID))
		chief.reply("Ready. <!-- attn:state=idle -->")
		chiefMailbox := toTheChief()

		sendQuickCapture(t, trusted, chiefMailbox, "Captured for the live chief")
		w.advance(0)
		if pasted := strings.Join(chief.term.Pasted(), "\n"); !strings.Contains(pasted, "Captured for the live chief") {
			t.Fatalf("the live chief was typed %q, want the new capture", pasted)
		}

		crewHandoff(t, cli, chief.id, "Back when the user writes.", false)
		sendQuickCapture(t, trusted, chiefMailbox, "Captured while the chief slept")
		synctest.Wait()
		nextID := protocol.Deref(chiefOfRoster(t, crewRoster(t, cli)).BindingSession)
		if nextID == "" || string(nextID) == chief.id {
			t.Fatalf("a quick capture for the sleeping Chief woke %q, want its next day", nextID)
		}
		next := w.bootBubbleClaude(t, string(nextID))
		next.reply("Ready. <!-- attn:state=idle -->")
		if got := next.promptsContaining("Captured while the chief slept"); got != 1 {
			t.Fatalf("the Chief's next day was typed the capture %d times, want once: %q", got, next.prompts())
		}
	})
}
