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

func sendUserMessage(t *testing.T, cli *testworld.Peer, target protocol.UserMessageTarget, body string, attachments ...string) protocol.UserMessageSendMessage {
	t.Helper()
	msg := protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: uuid.NewString(), Target: target, Content: body, AttachmentIds: attachments}
	if attachments == nil {
		msg.AttachmentIds = []string{}
	}
	r, err := cli.UserMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Record.ID != msg.MessageID || r.Record.Content != body {
		t.Fatalf("saved receipt = %+v", r)
	}
	return msg
}
func userMessageRecord(t *testing.T, cli *testworld.Peer, id string) *protocol.UserMessageRecord {
	t.Helper()
	r, err := cli.UserMessage(protocol.UserMessageGetMessage{Cmd: protocol.CmdUserMessageGet, MessageID: id})
	if err != nil {
		t.Fatal(err)
	}
	return r.Record
}

func userMessagePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	im.Set(1, 1, color.NRGBA{R: 123, G: 45, B: 67, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func uploadUserMessage(t *testing.T, cli *testworld.Peer, userMessage, id string, data []byte, final bool) *protocol.UserMessageAttachmentPutResult {
	t.Helper()
	r, err := cli.UserMessage(protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(data), Final: final})
	if err != nil {
		t.Fatal(err)
	}
	return r.Upload
}
func TestUserMessageFilesRequireFinalizationPreserveBytesAndDiscardOnlyDrafts(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	userMessage, id := uuid.NewString(), uuid.NewString()
	data := userMessagePNG(t)
	source := w.Dir + "/source.png"
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	uploadUserMessage(t, app, userMessage, id, data, false)
	msg := protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, AttachmentIds: []string{id}}
	if _, err := app.UserMessage(msg); err == nil {
		t.Fatal("incomplete upload accepted")
	}
	a := uploadUserMessage(t, app, userMessage, id, data, true)
	if a.Attachment.Bytes != len(data) || a.Attachment.MediaType != "image/png" {
		t.Fatalf("metadata %+v", a)
	}
	if _, err := app.UserMessage(msg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	r, err := app.UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: userMessage, AttachmentID: id})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(r.Download.DataBase64)
	if err != nil || !bytes.Equal(decoded, data) || !r.Download.Eof {
		t.Fatalf("saved pixels changed: %v", err)
	}
	if _, err := app.UserMessage(protocol.UserMessageAttachmentDiscardMessage{Cmd: protocol.CmdUserMessageAttachmentDiscard, MessageID: userMessage, AttachmentID: id}); err == nil {
		t.Fatal("discard deleted committed asset")
	}
	changed := append([]byte(nil), data...)
	changed[0] ^= 1
	if _, err := app.UserMessage(protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(changed), Final: true}); err == nil {
		t.Fatal("conflicting replay accepted")
	}
	draft, draftID := uuid.NewString(), uuid.NewString()
	uploadUserMessage(t, app, draft, draftID, data, false)
	if _, err := app.UserMessage(protocol.UserMessageAttachmentDiscardMessage{Cmd: protocol.CmdUserMessageAttachmentDiscard, MessageID: draft, AttachmentID: draftID}); err != nil {
		t.Fatal(err)
	}
	list, err := app.UserMessage(protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 100})
	if err != nil || len(list.List.DraftAssets) != 0 {
		t.Fatalf("discard leaves drafts %+v %v", list, err)
	}
	if _, err := app.UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: draft, AttachmentID: draftID}); err == nil {
		t.Fatal("discarded asset retrieved")
	}
}

func TestConcurrentIdenticalFileUserMessagesReturnOneSavedIdentity(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	id, asset := uuid.NewString(), uuid.NewString()
	uploadUserMessage(t, app, id, asset, userMessagePNG(t), true)
	msg := protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: id, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, Content: "same request", AttachmentIds: []string{asset}}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := app.UserMessage(msg); errs <- err }()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	list := userMessageRecord(t, app, id)
	if len(list.Attachments) != 1 {
		t.Fatalf("duplicate assets %+v", list)
	}
	result, err := app.UserMessage(protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 100})
	if err != nil || len(result.List.Items) != 1 {
		t.Fatalf("duplicate messages %+v %v", result, err)
	}
}
func TestAppUserMessageRequestsAreCorrelatedAndEmptyOrMalformedTargetsRefused(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	userMessage, asset := uuid.NewString(), uuid.NewString()
	req := protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, RequestID: protocol.Ptr("upload"), MessageID: userMessage, AttachmentID: asset, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(userMessagePNG(t)), Final: true}
	r := testworld.Request(app, req, protocol.EventUserMessageResult, func(r protocol.UserMessageResultMessage) bool { return r.RequestID == "upload" })
	if !r.Success || r.Result.Upload.Attachment == nil {
		t.Fatalf("app upload %+v", r)
	}
	sent := testworld.Request(app, protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, RequestID: protocol.Ptr("save"), MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, AttachmentIds: []string{asset}}, protocol.EventUserMessageResult, func(r protocol.UserMessageResultMessage) bool { return r.RequestID == "save" })
	if !sent.Success || sent.Result.Record.ID != userMessage || len(sent.Result.Record.Attachments) != 1 {
		t.Fatalf("app save %+v", sent)
	}
	for _, target := range []protocol.UserMessageTarget{{Kind: protocol.UserMessageTargetKindChief}, {Kind: protocol.UserMessageTargetKindChief, MemberID: protocol.Ptr("alder")}, {Kind: protocol.UserMessageTargetKindCrew}, {Kind: protocol.UserMessageTargetKind("invalid")}} {
		r := testworld.Request(app, protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, RequestID: protocol.Ptr("refuse"), MessageID: uuid.NewString(), Target: target, AttachmentIds: []string{}}, protocol.EventUserMessageResult, func(r protocol.UserMessageResultMessage) bool { return r.RequestID == "refuse" })
		if r.Success || protocol.Deref(r.Error) == "" {
			t.Fatalf("invalid userMessage accepted %+v", r)
		}
	}
}
func TestUserAndPeerMessagesShareInboxWithoutChangingAuthorship(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	registerSessions(t, w, cli, "chief", "peer")
	setChiefOfStaff(app, "chief", true)
	sendUserMessage(t, app, protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, "user request")
	sendAgentMessage(t, cli, "peer", "chief", "peer suggestion")
	batch := readInbox(t, cli, "chief", 0)
	if len(batch.Items) != 2 || batch.Items[0].Kind != "user_message" || batch.Items[0].SenderSessionID != nil || batch.Items[1].Kind != "peer_message" || protocol.Deref(batch.Items[1].SenderSessionID) != "peer" {
		t.Fatalf("mixed authorship %+v", batch)
	}
}

func TestUserMessageHistoryPagesWithoutReadingAndReportsMissingIdentity(t *testing.T) {
	w := newWorld(t)
	app := w.TrustedApp()
	var sent []string
	for _, body := range []string{"first", "second", "third"} {
		sent = append(sent, sendUserMessage(t, app, protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, body).MessageID)
	}
	page, err := app.UserMessage(protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.List.Items) != 2 || page.List.Items[0].ID != sent[2] || page.List.Items[1].ID != sent[1] || page.List.NextCursor == nil {
		t.Fatalf("first page %+v", page)
	}
	sendUserMessage(t, app, protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, "arrived between pages")
	tail, err := app.UserMessage(protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 2, Cursor: page.List.NextCursor})
	if err != nil || len(tail.List.Items) != 1 || tail.List.Items[0].ID != sent[0] || tail.List.NextCursor != nil || tail.List.Items[0].ReadAt != nil {
		t.Fatalf("tail %+v %v", tail, err)
	}
	if _, err := app.UserMessage(protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 0}); err == nil {
		t.Fatal("zero page size accepted")
	}
	missing := uuid.NewString()
	if _, err := app.UserMessage(protocol.UserMessageGetMessage{Cmd: protocol.CmdUserMessageGet, MessageID: missing}); client.ErrorCode(err) != protocol.ErrorCodeUserMessageNotFound {
		t.Fatalf("missing identity %v code %q", err, client.ErrorCode(err))
	}
	result := testworld.Request(app, protocol.UserMessageGetMessage{Cmd: protocol.CmdUserMessageGet, RequestID: protocol.Ptr("missing"), MessageID: missing}, protocol.EventUserMessageResult, func(r protocol.UserMessageResultMessage) bool { return r.RequestID == "missing" })
	if result.Success || protocol.Deref(result.ErrorCode) != protocol.ErrorCodeUserMessageNotFound {
		t.Fatalf("app missing %+v", result)
	}
}

func TestUserMessageAuthoringAndHistoryRefuseTheAgentSocket(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	userMessage, asset := uuid.NewString(), uuid.NewString()
	uploadUserMessage(t, app, userMessage, asset, userMessagePNG(t), true)
	send := protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, Content: "User request", AttachmentIds: []string{asset}}
	for _, msg := range []any{
		send,
		protocol.UserMessageGetMessage{Cmd: protocol.CmdUserMessageGet, MessageID: userMessage},
		protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 1},
		protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: asset},
		protocol.UserMessageAttachmentDiscardMessage{Cmd: protocol.CmdUserMessageAttachmentDiscard, MessageID: userMessage, AttachmentID: asset},
	} {
		if _, err := cli.UserMessage(msg); err == nil || !strings.Contains(err.Error(), "app-only") {
			t.Fatalf("agent authoring/history %T: %v", msg, err)
		}
	}
	if _, err := app.UserMessage(send); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: userMessage, AttachmentID: asset}); err != nil {
		t.Fatalf("agent retrieval refused: %v", err)
	}
	if r := userMessageRecord(t, app, userMessage); r.ReadAt != nil {
		t.Fatalf("agent mutated userMessage: %+v", r)
	}
}

func TestUserMessageAuthoringRequiresTheTrustedAppIdentity(t *testing.T) {
	w := newWorld(t)
	trusted := w.TrustedApp()
	userMessage, asset := uuid.NewString(), uuid.NewString()
	uploadUserMessage(t, trusted, userMessage, asset, userMessagePNG(t), true)
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
	send := protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, Content: "Only the user", AttachmentIds: []string{asset}}
	for _, peer := range peers {
		for _, msg := range []any{send,
			protocol.UserMessageGetMessage{Cmd: protocol.CmdUserMessageGet, MessageID: userMessage},
			protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 1},
			protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: asset},
			protocol.UserMessageAttachmentDiscardMessage{Cmd: protocol.CmdUserMessageAttachmentDiscard, MessageID: userMessage, AttachmentID: asset},
		} {
			if _, err := peer.UserMessage(msg); client.ErrorCode(err) != protocol.ErrorCodeUnauthorizedClient || !strings.Contains(err.Error(), "authenticated attn app") {
				t.Fatalf("untrusted authoring %T: %v", msg, err)
			}
		}
		if _, err := peer.UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: userMessage, AttachmentID: asset}); err != nil {
			t.Fatalf("retrieval blocked: %v", err)
		}
	}
	if _, err := trusted.UserMessage(send); err != nil {
		t.Fatal(err)
	}
	if r := userMessageRecord(t, trusted, userMessage); r.ReadAt != nil {
		t.Fatalf("untrusted userMessage mutation: %+v", r)
	}
}

func TestUserMessageWaitsForTheNextChief(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.TrustedApp()
	msg := sendUserMessage(t, app, protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, "Please investigate this")
	if r := userMessageRecord(t, app, msg.MessageID); r.ReadAt != nil {
		t.Fatalf("absent Chief userMessage is read: %+v", r)
	}
	registerSessions(t, w, cli, "chief")
	if r := setChiefOfStaff(app, "chief", true); !r.Success {
		t.Fatal(r)
	}
	if r := userMessageRecord(t, app, msg.MessageID); r.ReadAt != nil {
		t.Fatal("inspection marked userMessage read")
	}
	items := readInbox(t, cli, "chief", 0).Items
	if len(items) != 1 || items[0].Kind != "user_message" || items[0].Content != msg.Content || items[0].SenderSessionID != nil || items[0].Address != "chief:"+app.SelectedProfile() {
		t.Fatalf("user attribution: %+v", items)
	}
	if r := userMessageRecord(t, app, msg.MessageID); r.ReadAt == nil || *r.ReadAt != items[0].ReadAt {
		t.Fatalf("history read receipt: %+v", r)
	}
	if replay, err := app.UserMessage(msg); err != nil || replay.Record.ID != msg.MessageID || replay.Record.ReadAt == nil {
		t.Fatalf("lost acknowledgement replay: %+v %v", replay, err)
	}
	changed := msg
	changed.Content = "conflicting reuse"
	if _, err := app.UserMessage(changed); err == nil {
		t.Fatal("conflicting identity accepted")
	}
	other := sendUserMessage(t, app, msg.Target, msg.Content)
	if other.MessageID == msg.MessageID {
		t.Fatal("intentional identical text deduplicated")
	}
}

func TestUserMessageDeliveryWakeIsChargedOnce(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		writeCrewCharter(t, w, "trellis")
		w.restart()
		app, cli := w.TrustedApp(), w.Client()
		setSetting(t, app, "crew.heartbeat_enabled", "false")
		setSetting(t, app, "crew.autosleep_enabled", "false")
		setSetting(t, app, "crew.wake_limit", "1")
		target := protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindCrew, MemberID: protocol.Ptr("trellis")}
		first := sendUserMessage(t, app, target, "first user request")
		synctest.Wait()
		dayID := protocol.Deref(crewRosterMember(t, cli, "trellis").BindingSession)
		if dayID == "" {
			t.Fatal("userMessage did not wake the member")
		}
		second := sendUserMessage(t, app, target, "second user request")
		day := w.bootBubbleClaude(t, dayID)
		day.reply("Ready. <!-- attn:state=idle -->")
		if got := day.promptsContaining(inboxDoorbell); got != 1 {
			t.Fatalf("wake rings=%d, want one", got)
		}
		items := readInbox(t, cli, dayID, 0).Items
		if len(items) != 2 || !((items[0].ItemID == first.MessageID && items[1].ItemID == second.MessageID) || (items[0].ItemID == second.MessageID && items[1].ItemID == first.MessageID)) {
			t.Fatalf("userMessage inbox: %+v", items)
		}
		if _, err := cli.CrewHandoff(dayID, "finished the user messages", false, protocol.CrewDayCloseSleep); err != nil {
			t.Fatal(err)
		}
		third := sendUserMessage(t, app, target, "wait for the next manual wake")
		w.advance(15 * time.Minute)
		if binding := crewRosterMember(t, cli, "trellis").BindingSession; binding != nil {
			t.Fatalf("userMessage bypassed charged wake_limit=1: %s", *binding)
		}
		if r := userMessageRecord(t, app, third.MessageID); r.ReadAt != nil || r.Content != third.Content {
			t.Fatalf("wake limit lost userMessage: %+v", r)
		}
	})
}

func TestUserMessagesBelongToTheirProfileAcrossInboxReadsAndRestart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		a := w.TrustedApp()
		profileA := a.SelectedProfile()
		profileB := createProfile(w.App(), "Other user messages").ID
		b := w.TrustedApp(profileB)
		cli := w.Client()
		for _, chief := range []struct {
			id, profile string
			app         *testworld.Peer
		}{{"chief-a", profileA, a}, {"chief-b", profileB, b}} {
			if err := w.InjectSession(chief.id, chief.id, w.Path(chief.id), protocol.SessionAgentClaude, chief.profile); err != nil {
				t.Fatal(err)
			}
			if r := setChiefOfStaff(chief.app, chief.id, true); !r.Success {
				t.Fatal(r)
			}
		}
		userMessage, file, draft := uuid.NewString(), uuid.NewString(), uuid.NewString()
		bytesA, bytesB := []byte("profile A file"), []byte("profile B file")
		uploadUserMessage(t, a, userMessage, file, bytesA, true)
		uploadUserMessage(t, a, draft, file, bytesA, true)
		msg := protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, Content: "A request", AttachmentIds: []string{file}}
		if _, err := a.UserMessage(msg); err != nil {
			t.Fatal(err)
		}
		testworld.Await[protocol.UserMessageChangedMessage](a, protocol.EventUserMessageChanged, func(e protocol.UserMessageChangedMessage) bool {
			return e.MessageID == userMessage && e.ProfileID == profileA
		})
		if _, err := b.UserMessage(protocol.UserMessageGetMessage{Cmd: protocol.CmdUserMessageGet, MessageID: userMessage}); client.ErrorCode(err) != protocol.ErrorCodeUserMessageNotFound {
			t.Fatalf("other-profile get: %v", err)
		}
		if _, err := b.UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: userMessage, AttachmentID: file}); err == nil {
			t.Fatal("other profile downloaded file")
		}
		if _, err := cli.WithGardenProfile(profileB, "chief-b").UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: userMessage, AttachmentID: file}); err == nil {
			t.Fatal("other-profile CLI downloaded file")
		}
		if _, _, err := cli.AgentInboxEntry(userMessage, "chief-b"); err == nil {
			t.Fatal("other Chief read A userMessage")
		}
		if got := readInbox(t, cli, "chief-b", 0); len(got.Items) != 0 {
			t.Fatalf("other Chief received A userMessage: %+v", got)
		}
		list, err := b.UserMessage(protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 1})
		if err != nil || len(list.List.Items) != 0 || len(list.List.DraftAssets) != 0 {
			t.Fatalf("other-profile history: %+v %v", list, err)
		}
		if _, err := b.UserMessage(protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 1, Cursor: &userMessage}); err == nil {
			t.Fatal("other profile used A cursor")
		}
		if _, err := b.UserMessage(protocol.UserMessageAttachmentDiscardMessage{Cmd: protocol.CmdUserMessageAttachmentDiscard, MessageID: draft, AttachmentID: file}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: draft, AttachmentID: file}); err != nil {
			t.Fatalf("other-profile discard deleted A file: %v", err)
		}
		synctest.Wait()
		for _, event := range b.Received() {
			if event.Event == protocol.EventUserMessageChanged {
				t.Fatalf("other profile got userMessage event: %+v", event)
			}
		}
		uploadUserMessage(t, b, userMessage, file, bytesB, true)
		msg.Content = "B request"
		if _, err := b.UserMessage(msg); err != nil {
			t.Fatal(err)
		}
		itemsB := readInbox(t, cli, "chief-b", 0).Items
		if len(itemsB) != 1 || itemsB[0].Content != "B request" {
			t.Fatalf("B inbox: %+v", itemsB)
		}
		if r := userMessageRecord(t, a, userMessage); r.ReadAt != nil {
			t.Fatal("B read A receipt")
		}
		itemsA := readInbox(t, cli, "chief-a", 0).Items
		if len(itemsA) != 1 || itemsA[0].Content != "A request" {
			t.Fatalf("A inbox: %+v", itemsA)
		}
		w.restart()
		for _, owner := range []struct {
			profile string
			bytes   []byte
		}{{profileA, bytesA}, {profileB, bytesB}} {
			app := w.TrustedApp(owner.profile)
			r, err := app.UserMessage(protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: userMessage, AttachmentID: file})
			if err != nil {
				t.Fatal(err)
			}
			got, err := base64.StdEncoding.DecodeString(r.Download.DataBase64)
			if err != nil || !bytes.Equal(got, owner.bytes) {
				t.Fatalf("profile %s bytes=%q, %v", owner.profile, got, err)
			}
			if userMessageRecord(t, app, userMessage).ReadAt == nil {
				t.Fatal("read receipt lost on restart")
			}
		}
		b = w.TrustedApp(profileB)
		if _, err := b.UserMessage(protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, ProfileID: &profileA, MessageID: uuid.NewString(), Target: msg.Target, Content: "queued A request", AttachmentIds: []string{}}); err == nil {
			t.Fatal("profile switch redirected queued userMessage")
		}
	})
}
