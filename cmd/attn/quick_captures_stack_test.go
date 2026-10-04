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

func quickCaptureCall(t *testing.T, cli *testworld.Peer, msg any) *protocol.QuickCaptureResult {
	t.Helper()
	r, err := cli.QuickCapture(msg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func quickCaptureSetChief(t *testing.T, app *testworld.Peer, id string) {
	t.Helper()
	r := testworld.Request(app, protocol.SetChiefOfStaffMessage{Cmd: protocol.CmdSetChiefOfStaff, SessionID: id, ChiefOfStaff: true}, protocol.EventChiefOfStaffResult, func(r protocol.ChiefOfStaffResultMessage) bool { return r.SessionID == id })
	if !r.Success {
		t.Fatal(r)
	}
}
func uploadQuickCaptureChunks(t *testing.T, cli *testworld.Peer, quickCapture, id string, data []byte) {
	t.Helper()
	const chunkBytes = 524288
	for offset := 0; offset < len(data); {
		end := min(len(data), offset+chunkBytes)
		msg := protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, CaptureID: quickCapture, AttachmentID: id, Name: "screenshot.png", Offset: offset, DataBase64: base64.StdEncoding.EncodeToString(data[offset:end]), Final: end == len(data)}
		r := quickCaptureCall(t, cli, msg)
		if r.Upload.NextOffset != end {
			t.Fatalf("offset %d, wanted %d", r.Upload.NextOffset, end)
		}
		retry := quickCaptureCall(t, cli, msg)
		if retry.Upload.NextOffset != end {
			t.Fatal("chunk replay changed receipt")
		}
		offset = end
	}
}
func TestQuickCaptureRestartAndCLIFileRetrieval(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.TrustedApp()
	register(t, s, "first", "first")
	register(t, s, "second", "second")
	quickCaptureSetChief(t, s.TrustedApp(), "first")
	data, err := os.ReadFile("../../docs/banner.png")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image fixture: docs/banner.png %d bytes; raw chunks=524288, encoded request receipt=699309, WebSocket transport=1048576", len(data))
	source := filepath.Join(t.TempDir(), "original.png")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	quickCapture, id := uuid.NewString(), uuid.NewString()
	uploadQuickCaptureChunks(t, app, quickCapture, id, data)
	msg := protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindChief}, Content: "Please inspect this screenshot", AttachmentIds: []string{id}}
	quickCaptureCall(t, app, msg)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.Start()
	app = s.TrustedApp()
	register(t, s, "first", "first")
	register(t, s, "second", "second")
	replay := quickCaptureCall(t, app, msg)
	if replay.Record.ID != quickCapture || len(replay.Record.Attachments) != 1 || replay.Record.Attachments[0].Bytes != len(data) {
		t.Fatalf("restart/lost ack %+v", replay)
	}
	quickCaptureSetChief(t, s.TrustedApp(), "second")
	old := s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: "first"})
	var empty protocol.AgentInboxBatchResult
	old.JSON(t, &empty)
	if len(empty.Items) != 0 {
		t.Fatalf("old chief read %+v", empty)
	}
	read := s.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: "second"})
	if read.Code != 0 || !strings.Contains(read.Stdout, "Message from the user, sent through Quick Capture:") || !strings.Contains(read.Stdout, msg.Content) || !strings.Contains(read.Stdout, "attn agent attachment "+quickCapture+" "+id) || strings.Contains(read.Stdout, "another agent") {
		t.Fatalf("CLI attribution %+v", read)
	}
	reread := s.Run(testworld.Invocation{Args: []string{"agent", "inbox", quickCapture, "--json"}, Session: "second"})
	var entry protocol.AgentInboxItem
	reread.JSON(t, &entry)
	if entry.Kind != "quick_capture" || entry.Content != msg.Content || len(entry.Attachments) != 1 {
		t.Fatalf("reread %+v", entry)
	}
	output := filepath.Join(t.TempDir(), "received.png")
	downloaded := s.Attn("agent", "attachment", quickCapture, id, "--out", output)
	if downloaded.Code != 0 {
		t.Fatalf("download %+v", downloaded)
	}
	saved, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("downloaded bytes differ: %v", err)
	}
	if _, format, err := image.Decode(bytes.NewReader(saved)); err != nil || format != "png" {
		t.Fatalf("agent image decode %s %v", format, err)
	}
	again := s.Attn("agent", "attachment", quickCapture, id, "--out", output)
	if again.Code == 0 {
		t.Fatal("download overwrote existing file")
	}
	s.Stop()
	s.Start()
	register(t, s, "first", "first")
	quickCaptureSetChief(t, s.TrustedApp(), "first")
	final := s.Run(testworld.Invocation{Args: []string{"agent", "inbox", "--json"}, Session: "first"})
	final.JSON(t, &empty)
	if len(empty.Items) != 0 {
		t.Fatalf("read message redelivered after restart %+v", empty)
	}
}
func TestQuickCaptureInstalledFileRecoversAfterProcessCrash(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.StartCrashingAt("quick-capture-attachment-installed")
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	im.Set(1, 1, color.NRGBA{R: 200, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	quickCapture, id := uuid.NewString(), uuid.NewString()
	msg := protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, CaptureID: quickCapture, AttachmentID: id, Name: "screenshot.png", DataBase64: base64.StdEncoding.EncodeToString(b.Bytes()), Final: true}
	if _, err := s.TrustedApp().QuickCapture(msg); err == nil {
		t.Fatal("crashed upload returned success")
	}
	s.AwaitCrash()
	s.Start()
	quickCaptureCall(t, s.TrustedApp(), protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindChief}, AttachmentIds: []string{id}})
	saved := quickCaptureCall(t, s.TrustedApp(), protocol.QuickCaptureAttachmentGetMessage{Cmd: protocol.CmdQuickCaptureAttachmentGet, CaptureID: quickCapture, AttachmentID: id})
	data, err := base64.StdEncoding.DecodeString(saved.Download.DataBase64)
	if err != nil || !bytes.Equal(data, b.Bytes()) {
		t.Fatalf("recovered image differs %v", err)
	}
}

func TestQuickCapturePDFAttachmentRoundTripsThroughCLI(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.TrustedApp()
	register(t, s, "chief", "chief")
	quickCaptureSetChief(t, app, "chief")
	data := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n")
	quickCapture, id := uuid.NewString(), uuid.NewString()
	quickCaptureCall(t, app, protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, CaptureID: quickCapture, AttachmentID: id, Name: "notes.pdf", DataBase64: base64.StdEncoding.EncodeToString(data), Final: true})
	quickCaptureCall(t, app, protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindChief}, AttachmentIds: []string{id}})
	read := s.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: "chief"})
	if read.Code != 0 || !strings.Contains(read.Stdout, "File \"notes.pdf\" (application/pdf") || !strings.Contains(read.Stdout, "Inspect the saved file with your tools.") {
		t.Fatalf("PDF inbox: %+v", read)
	}
	output := filepath.Join(t.TempDir(), "received.pdf")
	download := s.Attn("agent", "attachment", quickCapture, id, "--out", output)
	if download.Code != 0 {
		t.Fatalf("PDF download: %+v", download)
	}
	saved, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("PDF bytes changed: %v", err)
	}
}

func TestDamagedQuickCaptureDraftDoesNotBlockDaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.StartCrashingAt("quick-capture-attachment-installed")
	quickCapture, id := uuid.NewString(), uuid.NewString()
	data := []byte("%PDF-1.4\nQuick Capture recovery fixture\n%%EOF\n")
	crashApp := s.TrustedApp()
	if _, err := crashApp.QuickCapture(protocol.QuickCaptureAttachmentPutMessage{Cmd: protocol.CmdQuickCaptureAttachmentPut, CaptureID: quickCapture, AttachmentID: id, Name: "notes.pdf", DataBase64: base64.StdEncoding.EncodeToString(data), Final: true}); err == nil {
		t.Fatal("crashed upload returned success")
	}
	s.AwaitCrash()
	if err := os.Truncate(filepath.Join(s.Dir, "quick-captures", crashApp.SelectedProfile(), quickCapture, id), int64(len(data)-1)); err != nil {
		t.Fatal(err)
	}
	s.Start()
	app := s.TrustedApp()
	list := quickCaptureCall(t, app, protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 1})
	if len(list.List.DraftAssets) != 1 || list.List.DraftAssets[0].State != "staged" {
		t.Fatalf("damaged file lost its draft: %+v", list.List)
	}
	if _, err := app.QuickCapture(protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: quickCapture, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindChief}, AttachmentIds: []string{id}}); err == nil || !strings.Contains(err.Error(), "staged") {
		t.Fatalf("damaged file was accepted: %v", err)
	}
	quickCaptureCall(t, app, protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, CaptureID: quickCapture, AttachmentID: id})
	list = quickCaptureCall(t, app, protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 1})
	if len(list.List.DraftAssets) != 0 {
		t.Fatalf("discard retained the damaged draft: %+v", list.List)
	}
	quickCaptureCall(t, app, protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: uuid.NewString(), Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindChief}, Content: "A damaged draft must not block other quick captures."})
}
