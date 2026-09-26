package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASeedOwnsTheDirectVisibleFilesCopiedIntoItsFolder(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	root := seedArtifactsNotebook(t, w)
	seed := plantSeedAs(t, cli, "", "Durable files")
	worktree := filepath.Join(t.TempDir(), "worktree")
	source := seedArtifactsWrite(t, worktree, "cover image.bin", []byte{0, 1, 2, 0xff})

	copied := seedArtifactsTransfer(t, cli, seed, "copy", source, "", "")
	folder := filepath.Join(root, "seeds", seed)
	if copied.RelativeTarget != "cover%20image.bin" || copied.DestinationPath != filepath.Join(folder, "cover image.bin") {
		t.Fatalf("the copy landed at %s as %s, want %s in the seed's folder", copied.DestinationPath, copied.RelativeTarget, "cover image.bin")
	}
	if got, err := os.ReadFile(copied.DestinationPath); err != nil || !bytes.Equal(got, []byte{0, 1, 2, 0xff}) {
		t.Errorf("the seed's copy holds %v (%v), want the source's bytes", got, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Errorf("the copy took the source away: %v", err)
	}

	seedArtifactsWrite(t, folder, "direct.pdf", []byte("pdf"))
	seedArtifactsWrite(t, folder, ".seed-transfer-hidden", []byte("stage"))
	if err := os.Mkdir(filepath.Join(folder, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(folder, "direct.pdf"), filepath.Join(folder, "linked.pdf")); err != nil {
		t.Fatal(err)
	}
	want := []string{"cover image.bin", "direct.pdf"}
	seedArtifactsListed(t, cli, seed, "with hidden, folder and linked entries beside them", want...)

	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	if retried := seedArtifactsTransfer(t, cli, seed, "copy", source, "", ""); !retried.Recovered {
		t.Errorf("retrying the copy after its worktree was deleted = %+v, want it recovered", retried)
	}
	seedArtifactsListed(t, cli, seed, "after the retry", want...)
	lifeMove(t, cli, "", seed, "harvest", "verified", "")
	seedArtifactsListed(t, cli, seed, "after the harvest", want...)

	report := seedArtifactsWrite(t, t.TempDir(), "report.bin", []byte("first"))
	seedArtifactsTransfer(t, cli, seed, "copy", report, "", "")
	detached := filepath.Join(t.TempDir(), "report.bin")
	seedArtifactsTransfer(t, cli, seed, "detach", "", "report.bin", detached)
	seedArtifactsWrite(t, filepath.Dir(report), "report.bin", []byte("second"))
	if fresh := seedArtifactsTransfer(t, cli, seed, "copy", report, "", ""); fresh.Recovered {
		t.Errorf("copying the changed source after a detach = %+v, want a fresh copy", fresh)
	}
	for path, body := range map[string]string{filepath.Join(folder, "report.bin"): "second", detached: "first", report: "second"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != body {
			t.Errorf("%s holds %q (%v), want %q", path, got, err, body)
		}
	}
	if err := os.Remove(report); err != nil {
		t.Fatal(err)
	}
	if retried := seedArtifactsTransfer(t, cli, seed, "copy", report, "", ""); !retried.Recovered {
		t.Errorf("retrying the fresh copy after its source was removed = %+v, want it recovered", retried)
	}
}

func TestMovingAGitTrackedFileIntoASeedIsRefusedBeforeAnythingIsStored(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	root := seedArtifactsNotebook(t, w)
	seed := plantSeedAs(t, cli, "", "Durable files")
	repo := newRepo(t, "repo")
	commitFile(t, repo, "tracked.bin", "tracked")
	linked := filepath.Join(filepath.Dir(repo), "linked")
	runGit(t, repo, "worktree", "add", "-b", "artifact-test", linked)
	staged := seedArtifactsWrite(t, repo, "staged.bin", []byte("staged"))
	runGit(t, repo, "add", "staged.bin")

	for _, source := range []string{filepath.Join(repo, "tracked.bin"), filepath.Join(linked, "tracked.bin"), staged} {
		_, err := cli.SeedArtifactTransfer("", seed, "move", source, "", "", nil)
		want := filepath.Base(source) + " is tracked by Git. Make the file untracked in Git first, then run this command again. Use --copy if it should remain tracked."
		if err == nil || !strings.HasSuffix(err.Error(), ": "+want) {
			t.Errorf("moving %s = %v, want %q", source, err, want)
		}
		if _, err := os.Stat(source); err != nil {
			t.Errorf("the refused move touched %s: %v", source, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "seeds", seed)); !os.IsNotExist(err) {
		t.Errorf("the refused moves created the seed's folder: %v", err)
	}
}

func TestSeedArtifactTransfersNeverClobberOrFollowLinks(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	root := seedArtifactsNotebook(t, w)
	seed := plantSeedAs(t, cli, "", "Durable files")
	folder := filepath.Join(root, "seeds", seed)
	real := seedArtifactsWrite(t, t.TempDir(), "real.bin", []byte("real"))

	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "seeds")); err != nil {
		t.Fatal(err)
	}
	_, err := cli.SeedArtifactTransfer("", seed, "copy", real, "", "", nil)
	lifeRefusal(t, "a copy through a linked seeds folder", err, "not a real directory")
	if _, err := os.Stat(filepath.Join(outside, seed)); !os.IsNotExist(err) {
		t.Errorf("the refused copy wrote through the link: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "seeds")); err != nil {
		t.Fatal(err)
	}

	first := seedArtifactsWrite(t, filepath.Join(t.TempDir(), "a"), "same.bin", []byte("first"))
	seedArtifactsTransfer(t, cli, seed, "copy", first, "", "")
	second := seedArtifactsWrite(t, filepath.Join(t.TempDir(), "b"), "same.bin", []byte("second"))
	occupied := seedArtifactsWrite(t, t.TempDir(), "same.bin", []byte("outside"))
	link := filepath.Join(t.TempDir(), "linked.bin")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	for _, refusal := range []struct {
		name                                     string
		operation, source, filename, destination string
		want                                     string
	}{
		{"a copy onto a name the seed holds", "copy", second, "", "", "already exists"},
		{"a detach onto an existing file", "detach", "", "same.bin", occupied, "already exists"},
		{"a copy of a link", "copy", link, "", "", "not a regular file"},
		{"a copy under an escaping name", "copy", real, "../escape.bin", "", "not a direct visible artifact filename"},
	} {
		_, err := cli.SeedArtifactTransfer("", seed, refusal.operation, refusal.source, refusal.filename, refusal.destination, nil)
		lifeRefusal(t, refusal.name, err, refusal.want)
	}
	for path, body := range map[string]string{filepath.Join(folder, "same.bin"): "first", occupied: "outside", second: "second"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != body {
			t.Errorf("after the refusals %s holds %q (%v), want %q", path, got, err, body)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(folder), "escape.bin")); !os.IsNotExist(err) {
		t.Errorf("the escaping name was written beside the seed's folder: %v", err)
	}
	seedArtifactsListed(t, cli, seed, "after the refusals", "same.bin")
}

func TestBringingALegacyAttachmentIntoASeedDropsItsReferenceOnlyOnSuccess(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	seedArtifactsNotebook(t, w)
	seed := plantSeedAs(t, cli, "", "Durable files")

	brought := seedArtifactsWrite(t, t.TempDir(), "legacy.md", []byte("legacy"))
	seedArtifactsAttach(t, cli, seed, brought)
	if _, err := cli.SeedArtifactTransfer("", seed, "copy", brought, "", "", seedArtifactsMarkdown(brought)); err != nil {
		t.Fatalf("bringing %s in: %v", brought, err)
	}
	if references := lifeShow(t, cli, seed).References; len(references) != 0 {
		t.Errorf("after the bring the seed still references %+v", references)
	}

	colliding := seedArtifactsWrite(t, t.TempDir(), "legacy.md", []byte("new"))
	seedArtifactsAttach(t, cli, seed, colliding)
	_, err := cli.SeedArtifactTransfer("", seed, "copy", colliding, "", "", seedArtifactsMarkdown(colliding))
	lifeRefusal(t, "bringing a second legacy.md in", err, "already exists")
	if references := lifeShow(t, cli, seed).References; len(references) != 1 || protocol.Deref(references[0].Path) != colliding {
		t.Errorf("after the failed bring the seed references %+v, want %s kept", references, colliding)
	}
}

func TestASeedFolderServesOnlyTypedSafeFilesToItsDocument(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	root := seedArtifactsNotebook(t, w)
	seed := plantSeedAs(t, cli, "", "Durable files")
	seedArtifactsTransfer(t, cli, seed, "copy", seedArtifactsWrite(t, t.TempDir(), "cover art.png", []byte("png")), "", "")
	folder := filepath.Join(root, "seeds", seed)
	seedArtifactsWrite(t, folder, "active.html", []byte("<script>"))
	if err := os.Symlink(filepath.Join(folder, "cover art.png"), filepath.Join(folder, "linked.png")); err != nil {
		t.Fatal(err)
	}

	image := seedArtifactsTarget(app, seed, "cover%20art.png", "image")
	if !image.Success || image.Result == nil || protocol.Deref(image.Result.MimeType) != "image/png" ||
		protocol.Deref(image.Result.DataBase64) != "cG5n" || image.Result.RelativeTarget != "cover%20art.png" {
		t.Errorf("the image target = %+v (%+v), want the PNG's bytes", image, image.Result)
	}
	for _, target := range []string{"../cover%20art.png", "%2e%2e%2fcover.png", "linked.png"} {
		if unsafe := seedArtifactsTarget(app, seed, target, "image"); unsafe.Success || protocol.Deref(unsafe.Error) == "" {
			t.Errorf("the image target %s = %+v, want it refused", target, unsafe)
		}
	}
	if active := seedArtifactsTarget(app, seed, "active.html", "link"); active.Success || !strings.Contains(protocol.Deref(active.Error), "not a safe Markdown") {
		t.Errorf("a link to active content = %+v, want it refused", active)
	}
}

func TestEditsMadeDirectlyInASeedFolderReachTheGarden(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	root := seedArtifactsNotebook(t, w)
	seed := plantSeedAs(t, cli, "", "Durable files")
	seedArtifactsTransfer(t, cli, seed, "copy", seedArtifactsWrite(t, t.TempDir(), "kept.bin", []byte("kept")), "", "")
	folder := filepath.Join(root, "seeds", seed)

	for _, edit := range []struct {
		name    string
		changed string
		apply   func() error
		want    []string
	}{
		{"an added file", "direct.bin", func() error { return os.WriteFile(filepath.Join(folder, "direct.bin"), []byte("one"), 0o644) }, []string{"direct.bin", "kept.bin"}},
		{"a renamed file", "renamed.bin", func() error {
			return os.Rename(filepath.Join(folder, "direct.bin"), filepath.Join(folder, "renamed.bin"))
		}, []string{"kept.bin", "renamed.bin"}},
		{"a deleted file", "renamed.bin", func() error { return os.Remove(filepath.Join(folder, "renamed.bin")) }, []string{"kept.bin"}},
	} {
		watcher := w.App()
		if err := edit.apply(); err != nil {
			t.Fatal(err)
		}
		testworld.Await(watcher, protocol.EventFsChanged, func(m protocol.FsChangedMessage) bool {
			return slices.ContainsFunc(m.Paths, func(p string) bool { return strings.HasSuffix(p, edit.changed) })
		})
		testworld.Await(watcher, protocol.EventGardenSeedsUpdated, func(protocol.WebSocketEvent) bool { return true })
		seedArtifactsListed(t, cli, seed, "after "+edit.name, edit.want...)
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		t.Errorf("the seed's folder did not outlive its files: %v", err)
	}
}

func TestTheDaemonStartsAndServesBesideAnUnreadableSeedFolder(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	root := seedArtifactsNotebook(t, w)
	unreadable := plantSeedAs(t, cli, "", "Unreadable durable files")
	healthy := plantSeedAs(t, cli, "", "Healthy durable files")
	seedArtifactsTransfer(t, cli, healthy, "copy", seedArtifactsWrite(t, t.TempDir(), "kept.bin", []byte("kept")), "", "")
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "seeds", unreadable)); err != nil {
		t.Fatal(err)
	}

	w.restart()
	if seeds := w.App().Initial.Seeds; len(seeds) != 2 {
		t.Errorf("after the restart the app sees %d seeds, want both", len(seeds))
	}
	seedArtifactsListed(t, w.Client(), healthy, "after the restart", "kept.bin")
}

func seedArtifactsWrite(t *testing.T, dir, name string, content []byte) string {
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

func seedArtifactsTransfer(t *testing.T, cli *client.Client, seedID, operation, source, filename, destination string) *protocol.SeedArtifactTransferResult {
	t.Helper()
	result, err := cli.SeedArtifactTransfer("", seedID, operation, source, filename, destination, nil)
	if err != nil {
		t.Fatalf("%s %s%s into %s: %v", operation, source, filename, seedID, err)
	}
	return result
}

func seedArtifactsListed(t *testing.T, cli *client.Client, seedID, when string, want ...string) {
	t.Helper()
	var got []string
	for _, artifact := range lifeShow(t, cli, seedID).Artifacts {
		got = append(got, artifact.Filename)
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s the seed lists artifacts %q, want %q", when, got, want)
	}
}

func seedArtifactsMarkdown(path string) *protocol.SeedArtifactReference {
	return &protocol.SeedArtifactReference{Kind: "markdown_file", Path: protocol.Ptr(path)}
}

func seedArtifactsAttach(t *testing.T, cli *client.Client, seedID, path string) {
	t.Helper()
	if _, err := cli.SeedNote("", seedID, "", "", "attach", false, seedArtifactsMarkdown(path)); err != nil {
		t.Fatalf("attach %s to %s: %v", path, seedID, err)
	}
	if references := lifeShow(t, cli, seedID).References; !slices.ContainsFunc(references, func(r protocol.SeedArtifactReference) bool {
		return protocol.Deref(r.Path) == path
	}) {
		t.Fatalf("after attaching %s the seed references %+v", path, references)
	}
}

func seedArtifactsTarget(app *testworld.Peer, seedID, target, purpose string) protocol.SeedArtifactTargetResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.SeedArtifactTargetMessage{
		Cmd: protocol.CmdSeedArtifactTarget, RequestID: requestID, SeedID: seedID, RelativeTarget: target, Purpose: purpose,
	}, protocol.EventSeedArtifactTargetResult, func(r protocol.SeedArtifactTargetResultMessage) bool { return r.RequestID == requestID })
}

func seedArtifactsNotebook(t *testing.T, w *world) string {
	t.Helper()
	root := filepath.Join(w.Dir, "notebook")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}
