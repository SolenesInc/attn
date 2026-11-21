package main_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	oldHomeProfile    = "profile-2b52360d-c505-4375-b188-bcfb403d3123"
	oldHomeCapture    = "5f1f3c9a-2b55-4c1e-9d4e-2a7b8c3d0e11"
	oldHomeCommitted  = "8a6c2f40-7d1b-4e8a-b5c3-9f0e1d2c3b4a"
	oldHomeDraft      = "c3d4e5f6-0718-4293-a4b5-c6d7e8f90a1b"
	oldHomeUploading  = "e1f2a3b4-c5d6-4e7f-8091-a2b3c4d5e6f7"
	oldHomeUploadedTo = 1900

	buildBeforeAttachmentOwners = "c03b41866edb21dc8bce0254f031242752667db3"
)

func oldHomeCommittedBytes() []byte {
	return bytes.Repeat([]byte("committed quick capture file\n"), 100)
}

func oldHomeUploadBytes() []byte {
	return bytes.Repeat([]byte("upload in progress\n"), 200)
}

func restoreHomeBeforeAttachmentOwners(t *testing.T, dir string) {
	t.Helper()
	fixture := filepath.Join("testdata", "home-before-attachment-owners")
	compressed, err := os.Open(filepath.Join(fixture, "attn.db.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	db, err := gzip.NewReader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(filepath.Join(dir, "attn.db"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, db); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(dir, "quick-captures"), os.DirFS(filepath.Join(fixture, "quick-captures"))); err != nil {
		t.Fatal(err)
	}
}

func moveCommittedFileAsAnInterruptedStartWould(t *testing.T, dir string) {
	t.Helper()
	to := filepath.Join(dir, "attachments", oldHomeProfile, oldHomeCommitted)
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "quick-captures", oldHomeProfile, oldHomeCapture, oldHomeCommitted), to); err != nil {
		t.Fatal(err)
	}
}

func TestAHomeWrittenBeforeAttachmentOwnersKeepsItsFilesAndResumesItsUpload(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	restoreHomeBeforeAttachmentOwners(t, s.Dir)

	moveCommittedFileAsAnInterruptedStartWould(t, s.Dir)
	s.Start()

	if _, err := os.Stat(filepath.Join(s.Dir, "quick-captures", oldHomeProfile, oldHomeCapture, oldHomeCommitted)); err != nil {
		t.Fatalf("the file the interrupted start moved is not back where the build before attachment owners reads it: %v", err)
	}
	app := s.TrustedApp()
	if app.SelectedProfile() != oldHomeProfile {
		t.Fatalf("the app selected %s, want the old home's profile %s", app.SelectedProfile(), oldHomeProfile)
	}
	list := quickCaptureCall(t, app, protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 10})
	if len(list.List.Items) != 1 || len(list.List.Items[0].Attachments) != 1 || list.List.Items[0].Attachments[0].ID != oldHomeCommitted {
		t.Fatalf("the saved capture after the upgrade: %+v", list.List.Items)
	}
	if len(list.List.DraftAssets) != 1 || list.List.DraftAssets[0].AttachmentID != oldHomeUploading || list.List.DraftAssets[0].CaptureID != oldHomeDraft || list.List.DraftAssets[0].NextOffset != oldHomeUploadedTo {
		t.Fatalf("the upload in progress after the upgrade: %+v", list.List.DraftAssets)
	}

	upload := oldHomeUploadBytes()
	done := attachmentCall(t, app, protocol.AttachmentPutMessage{Cmd: protocol.CmdAttachmentPut, Owner: ownedBy(oldHomeDraft), AttachmentID: oldHomeUploading, Name: "draft.txt", Offset: oldHomeUploadedTo, DataBase64: base64.StdEncoding.EncodeToString(upload[oldHomeUploadedTo:]), Final: true})
	if done.Upload.Attachment == nil || done.Upload.Attachment.Bytes != len(upload) {
		t.Fatalf("the resumed upload finished as %+v", done.Upload)
	}
	quickCaptureCall(t, app, protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: oldHomeDraft, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: protocol.Ptr("chief")}, Content: "Finished after the upgrade", AttachmentIds: []string{oldHomeUploading}})

	for _, c := range []struct {
		args []string
		want []byte
	}{
		{[]string{oldHomeCommitted}, oldHomeCommittedBytes()},
		{[]string{oldHomeCapture, oldHomeCommitted}, oldHomeCommittedBytes()},
		{[]string{oldHomeUploading}, upload},
		{[]string{oldHomeDraft, oldHomeUploading}, upload},
	} {
		out := filepath.Join(t.TempDir(), "saved")
		got := s.Attn(append(append([]string{"agent", "attachment"}, c.args...), "--out", out)...)
		if got.Code != 0 {
			t.Fatalf("attn agent attachment %v: %+v", c.args, got)
		}
		saved, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(saved, c.want) {
			t.Fatalf("attn agent attachment %v saved %d bytes, want the original %d: %v", c.args, len(saved), len(c.want), err)
		}
	}
}

func TestTheBuildBeforeAttachmentOwnersReadsQuickCaptureFilesAfterADatabaseRestore(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	restoreHomeBeforeAttachmentOwners(t, s.Dir)
	previous, current := testworld.AttnBinaryFromCommit(t, buildBeforeAttachmentOwners), testworld.AttnBinary(t)
	savedBytes := func(label string, args ...string) {
		t.Helper()
		out := filepath.Join(t.TempDir(), "saved")
		got := s.Attn(append(append([]string{"agent", "attachment"}, args...), "--out", out)...)
		saved, err := os.ReadFile(out)
		if got.Code != 0 || err != nil || !bytes.Equal(saved, oldHomeCommittedBytes()) {
			t.Fatalf("%s: attn agent attachment %v saved %d bytes, want the original %d: %+v %v", label, args, len(saved), len(oldHomeCommittedBytes()), got, err)
		}
	}
	premigrationBackups := func() []string {
		t.Helper()
		found, err := filepath.Glob(filepath.Join(s.Dir, "backups", "attn-premigration-*.db"))
		if err != nil {
			t.Fatal(err)
		}
		return found
	}

	s.RunFrom(previous)
	s.StartRelease()
	savedBytes("the previous build", oldHomeCapture, oldHomeCommitted)
	s.Stop()
	before := premigrationBackups()

	s.RunFrom(current)
	s.Start()
	savedBytes("the upgrade", oldHomeCommitted)
	s.Stop()

	upgrade := slices.DeleteFunc(premigrationBackups(), func(path string) bool { return slices.Contains(before, path) })
	if len(upgrade) != 1 {
		t.Fatalf("the upgrade wrote pre-migration backups %v, want one", upgrade)
	}
	if restored := s.Attn("db", "restore", upgrade[0]); restored.Code != 0 {
		t.Fatalf("attn db restore %s: %+v", upgrade[0], restored)
	}
	s.RunFrom(previous)
	s.StartRelease()
	savedBytes("the previous build on the restored database", oldHomeCapture, oldHomeCommitted)
	s.Stop()

	s.RunFrom(current)
	s.Start()
	savedBytes("the second upgrade", oldHomeCommitted)
	savedBytes("the second upgrade", oldHomeCapture, oldHomeCommitted)
}
