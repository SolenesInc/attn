package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/protocol"
)

// Boundary-bound, the whole paced set here: the real hub's loop has no exit path
// and the fsnotify watcher parks in kqueue — neither can end a synctest bubble.

func newFsDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	return d
}

func trustedFsClient(bufSize int) *wsClient {
	client := &wsClient{send: make(chan outboundMessage, bufSize), trustedTauriOrigin: true}
	client.setBrowserHostAuthenticated(true)
	client.setIdentity("tauri-app", "test", nil)
	return client
}

func fsWriteCAS(t *testing.T, d *Daemon, path, content, baseHash string) protocol.FsWriteResultMessage {
	t.Helper()
	return fsWriteCASRoot(t, d, path, content, baseHash, "")
}

func fsWriteCASRoot(t *testing.T, d *Daemon, path, content, baseHash, root string) protocol.FsWriteResultMessage {
	t.Helper()
	client := trustedFsClient(8)
	d.sendFsWriteWSResult(client, "setup-fs-write", path, content, baseHash, root)
	var res protocol.FsWriteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func listFs(t *testing.T, d *Daemon, dir string) []protocol.FsEntry {
	t.Helper()
	return listFsRoot(t, d, dir, "")
}

func listFsRoot(t *testing.T, d *Daemon, dir, root string) []protocol.FsEntry {
	t.Helper()
	client := trustedFsClient(8)
	d.sendFsListWSResult(client, "setup-fs-list", dir, root)
	var res protocol.FsListResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success {
		t.Fatalf("fs list(%q) failed: %v", dir, res.Error)
	}
	return res.Entries
}

func fsExists(t *testing.T, d *Daemon, path string) protocol.FsExistsResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 8)}
	d.sendFsExistsWSResult(client, "setup-fs-exists", path, "")
	var res protocol.FsExistsResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func waitForFsChange(t *testing.T, ch chan outboundMessage, origin string) []string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			var ev protocol.FsChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err != nil {
				continue
			}
			if ev.Event == protocol.EventFsChanged && ev.Origin == origin {
				return ev.Paths
			}
		case <-deadline:
			t.Fatalf("no fs_changed with origin %q was broadcast", origin)
			return nil
		}
	}
}

func TestFsWriteListReadWSResults(t *testing.T) {
	d := newFsDaemon(t)

	create := fsWriteCAS(t, d, "notes/todo.txt", "buy milk", "")
	if create.Event != protocol.EventFsWriteResult || !create.Success ||
		create.Result == nil || create.Result.Conflict || create.Result.Hash == nil {
		t.Fatalf("create result = %+v", create.Result)
	}
	hash := *create.Result.Hash

	rootEntries := listFs(t, d, "")
	if len(rootEntries) != 1 || rootEntries[0].Name != "notes" || !rootEntries[0].IsDir {
		t.Fatalf("root list = %+v, want a single notes/ directory", rootEntries)
	}

	sub := listFs(t, d, "notes")
	if len(sub) != 1 || sub[0].Path != "notes/todo.txt" || sub[0].IsDir ||
		sub[0].Size != int(len("buy milk")) || sub[0].Modified == nil {
		t.Fatalf("notes list = %+v", sub)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsReadWSResult(client, "r1", "notes/todo.txt", "")
	var read protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.Event != protocol.EventFsReadResult || read.RequestID != "r1" || !read.Success ||
		read.Result == nil || read.Result.Content != "buy milk" || read.Result.Hash != hash {
		t.Fatalf("read result = %+v", read.Result)
	}

	d.sendFsReadWSResult(client, "r2", "nope.txt", "")
	var missing protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &missing)
	if missing.RequestID != "r2" || missing.Success || missing.Error == nil {
		t.Fatalf("missing read result = %+v, want failure with error", missing)
	}
}

func TestFsReadWSRejectsOversizedFile(t *testing.T) {
	d := newFsDaemon(t)
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "attachments", "too-large.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), fsdoc.MaxFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsReadWSResult(client, "oversized", "attachments/too-large.txt", "")
	var read protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.Success || read.Error == nil || !strings.Contains(*read.Error, "read cap") {
		t.Fatalf("oversized read = %+v, want read-cap error", read)
	}
}

func TestFsExistsWSResults(t *testing.T) {
	d := newFsDaemon(t)
	if c := fsWriteCAS(t, d, "knowledge/areas/foo.md", "x", ""); !c.Success || c.Result == nil {
		t.Fatalf("seed write = %+v", c.Result)
	}

	present := fsExists(t, d, "/knowledge/areas/foo.md")
	if present.Event != protocol.EventFsExistsResult || present.RequestID != "setup-fs-exists" ||
		!present.Success || present.Result == nil || !present.Result.Exists {
		t.Fatalf("exists(present) = %+v", present)
	}

	absent := fsExists(t, d, "/knowledge/areas/missing.md")
	if !absent.Success || absent.Result == nil || absent.Result.Exists {
		t.Fatalf("exists(absent) = %+v, want a successful exists=false", absent)
	}

	bad := fsExists(t, d, ".secret")
	if bad.Success || bad.Error == nil {
		t.Fatalf("exists(dotfile) = %+v, want a failed result with error", bad)
	}
}

func TestFsWriteWSResultSaveAndConflict(t *testing.T) {
	d := newFsDaemon(t)

	create := fsWriteCAS(t, d, "a.txt", "v1", "")
	if create.Result == nil || create.Result.Hash == nil {
		t.Fatalf("create = %+v", create.Result)
	}
	h1 := *create.Result.Hash

	stale := fsWriteCAS(t, d, "a.txt", "v2", "deadbeef")
	if !stale.Success || stale.Result == nil || !stale.Result.Conflict ||
		stale.Result.CurrentHash == nil || *stale.Result.CurrentHash != h1 {
		t.Fatalf("stale write = %+v, want conflict with current hash %q", stale.Result, h1)
	}

	ok := fsWriteCAS(t, d, "a.txt", "v2", h1)
	if !ok.Success || ok.Result == nil || ok.Result.Conflict || ok.Result.Hash == nil {
		t.Fatalf("CAS edit = %+v", ok.Result)
	}
}

func TestFsDispatchThroughClientMessage(t *testing.T) {
	d := newFsDaemon(t)
	client := newWorkspaceProtocolTestClient()
	client.setIdentity("test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"fs_write","request_id":"w1","path":"docs/readme.md","content":"# hi\n"}`))
	var write protocol.FsWriteResultMessage
	readNotebookWSEvent(t, client.send, &write)
	if write.Event != protocol.EventFsWriteResult || write.RequestID != "w1" || !write.Success ||
		write.Result == nil || write.Result.Conflict || write.Result.Hash == nil {
		t.Fatalf("write dispatch = %+v", write)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_list","request_id":"l1","path":"docs"}`))
	var list protocol.FsListResultMessage
	readNotebookWSEvent(t, client.send, &list)
	if list.RequestID != "l1" || !list.Success || len(list.Entries) != 1 ||
		list.Entries[0].Path != "docs/readme.md" {
		t.Fatalf("list dispatch = %+v", list)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_read","request_id":"r1","path":"docs/readme.md"}`))
	var read protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if read.RequestID != "r1" || !read.Success || read.Result == nil || read.Result.Content != "# hi\n" {
		t.Fatalf("read dispatch = %+v", read.Result)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_exists","request_id":"e1","path":"docs/readme.md"}`))
	var exists protocol.FsExistsResultMessage
	readNotebookWSEvent(t, client.send, &exists)
	if exists.RequestID != "e1" || !exists.Success || exists.Result == nil ||
		exists.Result.Path != "docs/readme.md" || !exists.Result.Exists {
		t.Fatalf("exists dispatch = %+v", exists)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_rename","request_id":"rn1","path":"docs/readme.md","new_path":"docs/plan.md"}`))
	var renamed protocol.FsRenameResultMessage
	readNotebookWSEvent(t, client.send, &renamed)
	if renamed.RequestID != "rn1" || !renamed.Success || renamed.Result == nil || renamed.Result.NewPath != "docs/plan.md" {
		t.Fatalf("rename dispatch = %+v", renamed)
	}

	d.handleClientMessage(client, []byte(`{"cmd":"fs_delete","request_id":"d1","path":"docs/plan.md"}`))
	var deleted protocol.FsDeleteResultMessage
	readNotebookWSEvent(t, client.send, &deleted)
	if deleted.RequestID != "d1" || !deleted.Success || deleted.Result == nil || deleted.Result.Path != "docs/plan.md" {
		t.Fatalf("delete dispatch = %+v", deleted)
	}
}

func TestFsWriteBroadcastsUiChange(t *testing.T) {
	d := newFsDaemon(t)
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	writer := &wsClient{send: make(chan outboundMessage, 8)}
	d.sendFsWriteWSResult(writer, "w1", "notes/todo.txt", "hello", "", "")
	var res protocol.FsWriteResultMessage
	readNotebookWSEvent(t, writer.send, &res)
	if !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("write = %+v", res.Result)
	}

	got := waitForFsChange(t, hubClient.send, originUI)
	if !slices.Contains(got, "notes/todo.txt") {
		t.Fatalf("ui fs_changed = %v, want notes/todo.txt", got)
	}
}

func TestFsWriteNormalizesEchoedPath(t *testing.T) {
	d := newFsDaemon(t)
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	res := fsWriteCAS(t, d, "/notes/todo.md", "x", "")
	if res.Result == nil || res.Result.Path != "notes/todo.md" {
		t.Fatalf("write result path = %+v, want normalized notes/todo.md", res.Result)
	}
	got := waitForFsChange(t, hubClient.send, originUI)
	if !slices.Contains(got, "notes/todo.md") || slices.Contains(got, "/notes/todo.md") {
		t.Fatalf("ui fs_changed = %v, want normalized notes/todo.md", got)
	}
}

func TestFsChangedExternalEditNotSelfWrite(t *testing.T) {
	d := newFsDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	client := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[client] = true
	go d.wsHub.run()

	listFs(t, d, "")
	time.Sleep(80 * time.Millisecond)

	if res := fsWriteCAS(t, d, "own.md", "attn wrote this", ""); !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("own write = %+v", res.Result)
	}
	if err := os.WriteFile(filepath.Join(root, "ext.md"), []byte("edited externally"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := waitForFsChange(t, client.send, originExternal)
	if !slices.Contains(ext, "ext.md") {
		t.Fatalf("external fs_changed %v missing ext.md", ext)
	}
	if slices.Contains(ext, "own.md") {
		t.Fatalf("external fs_changed %v wrongly included attn's own write own.md", ext)
	}
}

var pngHeaderBytes = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}

func fsReadAsset(t *testing.T, d *Daemon, requestID, path string) protocol.FsReadAssetResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsReadAssetWSResult(client, requestID, path, "")
	var res protocol.FsReadAssetResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func TestFsReadAssetWSResult(t *testing.T) {
	d := newFsDaemon(t)
	if res := fsWriteCAS(t, d, "assets/pic.png", string(pngHeaderBytes), ""); !res.Success || res.Result == nil {
		t.Fatalf("seed write = %+v", res.Result)
	}

	got := fsReadAsset(t, d, "a1", "assets/pic.png")
	if got.Event != protocol.EventFsReadAssetResult || got.RequestID != "a1" || !got.Success || got.Result == nil {
		t.Fatalf("read asset = %+v", got)
	}
	if got.Result.MimeType != "image/png" {
		t.Fatalf("mime type = %q, want image/png", got.Result.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Result.DataBase64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !bytes.Equal(decoded, pngHeaderBytes) {
		t.Fatalf("decoded bytes = %v, want %v", decoded, pngHeaderBytes)
	}
}

func TestFsReadAssetPathEscape(t *testing.T) {
	d := newFsDaemon(t)
	got := fsReadAsset(t, d, "a1", "../outside.png")
	if got.Success {
		t.Fatalf("read asset(path escape) = %+v, want failure", got)
	}
}

func TestFsReadAssetMissingFile(t *testing.T) {
	d := newFsDaemon(t)
	got := fsReadAsset(t, d, "a1", "assets/nope.png")
	if got.Success || got.Error == nil {
		t.Fatalf("read asset(missing) = %+v, want failure with error", got)
	}
}

func TestFsReadAssetOversize(t *testing.T) {
	d := newFsDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{0xFF}, maxAssetBytes+1)
	if err := os.WriteFile(filepath.Join(root, "assets", "huge.png"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	got := fsReadAsset(t, d, "a1", "assets/huge.png")
	if got.Success || got.Error == nil || !strings.Contains(*got.Error, "cap") {
		t.Fatalf("read asset(oversize) = %+v, want failure mentioning the cap", got)
	}
}

// Fails if maxAssetBytes is raised without re-deriving the cap, or if the base64
// envelope math is wrong.
func TestFsReadAssetMaxSizeFitsMessageCap(t *testing.T) {
	d := newFsDaemon(t)
	root := d.store.GetSetting(SettingNotebookRoot)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	max := bytes.Repeat([]byte{0xFF}, maxAssetBytes)
	if err := os.WriteFile(filepath.Join(root, "assets", "max.png"), max, 0o644); err != nil {
		t.Fatal(err)
	}

	got := fsReadAsset(t, d, "a1", "assets/max.png")
	if !got.Success || got.Result == nil {
		t.Fatalf("read asset(max size) = %+v, want success", got)
	}

	marshaled, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if len(marshaled) > maxAssetMessageBytes {
		t.Fatalf("marshaled message = %d bytes, want <= %d (maxAssetMessageBytes)", len(marshaled), maxAssetMessageBytes)
	}
}

// Tested directly rather than through a real long path: macOS's PATH_MAX (1024)
// makes a real >4 KiB path uncreatable, but the check must hold past that.
func TestAssetMessageFitsRejectsLongPath(t *testing.T) {
	longPath := strings.Repeat("d/", 4096) + "x.png"
	if fits, err := assetMessageFits("a1", longPath, "image/png", maxAssetBytes); err != nil {
		t.Fatalf("assetMessageFits(long path): %v", err)
	} else if fits {
		t.Fatalf("assetMessageFits(long path) = true, want false")
	}

	if fits, err := assetMessageFits("a1", "assets/pic.png", "image/png", maxAssetBytes); err != nil {
		t.Fatalf("assetMessageFits(short path): %v", err)
	} else if !fits {
		t.Fatalf("assetMessageFits(short path) = false, want true")
	}
}

func TestFsReadAssetUnsupportedExtension(t *testing.T) {
	d := newFsDaemon(t)
	if res := fsWriteCAS(t, d, "assets/doc.pdf", "not an image", ""); !res.Success || res.Result == nil {
		t.Fatalf("seed write = %+v", res.Result)
	}

	got := fsReadAsset(t, d, "a1", "assets/doc.pdf")
	if got.Success || got.Error == nil || *got.Error != "not a supported image asset" {
		t.Fatalf("read asset(unsupported ext) = %+v, want \"not a supported image asset\"", got)
	}
}

func TestFsCommandsWithExplicitRootActOnThatDirectory(t *testing.T) {
	d := newFsDaemon(t)
	notebookRoot, err := d.notebookRoot()
	if err != nil {
		t.Fatal(err)
	}
	externalRoot := t.TempDir()

	watchClient := trustedFsClient(4)
	if res := fsWatch(t, d, watchClient, "w1", externalRoot); !res.Success {
		t.Fatalf("fs_watch(externalRoot) = %+v", res)
	}

	write := fsWriteCASRoot(t, d, "notes/todo.txt", "buy milk", "", externalRoot)
	if !write.Success || write.Result == nil || write.Result.Conflict || write.Result.Hash == nil {
		t.Fatalf("write under explicit root = %+v", write.Result)
	}
	if write.Result.Path != "notes/todo.txt" {
		t.Fatalf("write result path = %q, want root-relative notes/todo.txt", write.Result.Path)
	}

	if _, err := os.Stat(filepath.Join(externalRoot, "notes", "todo.txt")); err != nil {
		t.Fatalf("file did not land under explicit root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(notebookRoot, "notes", "todo.txt")); err == nil {
		t.Fatalf("file wrongly landed under the notebook root")
	}

	entries := listFsRoot(t, d, "notes", externalRoot)
	if len(entries) != 1 || entries[0].Path != "notes/todo.txt" {
		t.Fatalf("list under explicit root = %+v", entries)
	}

	client := trustedFsClient(4)
	d.sendFsReadWSResult(client, "r1", "notes/todo.txt", externalRoot)
	var read protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &read)
	if !read.Success || read.Result == nil || read.Result.Content != "buy milk" {
		t.Fatalf("read under explicit root = %+v", read.Result)
	}

	ev := waitForFsChangeWithRoot(t, watchClient.send, originUI)
	if !slices.Contains(ev.Paths, "notes/todo.txt") {
		t.Fatalf("ui fs_changed paths = %v, want notes/todo.txt", ev.Paths)
	}
	if ev.Root != externalRoot {
		t.Fatalf("ui fs_changed root = %q, want %q", ev.Root, externalRoot)
	}
}

func waitForFsChangeWithRoot(t *testing.T, ch chan outboundMessage, origin string) protocol.FsChangedMessage {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			var ev protocol.FsChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err != nil {
				continue
			}
			if ev.Event == protocol.EventFsChanged && ev.Origin == origin {
				return ev
			}
		case <-deadline:
			t.Fatalf("no fs_changed with origin %q was broadcast", origin)
			return protocol.FsChangedMessage{}
		}
	}
}

func TestFsWriteBroadcastsResolvedRoot(t *testing.T) {
	d := newFsDaemon(t)
	externalRoot := t.TempDir()

	watchClient := trustedFsClient(4)
	if res := fsWatch(t, d, watchClient, "w1", externalRoot); !res.Success {
		t.Fatalf("fs_watch(externalRoot) = %+v", res)
	}

	if res := fsWriteCASRoot(t, d, "a.txt", "x", "", externalRoot); !res.Success || res.Result == nil {
		t.Fatalf("write = %+v", res.Result)
	}
	ev := waitForFsChangeWithRoot(t, watchClient.send, originUI)
	if ev.Root != externalRoot {
		t.Fatalf("fs_changed.root = %q, want %q", ev.Root, externalRoot)
	}
}

func TestFsCommandsOmittedRootResolvesToNotebookRoot(t *testing.T) {
	d := newFsDaemon(t)
	notebookRoot, err := d.notebookRoot()
	if err != nil {
		t.Fatal(err)
	}
	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	res := fsWriteCAS(t, d, "notes/todo.txt", "buy milk", "")
	if !res.Success || res.Result == nil {
		t.Fatalf("write = %+v", res.Result)
	}
	if _, err := os.Stat(filepath.Join(notebookRoot, "notes", "todo.txt")); err != nil {
		t.Fatalf("file did not land under the notebook root: %v", err)
	}
	ev := waitForFsChangeWithRoot(t, hubClient.send, originUI)
	if ev.Root != notebookRoot {
		t.Fatalf("fs_changed.root = %q, want notebook root %q", ev.Root, notebookRoot)
	}
}

func TestFsCommandsRejectInvalidRoots(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(config.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	d := newFsDaemon(t)

	client := trustedFsClient(4)
	d.sendFsListWSResult(client, "rel", "", "relative/path")
	var relRes protocol.FsListResultMessage
	readNotebookWSEvent(t, client.send, &relRes)
	if relRes.Success || relRes.Error == nil {
		t.Fatalf("fs_list(relative root) = %+v, want failure", relRes)
	}

	insideDataDir := filepath.Join(config.DataDir(), "notebook")
	client2 := trustedFsClient(4)
	d.sendFsListWSResult(client2, "indatadir", "", insideDataDir)
	var dataDirRes protocol.FsListResultMessage
	readNotebookWSEvent(t, client2.send, &dataDirRes)
	if dataDirRes.Success || dataDirRes.Error == nil {
		t.Fatalf("fs_list(root inside data dir) = %+v, want failure", dataDirRes)
	}
}

// fsdoc.Store permits symlinked roots, so the data-dir exclusion cannot be
// lexical: a root that symlinks into config.DataDir() reaches attn.db.
func TestFsCommandsRejectSymlinkedRootIntoDataDir(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(config.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	d := newFsDaemon(t)

	sentinel := filepath.Join(config.DataDir(), "attn.db")
	if err := os.WriteFile(sentinel, []byte("sqlite-ish"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkDir := t.TempDir()
	directLink := filepath.Join(linkDir, "editor-root")
	if err := os.Symlink(config.DataDir(), directLink); err != nil {
		t.Fatal(err)
	}

	client := trustedFsClient(4)
	d.sendFsListWSResult(client, "l1", "", directLink)
	var listRes protocol.FsListResultMessage
	readNotebookWSEvent(t, client.send, &listRes)
	if listRes.Success || listRes.Error == nil {
		t.Fatalf("fs_list(symlink into data dir) = %+v, want failure", listRes)
	}
	if !strings.Contains(*listRes.Error, "outside the attn data dir") {
		t.Fatalf("fs_list(symlink into data dir) error = %q, want it to mention the data dir exclusion", *listRes.Error)
	}

	deleteClient := trustedFsClient(4)
	d.sendFsDeleteWSResult(deleteClient, "d1", "attn.db", directLink)
	var deleteRes protocol.FsDeleteResultMessage
	readNotebookWSEvent(t, deleteClient.send, &deleteRes)
	if deleteRes.Success || deleteRes.Error == nil {
		t.Fatalf("fs_delete(symlink into data dir) = %+v, want failure", deleteRes)
	}
	if !strings.Contains(*deleteRes.Error, "outside the attn data dir") {
		t.Fatalf("fs_delete(symlink into data dir) error = %q, want it to mention the data dir exclusion", *deleteRes.Error)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("fs_delete(symlink into data dir) must not touch the data dir, stat err = %v", err)
	}

	subdir := filepath.Join(config.DataDir(), "workers")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	nestedLink := filepath.Join(linkDir, "editor-root-nested")
	if err := os.Symlink(subdir, nestedLink); err != nil {
		t.Fatal(err)
	}
	nestedClient := trustedFsClient(4)
	d.sendFsListWSResult(nestedClient, "l2", "", nestedLink)
	var nestedRes protocol.FsListResultMessage
	readNotebookWSEvent(t, nestedClient.send, &nestedRes)
	if nestedRes.Success || nestedRes.Error == nil {
		t.Fatalf("fs_list(symlink into data dir subdir) = %+v, want failure", nestedRes)
	}
	if !strings.Contains(*nestedRes.Error, "outside the attn data dir") {
		t.Fatalf("fs_list(symlink into data dir subdir) error = %q, want it to mention the data dir exclusion", *nestedRes.Error)
	}
}

// t.TempDir() lives under /var/folders, reached via a /private symlink, so this
// is the real canonicalize-both-sides path.
func TestFsCommandsAcceptLegitimateSymlinkedRoot(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(config.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	d := newFsDaemon(t)

	target := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "editor-root")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	client := trustedFsClient(4)
	d.sendFsWriteWSResult(client, "w1", "note.md", "hello", "", link)
	var res protocol.FsWriteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("fs_write(legitimate symlinked root) = %+v, want success", res)
	}
	if _, err := os.Stat(filepath.Join(target, "note.md")); err != nil {
		t.Fatalf("file did not land under the symlink target: %v", err)
	}
}

func TestFsReadWithExplicitRootDeniedForUntrustedClient(t *testing.T) {
	d := newFsDaemon(t)
	externalRoot := t.TempDir()
	secretPath := filepath.Join(externalRoot, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsReadWSResult(client, "r1", "secret.txt", externalRoot)
	var res protocol.FsReadResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_read(explicit root, untrusted client) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, "authenticated") {
		t.Fatalf("fs_read(explicit root, untrusted client) error = %q, want it to mention the authenticated app", *res.Error)
	}
}

func TestFsWriteWithExplicitRootDeniedForUntrustedClientAndFileNotCreated(t *testing.T) {
	d := newFsDaemon(t)
	externalRoot := t.TempDir()

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsWriteWSResult(client, "w1", "pwned.txt", "attacker-controlled content", "", externalRoot)
	var res protocol.FsWriteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_write(explicit root, untrusted client) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, "authenticated") {
		t.Fatalf("fs_write(explicit root, untrusted client) error = %q, want it to mention the authenticated app", *res.Error)
	}
	if _, err := os.Stat(filepath.Join(externalRoot, "pwned.txt")); !os.IsNotExist(err) {
		t.Fatalf("fs_write(explicit root, untrusted client) must not create the file, stat err = %v", err)
	}
}

func TestFsCommandsOmittedRootStillWorksForUntrustedClient(t *testing.T) {
	d := newFsDaemon(t)
	notebookRoot, err := d.notebookRoot()
	if err != nil {
		t.Fatal(err)
	}

	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsWriteWSResult(client, "w1", "notes/todo.txt", "buy milk", "", "")
	var res protocol.FsWriteResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if !res.Success || res.Result == nil || res.Result.Conflict {
		t.Fatalf("fs_write(omitted root, untrusted client) = %+v, want success", res)
	}
	if _, err := os.Stat(filepath.Join(notebookRoot, "notes", "todo.txt")); err != nil {
		t.Fatalf("file did not land under the notebook root: %v", err)
	}
}

func TestFsCommandsExplicitRootDeniedForNonTauriAppClientKind(t *testing.T) {
	d := newFsDaemon(t)
	externalRoot := t.TempDir()

	client := &wsClient{send: make(chan outboundMessage, 4), trustedTauriOrigin: true}
	client.setBrowserHostAuthenticated(true)
	client.setIdentity("not-tauri-app", "test", nil)

	d.sendFsListWSResult(client, "l1", "", externalRoot)
	var res protocol.FsListResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_list(explicit root, non-tauri-app clientKind) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, "authenticated") {
		t.Fatalf("fs_list(explicit root, non-tauri-app clientKind) error = %q, want it to mention the authenticated app", *res.Error)
	}
}

func TestFsRenameDeleteNotebookCouplingIsRootConditional(t *testing.T) {
	d := newFsDaemon(t)
	notebookRoot, err := d.notebookRoot()
	if err != nil {
		t.Fatal(err)
	}
	externalRoot := t.TempDir()

	hubClient := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[hubClient] = true
	go d.wsHub.run()

	watchClient := trustedFsClient(8)
	if res := fsWatch(t, d, watchClient, "w1", externalRoot); !res.Success {
		t.Fatalf("fs_watch(externalRoot) = %+v", res)
	}

	if res := fsWriteCASRoot(t, d, "a.md", "x", "", externalRoot); !res.Success || res.Result == nil {
		t.Fatalf("seed write (external) = %+v", res.Result)
	}
	waitForFsChange(t, watchClient.send, originUI)
	renameClient := trustedFsClient(4)
	d.sendFsRenameWSResult(renameClient, "rn1", "a.md", "b.md", externalRoot)
	var renameRes protocol.FsRenameResultMessage
	readNotebookWSEvent(t, renameClient.send, &renameRes)
	if !renameRes.Success || renameRes.Result == nil {
		t.Fatalf("rename (external root) = %+v", renameRes)
	}
	assertNoBroadcast(t, hubClient.send, protocol.EventNotebookChanged, 300*time.Millisecond)
	if got := waitForFsChange(t, watchClient.send, originUI); !slices.Contains(got, "b.md") {
		t.Fatalf("fs_changed after external rename = %v, want b.md", got)
	}

	deleteClient := trustedFsClient(4)
	d.sendFsDeleteWSResult(deleteClient, "d1", "b.md", externalRoot)
	var deleteRes protocol.FsDeleteResultMessage
	readNotebookWSEvent(t, deleteClient.send, &deleteRes)
	if !deleteRes.Success || deleteRes.Result == nil {
		t.Fatalf("delete (external root) = %+v", deleteRes)
	}
	assertNoBroadcast(t, hubClient.send, protocol.EventNotebookChanged, 300*time.Millisecond)
	if got := waitForFsChange(t, watchClient.send, originUI); !slices.Contains(got, "b.md") {
		t.Fatalf("fs_changed after external delete = %v, want b.md", got)
	}

	if res := fsWriteCAS(t, d, "c.md", "x", ""); !res.Success || res.Result == nil {
		t.Fatalf("seed write (notebook) = %+v", res.Result)
	}
	waitForFsChange(t, hubClient.send, originUI)
	renameClient2 := &wsClient{send: make(chan outboundMessage, 4)}
	d.sendFsRenameWSResult(renameClient2, "rn2", "c.md", "d.md", "")
	var renameRes2 protocol.FsRenameResultMessage
	readNotebookWSEvent(t, renameClient2.send, &renameRes2)
	if !renameRes2.Success || renameRes2.Result == nil {
		t.Fatalf("rename (notebook root) = %+v", renameRes2)
	}
	notebookEv := waitForNotebookChangeEvent(t, hubClient.send)
	if !slices.Contains(notebookEv, "d.md") {
		t.Fatalf("notebook_changed after notebook rename = %v, want d.md", notebookEv)
	}
	if got := waitForFsChange(t, hubClient.send, originUI); !slices.Contains(got, "d.md") {
		t.Fatalf("fs_changed after notebook rename = %v, want d.md", got)
	}
	_ = notebookRoot
}

func assertNoBroadcast(t *testing.T, ch chan outboundMessage, eventType string, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	var drained []outboundMessage
	for {
		select {
		case msg := <-ch:
			var ev struct {
				Event string `json:"event"`
			}
			if err := json.Unmarshal(msg.payload, &ev); err == nil && ev.Event == eventType {
				t.Fatalf("unexpected %s broadcast", eventType)
			}
			drained = append(drained, msg)
		case <-deadline:
			for _, m := range drained {
				ch <- m
			}
			return
		}
	}
}

func waitForNotebookChangeEvent(t *testing.T, ch chan outboundMessage) []string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			var ev protocol.NotebookChangedMessage
			if err := json.Unmarshal(msg.payload, &ev); err != nil {
				continue
			}
			if ev.Event == protocol.EventNotebookChanged {
				return ev.Paths
			}
		case <-deadline:
			t.Fatalf("no notebook_changed was broadcast")
			return nil
		}
	}
}
