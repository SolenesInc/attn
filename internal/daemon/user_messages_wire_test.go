package daemon_test

import (
	"bytes"
	"encoding/base64"
	"github.com/victorarias/attn/internal/config"
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

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func sendCapture(t *testing.T, cli *testworld.Peer, target protocol.CaptureTarget, body string, attachments ...string) protocol.CaptureSendMessage {
	t.Helper()
	msg := protocol.CaptureSendMessage{Cmd: protocol.CmdCaptureSend, CaptureID: uuid.NewString(), Target: target, Content: body, AttachmentIds: attachments}
	if attachments == nil {
		msg.AttachmentIds = []string{}
	}
	r, err := cli.Capture(msg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Record.ID != msg.CaptureID || r.Record.Content != body {
		t.Fatalf("saved receipt = %+v", r)
	}
	return msg
}
func captureRecord(t *testing.T, cli *testworld.Peer, id string) *protocol.CaptureRecord {
	t.Helper()
	r, err := cli.Capture(protocol.CaptureGetMessage{Cmd: protocol.CmdCaptureGet, CaptureID: id})
	if err != nil {
		t.Fatal(err)
	}
	return r.Record
}

func capturePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	im.Set(1, 1, color.NRGBA{R: 123, G: 45, B: 67, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func uploadCapture(t *testing.T, cli *testworld.Peer, capture, id string, data []byte, final bool) *protocol.CaptureAttachmentPutResult {
	t.Helper()
	r, err := cli.Capture(protocol.CaptureAttachmentPutMessage{Cmd: protocol.CmdCaptureAttachmentPut, CaptureID: capture, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(data), Final: final})
	if err != nil {
		t.Fatal(err)
	}
	return r.Upload
}
func TestCaptureImagesRequireFinalizationPreserveBytesAndDiscardOnlyDrafts(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	capture, id := uuid.NewString(), uuid.NewString()
	data := capturePNG(t)
	source := w.Dir + "/source.png"
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	uploadCapture(t, app, capture, id, data, false)
	msg := protocol.CaptureSendMessage{Cmd: protocol.CmdCaptureSend, CaptureID: capture, Target: protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, AttachmentIds: []string{id}}
	if _, err := app.Capture(msg); err == nil {
		t.Fatal("incomplete upload accepted")
	}
	a := uploadCapture(t, app, capture, id, data, true)
	if a.Attachment.Bytes != len(data) || a.Attachment.MediaType != "image/png" {
		t.Fatalf("metadata %+v", a)
	}
	if _, err := app.Capture(msg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	r, err := app.Capture(protocol.CaptureAttachmentGetMessage{Cmd: protocol.CmdCaptureAttachmentGet, CaptureID: capture, AttachmentID: id})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(r.Download.DataBase64)
	if err != nil || !bytes.Equal(decoded, data) || !r.Download.Eof {
		t.Fatalf("saved pixels changed: %v", err)
	}
	if _, err := app.Capture(protocol.CaptureAttachmentDiscardMessage{Cmd: protocol.CmdCaptureAttachmentDiscard, CaptureID: capture, AttachmentID: id}); err == nil {
		t.Fatal("discard deleted committed asset")
	}
	changed := append([]byte(nil), data...)
	changed[0] ^= 1
	if _, err := app.Capture(protocol.CaptureAttachmentPutMessage{Cmd: protocol.CmdCaptureAttachmentPut, CaptureID: capture, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(changed), Final: true}); err == nil {
		t.Fatal("conflicting replay accepted")
	}
	draft, draftID := uuid.NewString(), uuid.NewString()
	uploadCapture(t, app, draft, draftID, data, false)
	if _, err := app.Capture(protocol.CaptureAttachmentDiscardMessage{Cmd: protocol.CmdCaptureAttachmentDiscard, CaptureID: draft, AttachmentID: draftID}); err != nil {
		t.Fatal(err)
	}
	list, err := app.Capture(protocol.CaptureListMessage{Cmd: protocol.CmdCaptureList, Limit: 100})
	if err != nil || len(list.List.DraftAssets) != 0 {
		t.Fatalf("discard leaves drafts %+v %v", list, err)
	}
	if _, err := app.Capture(protocol.CaptureAttachmentGetMessage{Cmd: protocol.CmdCaptureAttachmentGet, CaptureID: draft, AttachmentID: draftID}); err == nil {
		t.Fatal("discarded asset retrieved")
	}
}

func TestConcurrentIdenticalImageCapturesReturnOneSavedIdentity(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	id, asset := uuid.NewString(), uuid.NewString()
	uploadCapture(t, app, id, asset, capturePNG(t), true)
	msg := protocol.CaptureSendMessage{Cmd: protocol.CmdCaptureSend, CaptureID: id, Target: protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, Content: "same request", AttachmentIds: []string{asset}}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := app.Capture(msg); errs <- err }()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	list := captureRecord(t, app, id)
	if len(list.Attachments) != 1 {
		t.Fatalf("duplicate assets %+v", list)
	}
	result, err := app.Capture(protocol.CaptureListMessage{Cmd: protocol.CmdCaptureList, Limit: 100})
	if err != nil || len(result.List.Items) != 1 {
		t.Fatalf("duplicate messages %+v %v", result, err)
	}
}
func TestAppCaptureRequestsAreCorrelatedAndEmptyOrMalformedTargetsRefused(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	capture, asset := uuid.NewString(), uuid.NewString()
	req := protocol.CaptureAttachmentPutMessage{Cmd: protocol.CmdCaptureAttachmentPut, RequestID: protocol.Ptr("upload"), CaptureID: capture, AttachmentID: asset, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(capturePNG(t)), Final: true}
	r := testworld.Request(app, req, protocol.EventCaptureResult, func(r protocol.CaptureResultMessage) bool { return r.RequestID == "upload" })
	if !r.Success || r.Result.Upload.Attachment == nil {
		t.Fatalf("app upload %+v", r)
	}
	sent := testworld.Request(app, protocol.CaptureSendMessage{Cmd: protocol.CmdCaptureSend, RequestID: protocol.Ptr("save"), CaptureID: capture, Target: protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, AttachmentIds: []string{asset}}, protocol.EventCaptureResult, func(r protocol.CaptureResultMessage) bool { return r.RequestID == "save" })
	if !sent.Success || sent.Result.Record.ID != capture || len(sent.Result.Record.Attachments) != 1 {
		t.Fatalf("app save %+v", sent)
	}
	for _, target := range []protocol.CaptureTarget{{Kind: protocol.CaptureTargetKindChief}, {Kind: protocol.CaptureTargetKindChief, MemberID: protocol.Ptr("alder")}, {Kind: protocol.CaptureTargetKindCrew}, {Kind: protocol.CaptureTargetKind("invalid")}} {
		r := testworld.Request(app, protocol.CaptureSendMessage{Cmd: protocol.CmdCaptureSend, RequestID: protocol.Ptr("refuse"), CaptureID: uuid.NewString(), Target: target, AttachmentIds: []string{}}, protocol.EventCaptureResult, func(r protocol.CaptureResultMessage) bool { return r.RequestID == "refuse" })
		if r.Success || protocol.Deref(r.Error) == "" {
			t.Fatalf("invalid capture accepted %+v", r)
		}
	}
}
func TestUserAndPeerMessagesShareInboxWithoutChangingAuthorship(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	registerSessions(t, w, cli, "chief", "peer")
	setChiefOfStaff(app, "chief", true)
	sendCapture(t, app, protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, "user request")
	sendAgentMessage(t, cli, "peer", "chief", "peer suggestion")
	batch := readInbox(t, cli, "chief", 0)
	if len(batch.Items) != 2 || batch.Items[0].Kind != "user_message" || batch.Items[0].SenderSessionID != nil || batch.Items[1].Kind != "peer_message" || protocol.Deref(batch.Items[1].SenderSessionID) != "peer" {
		t.Fatalf("mixed authorship %+v", batch)
	}
}

func TestCaptureHistoryPagesWithoutReadingAndReportsMissingIdentity(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	var sent []string
	for _, body := range []string{"first", "second", "third"} {
		sent = append(sent, sendCapture(t, app, protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, body).CaptureID)
	}
	page, err := app.Capture(protocol.CaptureListMessage{Cmd: protocol.CmdCaptureList, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.List.Items) != 2 || page.List.Items[0].ID != sent[2] || page.List.Items[1].ID != sent[1] || page.List.NextCursor == nil {
		t.Fatalf("first page %+v", page)
	}
	sendCapture(t, app, protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, "arrived between pages")
	tail, err := app.Capture(protocol.CaptureListMessage{Cmd: protocol.CmdCaptureList, Limit: 2, Cursor: page.List.NextCursor})
	if err != nil || len(tail.List.Items) != 1 || tail.List.Items[0].ID != sent[0] || tail.List.NextCursor != nil || tail.List.Items[0].ReadAt != nil {
		t.Fatalf("tail %+v %v", tail, err)
	}
	if _, err := app.Capture(protocol.CaptureListMessage{Cmd: protocol.CmdCaptureList, Limit: 0}); err == nil {
		t.Fatal("zero page size accepted")
	}
	missing := uuid.NewString()
	if _, err := app.Capture(protocol.CaptureGetMessage{Cmd: protocol.CmdCaptureGet, CaptureID: missing}); client.ErrorCode(err) != protocol.ErrorCodeCaptureNotFound {
		t.Fatalf("missing identity %v code %q", err, client.ErrorCode(err))
	}
	result := testworld.Request(app, protocol.CaptureGetMessage{Cmd: protocol.CmdCaptureGet, RequestID: protocol.Ptr("missing"), CaptureID: missing}, protocol.EventCaptureResult, func(r protocol.CaptureResultMessage) bool { return r.RequestID == "missing" })
	if result.Success || protocol.Deref(result.ErrorCode) != protocol.ErrorCodeCaptureNotFound {
		t.Fatalf("app missing %+v", result)
	}
}

func TestCaptureAuthoringAndHistoryRefuseTheAgentSocket(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	capture, asset := uuid.NewString(), uuid.NewString()
	uploadCapture(t, app, capture, asset, capturePNG(t), true)
	send := protocol.CaptureSendMessage{Cmd: protocol.CmdCaptureSend, CaptureID: capture, Target: protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, Content: "User request", AttachmentIds: []string{asset}}
	for _, msg := range []any{
		send,
		protocol.CaptureGetMessage{Cmd: protocol.CmdCaptureGet, CaptureID: capture},
		protocol.CaptureListMessage{Cmd: protocol.CmdCaptureList, Limit: 1},
		protocol.CaptureAttachmentPutMessage{Cmd: protocol.CmdCaptureAttachmentPut, CaptureID: capture, AttachmentID: asset},
		protocol.CaptureAttachmentDiscardMessage{Cmd: protocol.CmdCaptureAttachmentDiscard, CaptureID: capture, AttachmentID: asset},
	} {
		if _, err := cli.Capture(msg); err == nil || !strings.Contains(err.Error(), "app-only") {
			t.Fatalf("agent authoring/history %T: %v", msg, err)
		}
	}
	if _, err := app.Capture(send); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Capture(protocol.CaptureAttachmentGetMessage{Cmd: protocol.CmdCaptureAttachmentGet, CaptureID: capture, AttachmentID: asset}); err != nil {
		t.Fatalf("agent retrieval refused: %v", err)
	}
	if r := captureRecord(t, app, capture); r.ReadAt != nil {
		t.Fatalf("agent mutated capture: %+v", r)
	}
}

func TestCaptureAuthoringRequiresTheTrustedAppIdentity(t *testing.T) {
	w := newWorld(t)
	trusted := w.TrustedApp()
	capture, asset := uuid.NewString(), uuid.NewString()
	uploadCapture(t, trusted, capture, asset, capturePNG(t), true)
	hostToken, err := os.ReadFile(filepath.Join(w.Dir, "browser-host-token"))
	if err != nil {
		t.Fatal(err)
	}
	hello := protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: "tauri-app", Version: "protocol-" + protocol.ProtocolVersion, ClientToken: protocol.Ptr(config.ClientToken()), Capabilities: []string{protocol.CapabilityWorkspaceSessions}}
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
	send := protocol.CaptureSendMessage{Cmd: protocol.CmdCaptureSend, CaptureID: capture, Target: protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, Content: "Only the user", AttachmentIds: []string{asset}}
	for _, peer := range peers {
		for _, msg := range []any{send,
			protocol.CaptureGetMessage{Cmd: protocol.CmdCaptureGet, CaptureID: capture},
			protocol.CaptureListMessage{Cmd: protocol.CmdCaptureList, Limit: 1},
			protocol.CaptureAttachmentPutMessage{Cmd: protocol.CmdCaptureAttachmentPut, CaptureID: capture, AttachmentID: asset},
			protocol.CaptureAttachmentDiscardMessage{Cmd: protocol.CmdCaptureAttachmentDiscard, CaptureID: capture, AttachmentID: asset},
		} {
			if _, err := peer.Capture(msg); client.ErrorCode(err) != protocol.ErrorCodeUnauthorizedClient || !strings.Contains(err.Error(), "authenticated attn app") {
				t.Fatalf("untrusted authoring %T: %v", msg, err)
			}
		}
		if _, err := peer.Capture(protocol.CaptureAttachmentGetMessage{Cmd: protocol.CmdCaptureAttachmentGet, CaptureID: capture, AttachmentID: asset}); err != nil {
			t.Fatalf("retrieval blocked: %v", err)
		}
	}
	if _, err := trusted.Capture(send); err != nil {
		t.Fatal(err)
	}
	if r := captureRecord(t, trusted, capture); r.ReadAt != nil {
		t.Fatalf("untrusted capture mutation: %+v", r)
	}
}

func TestUserCaptureWaitsForTheNextChief(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	msg := sendCapture(t, app, protocol.CaptureTarget{Kind: protocol.CaptureTargetKindChief}, "Please investigate this")
	if r := captureRecord(t, app, msg.CaptureID); r.ReadAt != nil {
		t.Fatalf("absent Chief capture is read: %+v", r)
	}
	registerSessions(t, w, cli, "chief")
	if r := setChiefOfStaff(app, "chief", true); !r.Success {
		t.Fatal(r)
	}
	if r := captureRecord(t, app, msg.CaptureID); r.ReadAt != nil {
		t.Fatal("inspection marked capture read")
	}
	items := readInbox(t, cli, "chief", 0).Items
	if len(items) != 1 || items[0].Kind != "user_message" || items[0].Content != msg.Content || items[0].SenderSessionID != nil || items[0].Address != "role:chief" {
		t.Fatalf("user attribution: %+v", items)
	}
	if r := captureRecord(t, app, msg.CaptureID); r.ReadAt == nil || *r.ReadAt != items[0].ReadAt {
		t.Fatalf("history read receipt: %+v", r)
	}
	if replay, err := app.Capture(msg); err != nil || replay.Record.ID != msg.CaptureID || replay.Record.ReadAt == nil {
		t.Fatalf("lost acknowledgement replay: %+v %v", replay, err)
	}
	changed := msg
	changed.Content = "conflicting reuse"
	if _, err := app.Capture(changed); err == nil {
		t.Fatal("conflicting identity accepted")
	}
	other := sendCapture(t, app, msg.Target, msg.Content)
	if other.CaptureID == msg.CaptureID {
		t.Fatal("intentional identical text deduplicated")
	}
}

func TestUserCaptureDeliveryWakeIsChargedOnce(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		setSetting(t, app, "crew.wake_limit", "1")
		target := protocol.CaptureTarget{Kind: protocol.CaptureTargetKindCrew, MemberID: protocol.Ptr("trellis")}
		first := sendCapture(t, app, target, "first user request")
		synctest.Wait()
		dayID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if dayID == "" {
			t.Fatal("capture did not wake the member")
		}
		second := sendCapture(t, app, target, "second user request")
		day := w.bootBubbleClaude(t, dayID)
		day.reply("Ready. <!-- attn:state=idle -->")
		if got := day.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("wake rings=%d, want one", got)
		}
		items := readInbox(t, cli, dayID, 0).Items
		if len(items) != 2 || !((items[0].ItemID == first.CaptureID && items[1].ItemID == second.CaptureID) || (items[0].ItemID == second.CaptureID && items[1].ItemID == first.CaptureID)) {
			t.Fatalf("capture inbox: %+v", items)
		}
		if _, err := cli.CrewHandoff(dayID, "finished the captures", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		third := sendCapture(t, app, target, "wait for the next manual wake")
		w.advance(15 * time.Minute)
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("capture bypassed charged wake_limit=1: %s", *binding)
		}
		if r := captureRecord(t, app, third.CaptureID); r.ReadAt != nil || r.Content != third.Content {
			t.Fatalf("wake limit lost capture: %+v", r)
		}
	})
}
