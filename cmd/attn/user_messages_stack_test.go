package main_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func userMessageCall(t *testing.T, cli *testworld.Peer, msg any) *protocol.UserMessageResult {
	t.Helper()
	r, err := cli.UserMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func userMessageSetChief(t *testing.T, app *testworld.Peer, id string) {
	t.Helper()
	r := testworld.Request(app, protocol.SetChiefOfStaffMessage{Cmd: protocol.CmdSetChiefOfStaff, SessionID: id, ChiefOfStaff: true}, protocol.EventChiefOfStaffResult, func(r protocol.ChiefOfStaffResultMessage) bool { return r.SessionID == id })
	if !r.Success {
		t.Fatal(r)
	}
}
func uploadUserMessageChunks(t *testing.T, cli *testworld.Peer, userMessage, id string, data []byte) {
	t.Helper()
	const chunkBytes = 524288
	for offset := 0; offset < len(data); {
		end := min(len(data), offset+chunkBytes)
		msg := protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: id, Name: "screenshot.png", Offset: offset, DataBase64: base64.StdEncoding.EncodeToString(data[offset:end]), Final: end == len(data)}
		r := userMessageCall(t, cli, msg)
		if r.Upload.NextOffset != end {
			t.Fatalf("offset %d, wanted %d", r.Upload.NextOffset, end)
		}
		retry := userMessageCall(t, cli, msg)
		if retry.Upload.NextOffset != end {
			t.Fatal("chunk replay changed receipt")
		}
		offset = end
	}
}
func TestUserMessageRestartAndCLIFileRetrieval(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.TrustedApp()
	register(t, s, "first", "first")
	register(t, s, "second", "second")
	userMessageSetChief(t, s.TrustedApp(), "first")
	data, err := os.ReadFile("../../docs/banner.png")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image fixture: docs/banner.png %d bytes; raw chunks=524288, encoded request receipt=699309, WebSocket transport=1048576", len(data))
	source := filepath.Join(t.TempDir(), "original.png")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	userMessage, id := uuid.NewString(), uuid.NewString()
	uploadUserMessageChunks(t, app, userMessage, id, data)
	msg := protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, Content: "Please inspect this screenshot", AttachmentIds: []string{id}}
	userMessageCall(t, app, msg)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.Start()
	app = s.TrustedApp()
	register(t, s, "first", "first")
	register(t, s, "second", "second")
	replay := userMessageCall(t, app, msg)
	if replay.Record.ID != userMessage || len(replay.Record.Attachments) != 1 || replay.Record.Attachments[0].Bytes != len(data) {
		t.Fatalf("restart/lost ack %+v", replay)
	}
	userMessageSetChief(t, s.TrustedApp(), "second")
	old := s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: "first"})
	var empty protocol.AgentInboxBatchResult
	old.JSON(t, &empty)
	if len(empty.Items) != 0 {
		t.Fatalf("old chief read %+v", empty)
	}
	read := s.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: "second"})
	if read.Code != 0 || !strings.Contains(read.Stdout, "Message from the user, sent through Quick Capture:") || !strings.Contains(read.Stdout, msg.Content) || !strings.Contains(read.Stdout, "attn agent attachment "+userMessage+" "+id) || strings.Contains(read.Stdout, "another agent") {
		t.Fatalf("CLI attribution %+v", read)
	}
	reread := s.Run(testworld.Invocation{Args: []string{"agent", "inbox", userMessage, "--json"}, Session: "second"})
	var entry protocol.AgentInboxItem
	reread.JSON(t, &entry)
	if entry.Kind != "user_message" || entry.Content != msg.Content || len(entry.Attachments) != 1 {
		t.Fatalf("reread %+v", entry)
	}
	output := filepath.Join(t.TempDir(), "received.png")
	downloaded := s.Attn("agent", "attachment", userMessage, id, "--out", output)
	if downloaded.Code != 0 {
		t.Fatalf("download %+v", downloaded)
	}
	saved, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("downloaded bytes differ: %v", err)
	}
	if _, format, err := image.Decode(bytes.NewReader(saved)); err != nil || format != "png" {
		t.Fatalf("recipient image decode %s %v", format, err)
	}
	again := s.Attn("agent", "attachment", userMessage, id, "--out", output)
	if again.Code == 0 {
		t.Fatal("download overwrote existing file")
	}
	s.Stop()
	s.Start()
	register(t, s, "first", "first")
	userMessageSetChief(t, s.TrustedApp(), "first")
	final := s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: "first"})
	final.JSON(t, &empty)
	if len(empty.Items) != 0 {
		t.Fatalf("read message redelivered after restart %+v", empty)
	}
}
func TestUserMessageInstalledFileRecoversAfterProcessCrash(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.StartCrashingAt("user-message-attachment-installed")
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	im.Set(1, 1, color.NRGBA{R: 200, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	userMessage, id := uuid.NewString(), uuid.NewString()
	msg := protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(b.Bytes()), Final: true}
	if _, err := s.TrustedApp().UserMessage(msg); err == nil {
		t.Fatal("crashed upload returned success")
	}
	s.AwaitCrash()
	s.Start()
	userMessageCall(t, s.TrustedApp(), protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, AttachmentIds: []string{id}})
	saved := userMessageCall(t, s.TrustedApp(), protocol.UserMessageAttachmentGetMessage{Cmd: protocol.CmdUserMessageAttachmentGet, MessageID: userMessage, AttachmentID: id})
	data, err := base64.StdEncoding.DecodeString(saved.Download.DataBase64)
	if err != nil || !bytes.Equal(data, b.Bytes()) {
		t.Fatalf("recovered image differs %v", err)
	}
}

func TestUserMessagePDFAttachmentRoundTripsThroughCLI(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.TrustedApp()
	register(t, s, "chief", "chief")
	userMessageSetChief(t, app, "chief")
	data := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n")
	userMessage, id := uuid.NewString(), uuid.NewString()
	userMessageCall(t, app, protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: id, Name: "notes.pdf", DataBase64: base64.StdEncoding.EncodeToString(data), Final: true})
	userMessageCall(t, app, protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, AttachmentIds: []string{id}})
	read := s.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: "chief"})
	if read.Code != 0 || !strings.Contains(read.Stdout, "File \"notes.pdf\" (application/pdf") || !strings.Contains(read.Stdout, "Inspect the saved file with your tools.") {
		t.Fatalf("PDF inbox: %+v", read)
	}
	output := filepath.Join(t.TempDir(), "received.pdf")
	download := s.Attn("agent", "attachment", userMessage, id, "--out", output)
	if download.Code != 0 {
		t.Fatalf("PDF download: %+v", download)
	}
	saved, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("PDF bytes changed: %v", err)
	}
}

func TestDamagedUserMessageDraftDoesNotBlockDaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.StartCrashingAt("user-message-attachment-installed")
	userMessage, id := uuid.NewString(), uuid.NewString()
	data := []byte("%PDF-1.4\nQuick Capture recovery fixture\n%%EOF\n")
	crashApp := s.TrustedApp()
	if _, err := crashApp.UserMessage(protocol.UserMessageAttachmentPutMessage{Cmd: protocol.CmdUserMessageAttachmentPut, MessageID: userMessage, AttachmentID: id, Name: "notes.pdf", DataBase64: base64.StdEncoding.EncodeToString(data), Final: true}); err == nil {
		t.Fatal("crashed upload returned success")
	}
	s.AwaitCrash()
	if err := os.Truncate(filepath.Join(s.Dir, "user-messages", crashApp.SelectedProfile(), userMessage, id), int64(len(data)-1)); err != nil {
		t.Fatal(err)
	}
	s.Start()
	app := s.TrustedApp()
	list := userMessageCall(t, app, protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 1})
	if len(list.List.DraftAssets) != 1 || list.List.DraftAssets[0].State != "staged" {
		t.Fatalf("damaged file lost its draft: %+v", list.List)
	}
	if _, err := app.UserMessage(protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: userMessage, Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, AttachmentIds: []string{id}}); err == nil || !strings.Contains(err.Error(), "staged") {
		t.Fatalf("damaged file was accepted: %v", err)
	}
	userMessageCall(t, app, protocol.UserMessageAttachmentDiscardMessage{Cmd: protocol.CmdUserMessageAttachmentDiscard, MessageID: userMessage, AttachmentID: id})
	list = userMessageCall(t, app, protocol.UserMessageListMessage{Cmd: protocol.CmdUserMessageList, Limit: 1})
	if len(list.List.DraftAssets) != 0 {
		t.Fatalf("discard retained the damaged draft: %+v", list.List)
	}
	userMessageCall(t, app, protocol.UserMessageSendMessage{Cmd: protocol.CmdUserMessageSend, MessageID: uuid.NewString(), Target: protocol.UserMessageTarget{Kind: protocol.UserMessageTargetKindChief}, Content: "A damaged draft must not block other user messages."})
}
