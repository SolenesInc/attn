package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func newSeedArtifactDaemon(t *testing.T) (*Daemon, string, protocol.Seed) {
	t.Helper()
	d := newGardenDaemon(t)
	root := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, root)
	t.Cleanup(d.stopNotebookWatcher)
	return d, root, plant(t, d, protocol.SeedPlantMessage{Title: "Durable files"})
}

func transferSeedArtifact(t *testing.T, d *Daemon, msg protocol.SeedArtifactTransferMessage) (*protocol.SeedArtifactTransferResult, error) {
	t.Helper()
	msg.Cmd = protocol.CmdSeedArtifactTransfer
	msg.SourceSessionID = protocol.Ptr("sess-a")
	return d.submitSeedArtifactTransfer(&msg)
}

func writeArtifactSource(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSeedArtifactCopyOwnsEveryDirectVisibleRegularFile(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	sourceRoot := filepath.Join(t.TempDir(), "worktree")
	source := writeArtifactSource(t, sourceRoot, "cover image.bin", []byte{0, 1, 2, 0xff})

	result, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source),
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if result.RelativeTarget != "cover%20image.bin" || result.DestinationPath != filepath.Join(notebook.SeedArtifactsDir(root, seed.ID), "cover image.bin") {
		t.Fatalf("copy result = %+v", result)
	}
	if got, err := os.ReadFile(result.DestinationPath); err != nil || !bytes.Equal(got, []byte{0, 1, 2, 0xff}) {
		t.Fatalf("managed bytes = %v, %v", got, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("copy removed source: %v", err)
	}

	dir := notebook.SeedArtifactsDir(root, seed.ID)
	writeArtifactSource(t, dir, "direct.pdf", []byte("pdf"))
	writeArtifactSource(t, dir, ".seed-transfer-hidden", []byte("stage"))
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "direct.pdf"), filepath.Join(dir, "linked.pdf")); err != nil {
		t.Fatal(err)
	}

	artifacts, err := d.seedArtifacts(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || artifacts[0].Filename != "cover image.bin" || artifacts[1].Filename != "direct.pdf" {
		t.Fatalf("direct membership = %+v", artifacts)
	}
	if err := os.RemoveAll(sourceRoot); err != nil {
		t.Fatal(err)
	}
	retry, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source),
	})
	if err != nil || !retry.Recovered {
		t.Fatalf("retry after source worktree deletion = %+v, %v", retry, err)
	}
	if got, err := d.seedArtifacts(seed.ID); err != nil || len(got) != 2 {
		t.Fatalf("after worktree deletion = %+v, %v", got, err)
	}
	if resp := transition(t, d, "sess-a", seed.ID, garden.VerbHarvest, "verified", ""); !resp.Ok {
		t.Fatalf("harvest: %v", protocol.Deref(resp.Error))
	}
	if got, err := d.seedArtifacts(seed.ID); err != nil || len(got) != 2 {
		t.Fatalf("harvest changed membership = %+v, %v", got, err)
	}
}

func TestSeedArtifactCopyStartsFreshAfterDetach(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	source := writeArtifactSource(t, t.TempDir(), "report.bin", []byte("first"))
	first, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source),
	})
	if err != nil {
		t.Fatal(err)
	}
	detached := filepath.Join(t.TempDir(), "report.bin")
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "detach", Filename: protocol.Ptr("report.bin"), DestinationPath: protocol.Ptr(detached),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("second"), 0o640); err != nil {
		t.Fatal(err)
	}

	second, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source),
	})
	if err != nil {
		t.Fatalf("copy changed source after detach: %v", err)
	}
	if second.Recovered {
		t.Fatalf("copy reused completed operation %+v", second)
	}
	if second.OperationID != first.OperationID {
		t.Fatalf("replacement receipt ID = %q, want %q", second.OperationID, first.OperationID)
	}
	managed := filepath.Join(notebook.SeedArtifactsDir(root, seed.ID), "report.bin")
	if got, err := os.ReadFile(managed); err != nil || string(got) != "second" {
		t.Fatalf("fresh managed artifact = %q, %v", got, err)
	}
	if got, err := os.ReadFile(detached); err != nil || string(got) != "first" {
		t.Fatalf("detached artifact = %q, %v", got, err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "second" {
		t.Fatalf("copy source = %q, %v", got, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	retry, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source),
	})
	if err != nil || !retry.Recovered {
		t.Fatalf("retry fresh copy after source deletion = %+v, %v", retry, err)
	}
}

func TestSeedArtifactMoveRefusesTrackedFilesBeforeCreatingStorage(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runArtifactGit(t, repo, "init")
	source := writeArtifactSource(t, repo, "tracked.bin", []byte("tracked"))
	runArtifactGit(t, repo, "add", "tracked.bin")

	_, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "move", SourcePath: protocol.Ptr(source),
	})
	want := "tracked.bin is tracked by Git. Make the file untracked in Git first, then run this command again. Use --copy if it should remain tracked."
	if err == nil || err.Error() != want {
		t.Fatalf("tracked refusal = %v, want %q", err, want)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("tracked source changed: %v", err)
	}
	if _, err := os.Stat(notebook.SeedArtifactsDir(root, seed.ID)); !os.IsNotExist(err) {
		t.Fatalf("tracked refusal created seed storage: %v", err)
	}
	if _, err := os.Stat(notebook.SeedArtifactTransfersDir(root)); !os.IsNotExist(err) {
		t.Fatalf("tracked refusal created transfer receipts: %v", err)
	}
}

func TestSeedArtifactMoveFindsTrackedFileInLinkedWorktree(t *testing.T) {
	d, _, seed := newSeedArtifactDaemon(t)
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	linked := filepath.Join(base, "linked")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runArtifactGit(t, repo, "init")
	writeArtifactSource(t, repo, "tracked.md", []byte("tracked"))
	runArtifactGit(t, repo, "add", "tracked.md")
	runArtifactGit(t, repo, "-c", "user.name=attn test", "-c", "user.email=attn@example.test", "commit", "-m", "fixture")
	runArtifactGit(t, repo, "worktree", "add", "-b", "artifact-test", linked)

	source := filepath.Join(linked, "tracked.md")
	_, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "move", SourcePath: protocol.Ptr(source),
	})
	if err == nil || !strings.HasPrefix(err.Error(), "tracked.md is tracked by Git.") {
		t.Fatalf("linked worktree refusal = %v", err)
	}
}

func TestSeedArtifactTransfersNeverClobber(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	sourceA := writeArtifactSource(t, filepath.Join(t.TempDir(), "a"), "same.bin", []byte("first"))
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(sourceA),
	}); err != nil {
		t.Fatal(err)
	}
	sourceB := writeArtifactSource(t, filepath.Join(t.TempDir(), "b"), "same.bin", []byte("second"))
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(sourceB),
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("copy collision = %v", err)
	}
	managed := filepath.Join(notebook.SeedArtifactsDir(root, seed.ID), "same.bin")
	if got, _ := os.ReadFile(managed); string(got) != "first" {
		t.Fatalf("copy collision replaced bytes with %q", got)
	}

	destination := writeArtifactSource(t, t.TempDir(), "same.bin", []byte("outside"))
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "detach", Filename: protocol.Ptr("same.bin"), DestinationPath: protocol.Ptr(destination),
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("detach collision = %v", err)
	}
	if got, _ := os.ReadFile(managed); string(got) != "first" {
		t.Fatalf("detach collision removed managed bytes: %q", got)
	}
	if got, _ := os.ReadFile(destination); string(got) != "outside" {
		t.Fatalf("detach collision replaced destination: %q", got)
	}
}

func TestSeedArtifactInstalledRecoveryNeverDeletesANewerSource(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	source := writeArtifactSource(t, t.TempDir(), "recover.bin", []byte("old"))
	destination := filepath.Join(notebook.SeedArtifactsDir(root, seed.ID), "recover.bin")
	if _, _, err := d.seedArtifactDir(seed.ID, true); err != nil {
		t.Fatal(err)
	}
	staged, err := stageSeedArtifact(source, filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	receipt := receiptFromStage(seed.ID, "move", source, destination, "recover.bin", staged)
	if err := installSeedArtifactStage(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Stage = ""
	receipt.State = seedTransferInstalled
	if err := writeSeedTransferReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("newer"), 0o640); err != nil {
		t.Fatal(err)
	}

	_, recovered, err := d.runSeedArtifactTransfer(root, seed.ID, "move", source, destination, "recover.bin", nil)
	if !recovered || err == nil || !strings.Contains(err.Error(), "newer source was not removed") {
		t.Fatalf("recovery = recovered %v, err %v", recovered, err)
	}
	if got, _ := os.ReadFile(source); string(got) != "newer" {
		t.Fatalf("newer source = %q", got)
	}
	if got, _ := os.ReadFile(destination); string(got) != "old" {
		t.Fatalf("installed destination = %q", got)
	}
}

func TestSeedArtifactStagedReceiptRecoversAfterInterruption(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	source := writeArtifactSource(t, t.TempDir(), "recover.bin", []byte("payload"))
	destination := filepath.Join(notebook.SeedArtifactsDir(root, seed.ID), "recover.bin")
	if _, _, err := d.seedArtifactDir(seed.ID, true); err != nil {
		t.Fatal(err)
	}
	staged, err := stageSeedArtifact(source, filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	receipt := receiptFromStage(seed.ID, "move", source, destination, "recover.bin", staged)
	if err := writeSeedTransferReceipt(root, receipt); err != nil {
		t.Fatal(err)
	}

	got, recovered, err := d.runSeedArtifactTransfer(root, seed.ID, "move", source, destination, "recover.bin", nil)
	if err != nil || !recovered || got.State != seedTransferComplete {
		t.Fatalf("recovered transfer = %+v, %v, %v", got, recovered, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source after recovered move: %v", err)
	}
	if content, _ := os.ReadFile(destination); string(content) != "payload" {
		t.Fatalf("destination = %q", content)
	}
}

func TestSeedArtifactRejectsSymlinksAndEscapes(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	source := writeArtifactSource(t, t.TempDir(), "real.bin", []byte("real"))
	link := filepath.Join(t.TempDir(), "linked.bin")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(link),
	}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink source = %v", err)
	}
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source), Filename: protocol.Ptr("../escape.bin"),
	}); err == nil || !strings.Contains(err.Error(), "not a direct visible artifact filename") {
		t.Fatalf("filename escape = %v", err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "seeds")); err != nil {
		t.Fatal(err)
	}
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source),
	}); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlink seed directory = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, seed.ID)); !os.IsNotExist(err) {
		t.Fatalf("refusal wrote through the symlink: %v", err)
	}
}

func TestSeedArtifactLegacyReferenceLeavesOnlyAfterSuccessfulBring(t *testing.T) {
	d, _, seed := newSeedArtifactDaemon(t)
	source := writeArtifactSource(t, t.TempDir(), "legacy.md", []byte("legacy"))
	legacy := markdownArtifact(source)
	if resp := artifactNote(t, d, seed.ID, garden.NoteKindAttach, "", legacy); !resp.Ok {
		t.Fatalf("attach legacy: %v", protocol.Deref(resp.Error))
	}
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(source), LegacyReference: legacy,
	}); err != nil {
		t.Fatal(err)
	}
	if references := d.seedArtifactReferences(seed.ID); len(references) != 0 {
		t.Fatalf("successful bring left references: %+v", references)
	}

	second := writeArtifactSource(t, t.TempDir(), "legacy.md", []byte("new"))
	secondLegacy := markdownArtifact(second)
	if resp := artifactNote(t, d, seed.ID, garden.NoteKindAttach, "", secondLegacy); !resp.Ok {
		t.Fatal(protocol.Deref(resp.Error))
	}
	if _, err := transferSeedArtifact(t, d, protocol.SeedArtifactTransferMessage{
		SeedID: seed.ID, Operation: "copy", SourcePath: protocol.Ptr(second), LegacyReference: secondLegacy,
	}); err == nil {
		t.Fatal("collision brought a second same-named artifact")
	}
	if references := d.seedArtifactReferences(seed.ID); len(references) != 1 || protocol.Deref(references[0].Path) != second {
		t.Fatalf("failed bring changed references: %+v", references)
	}
}

func TestSeedArtifactTargetReadsOnlyTypedDirectSafeFiles(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	dir := notebook.SeedArtifactsDir(root, seed.ID)
	if _, _, err := d.seedArtifactDir(seed.ID, true); err != nil {
		t.Fatal(err)
	}
	writeArtifactSource(t, dir, "cover art.png", []byte("png"))
	writeArtifactSource(t, dir, "active.html", []byte("<script>"))
	if err := os.Symlink(filepath.Join(dir, "cover art.png"), filepath.Join(dir, "linked.png")); err != nil {
		t.Fatal(err)
	}

	image := seedArtifactTargetEvent(t, d, protocol.SeedArtifactTargetMessage{
		Cmd: protocol.CmdSeedArtifactTarget, RequestID: "image-1", SeedID: seed.ID,
		RelativeTarget: "cover%20art.png", Purpose: "image",
	})
	if !image.Success || image.Result == nil || protocol.Deref(image.Result.MimeType) != "image/png" ||
		protocol.Deref(image.Result.DataBase64) != "cG5n" || image.Result.RelativeTarget != "cover%20art.png" {
		t.Fatalf("image target = %+v", image)
	}

	for _, target := range []string{"../cover%20art.png", "%2e%2e%2fcover.png", "linked.png"} {
		result := seedArtifactTargetEvent(t, d, protocol.SeedArtifactTargetMessage{
			Cmd: protocol.CmdSeedArtifactTarget, RequestID: target, SeedID: seed.ID,
			RelativeTarget: target, Purpose: "image",
		})
		if result.Success || result.Error == nil {
			t.Fatalf("unsafe target %q = %+v", target, result)
		}
	}
	active := seedArtifactTargetEvent(t, d, protocol.SeedArtifactTargetMessage{
		Cmd: protocol.CmdSeedArtifactTarget, RequestID: "active", SeedID: seed.ID,
		RelativeTarget: "active.html", Purpose: "link",
	})
	if active.Success || !strings.Contains(protocol.Deref(active.Error), "not a safe Markdown") {
		t.Fatalf("active link target = %+v", active)
	}
}

func TestSeedArtifactDirectFolderEditsRefreshGardenMembership(t *testing.T) {
	d, root, seed := newSeedArtifactDaemon(t)
	_, dir, err := d.seedArtifactDir(seed.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	refreshes := make(chan struct{}, 4)
	d.gardenBroadcastHook = func(_ []protocol.Seed, _ int) { refreshes <- struct{}{} }

	path := writeArtifactSource(t, dir, "direct.bin", []byte("one"))
	waitForArtifactRefresh(t, refreshes)
	if got, _ := d.seedArtifacts(seed.ID); len(got) != 1 || got[0].Filename != "direct.bin" {
		t.Fatalf("after direct add = %+v", got)
	}
	renamed := filepath.Join(dir, "renamed.bin")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	waitForArtifactRefresh(t, refreshes)
	if got, _ := d.seedArtifacts(seed.ID); len(got) != 1 || got[0].Filename != "renamed.bin" {
		t.Fatalf("after direct rename = %+v", got)
	}
	if err := os.Remove(renamed); err != nil {
		t.Fatal(err)
	}
	waitForArtifactRefresh(t, refreshes)
	if got, _ := d.seedArtifacts(seed.ID); len(got) != 0 {
		t.Fatalf("after direct delete = %+v", got)
	}
	if _, err := os.Stat(notebook.SeedArtifactsDir(root, seed.ID)); err != nil {
		t.Fatalf("direct delete removed storage: %v", err)
	}
}

func waitForArtifactRefresh(t *testing.T, refreshes <-chan struct{}) {
	t.Helper()
	select {
	case <-refreshes:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the Garden artifact refresh")
	}
}

func seedArtifactTargetEvent(t *testing.T, d *Daemon, msg protocol.SeedArtifactTargetMessage) protocol.SeedArtifactTargetResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleSeedArtifactTarget(client, &msg)
	wire := <-client.send
	var result protocol.SeedArtifactTargetResultMessage
	if err := json.Unmarshal(wire.payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func receiptFromStage(seedID, operation, source, destination, filename string, staged stagedSeedArtifact) *seedArtifactTransferReceipt {
	return &seedArtifactTransferReceipt{
		Version: seedArtifactTransferVersion,
		ID:      transferReceiptID(seedID, operation, source, destination),
		SeedID:  seedID, Operation: operation, Source: source, Destination: destination,
		Filename: filename, Hash: staged.hash, Size: staged.size, ModTimeNS: staged.modTimeNS,
		Device: staged.device, Inode: staged.inode, Stage: staged.path, State: seedTransferStaged,
	}
}

func runArtifactGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
