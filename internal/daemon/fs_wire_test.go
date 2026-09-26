package daemon_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func fsRequest[T any](p *testworld.Peer, event string, cmd func(requestID *string) any) T {
	p.T.Helper()
	id := uuid.NewString()
	p.Send(cmd(protocol.Ptr(id)))
	raw := testworld.Await(p, event, func(m json.RawMessage) bool {
		var answer struct {
			RequestID string `json:"request_id"`
		}
		return json.Unmarshal(m, &answer) == nil && answer.RequestID == id
	})
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		p.T.Fatalf("decode %s: %v", event, err)
	}
	return result
}

func fsRoot(root string) *string {
	if root == "" {
		return nil
	}
	return protocol.Ptr(root)
}

func fsAskWrite(p *testworld.Peer, root, path, content, baseHash string) protocol.FsWriteResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsWriteResultMessage](p, protocol.EventFsWriteResult, func(id *string) any {
		msg := protocol.FsWriteMessage{Cmd: protocol.CmdFsWrite, RequestID: id, Path: path, Content: content, Root: fsRoot(root)}
		if baseHash != "" {
			msg.BaseHash = protocol.Ptr(baseHash)
		}
		return msg
	})
}

func fsAskRead(p *testworld.Peer, root, path string) protocol.FsReadResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsReadResultMessage](p, protocol.EventFsReadResult, func(id *string) any {
		return protocol.FsReadMessage{Cmd: protocol.CmdFsRead, RequestID: id, Path: path, Root: fsRoot(root)}
	})
}

func fsAskList(p *testworld.Peer, root, path string) protocol.FsListResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsListResultMessage](p, protocol.EventFsListResult, func(id *string) any {
		return protocol.FsListMessage{Cmd: protocol.CmdFsList, RequestID: id, Path: protocol.Ptr(path), Root: fsRoot(root)}
	})
}

func fsAskExists(p *testworld.Peer, path string) protocol.FsExistsResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsExistsResultMessage](p, protocol.EventFsExistsResult, func(id *string) any {
		return protocol.FsExistsMessage{Cmd: protocol.CmdFsExists, RequestID: id, Path: path}
	})
}

func fsAskRename(p *testworld.Peer, root, from, to string) protocol.FsRenameResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsRenameResultMessage](p, protocol.EventFsRenameResult, func(id *string) any {
		return protocol.FsRenameMessage{Cmd: protocol.CmdFsRename, RequestID: id, Path: from, NewPath: to, Root: fsRoot(root)}
	})
}

func fsAskDelete(p *testworld.Peer, root, path string) protocol.FsDeleteResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsDeleteResultMessage](p, protocol.EventFsDeleteResult, func(id *string) any {
		return protocol.FsDeleteMessage{Cmd: protocol.CmdFsDelete, RequestID: id, Path: path, Root: fsRoot(root)}
	})
}

func fsAskAsset(p *testworld.Peer, path string) protocol.FsReadAssetResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsReadAssetResultMessage](p, protocol.EventFsReadAssetResult, func(id *string) any {
		return protocol.FsReadAssetMessage{Cmd: protocol.CmdFsReadAsset, RequestID: id, Path: path}
	})
}

func fsAwaitChanged(p *testworld.Peer, match func(protocol.FsChangedMessage) bool) protocol.FsChangedMessage {
	p.T.Helper()
	return testworld.Await(p, protocol.EventFsChanged, match)
}

func fsDir(t *testing.T, name string) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fsNotebookRoot(t *testing.T, w *world) string {
	t.Helper()
	root := filepath.Join(w.Dir, "notebook")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func fsWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFsWorld(t *testing.T) *world {
	t.Helper()
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", "fs-browser-host-token")
	return newWorld(t)
}

func fsRemotePeer(w *world) *testworld.Peer {
	w.T.Helper()
	remote := w.Connect(helloWithToken(protocol.Ptr(config.ClientToken())), nil)
	testworld.Await[protocol.InitialStateMessage](remote, protocol.EventInitialState, nil)
	return remote
}

func TestNotebookFileCommandsRoundTripThroughTheApp(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	root := fsNotebookRoot(t, w)

	created := fsAskWrite(app, "", "/notes/todo.txt", "buy milk", "")
	if !created.Success || created.Result == nil || created.Result.Conflict || created.Result.Hash == nil || created.Result.Path != "notes/todo.txt" {
		t.Fatalf("writing /notes/todo.txt = %+v (%s), want it saved at notes/todo.txt with a hash", created.Result, protocol.Deref(created.Error))
	}
	hash := *created.Result.Hash

	if top := fsAskList(app, "", ""); !top.Success || len(top.Entries) != 1 || top.Entries[0].Name != "notes" || !top.Entries[0].IsDir {
		t.Fatalf("listing the root = %+v, want the notes directory alone", top.Entries)
	}
	if notes := fsAskList(app, "", "notes"); !notes.Success || len(notes.Entries) != 1 || notes.Entries[0].Path != "notes/todo.txt" ||
		notes.Entries[0].IsDir || notes.Entries[0].Size != len("buy milk") || notes.Entries[0].Modified == nil {
		t.Fatalf("listing notes = %+v", notes.Entries)
	}
	if read := fsAskRead(app, "", "notes/todo.txt"); !read.Success || read.Result == nil || read.Result.Content != "buy milk" || read.Result.Hash != hash {
		t.Fatalf("reading notes/todo.txt = %+v, want the content and the write's hash", read.Result)
	}
	if missing := fsAskRead(app, "", "nope.txt"); missing.Success || missing.Error == nil {
		t.Fatalf("reading a missing file = %+v, want a failure", missing)
	}
	fsWriteFile(t, filepath.Join(root, "attachments", "too-large.txt"), bytes.Repeat([]byte("x"), fsdoc.MaxFileSize+1))
	if oversized := fsAskRead(app, "", "attachments/too-large.txt"); oversized.Success || !strings.Contains(protocol.Deref(oversized.Error), "read cap") {
		t.Fatalf("reading a file over the read cap = %+v (%s)", oversized.Result, protocol.Deref(oversized.Error))
	}

	for _, c := range []struct {
		path   string
		ok     bool
		exists bool
	}{{"/notes/todo.txt", true, true}, {"/notes/missing.txt", true, false}, {".secret", false, false}} {
		got := fsAskExists(app, c.path)
		if got.Success != c.ok || (c.ok && (got.Result == nil || got.Result.Exists != c.exists)) || (!c.ok && got.Error == nil) {
			t.Errorf("exists %s = %+v (%s), want success=%v exists=%v", c.path, got.Result, protocol.Deref(got.Error), c.ok, c.exists)
		}
	}

	if stale := fsAskWrite(app, "", "notes/todo.txt", "oat milk", "deadbeef"); !stale.Success || stale.Result == nil || !stale.Result.Conflict || protocol.Deref(stale.Result.CurrentHash) != hash {
		t.Fatalf("a write against a stale hash = %+v, want a conflict naming the current hash %s", stale.Result, hash)
	}
	if saved := fsAskWrite(app, "", "notes/todo.txt", "oat milk", hash); !saved.Success || saved.Result == nil || saved.Result.Conflict || saved.Result.Hash == nil {
		t.Fatalf("a write against the current hash = %+v, want it saved", saved.Result)
	}
	if renamed := fsAskRename(app, "", "notes/todo.txt", "notes/plan.txt"); !renamed.Success || renamed.Result == nil || renamed.Result.NewPath != "notes/plan.txt" {
		t.Fatalf("renaming = %+v (%s)", renamed.Result, protocol.Deref(renamed.Error))
	}
	if deleted := fsAskDelete(app, "", "notes/plan.txt"); !deleted.Success || deleted.Result == nil || deleted.Result.Path != "notes/plan.txt" {
		t.Fatalf("deleting = %+v (%s)", deleted.Result, protocol.Deref(deleted.Error))
	}
}

func TestFsChangedTellsTheAppsWhoChangedAFileAndOnlyNotesReachTheNotebook(t *testing.T) {
	w := newFsWorld(t)
	app := pickerApp(w)
	watcher := w.App()
	root := fsNotebookRoot(t, w)

	fsAskWrite(app, "", "/notes/todo.md", "x", "")
	if ui := fsAwaitChanged(watcher, func(m protocol.FsChangedMessage) bool { return m.Origin == "ui" }); ui.Root != root || !slices.Equal(ui.Paths, []string{"notes/todo.md"}) {
		t.Fatalf("the app's write reached the other app as %+v, want origin ui under %s with the normalized path", ui, root)
	}

	fsAskWrite(app, "", "own.md", "attn wrote this", "")
	fsWriteFile(t, filepath.Join(root, "ext.md"), []byte("edited externally"))
	external := fsAwaitChanged(watcher, func(m protocol.FsChangedMessage) bool {
		return m.Origin == "external" && (slices.Contains(m.Paths, "ext.md") || slices.Contains(m.Paths, "own.md"))
	})
	if !slices.Contains(external.Paths, "ext.md") || slices.Contains(external.Paths, "own.md") || external.Root != root {
		t.Fatalf("the edit made outside attn arrived as %+v, want ext.md under %s without attn's own write", external, root)
	}

	elsewhere := fsDir(t, "elsewhere")
	if watched := fsAskWatch(app, elsewhere); !watched.Success {
		t.Fatalf("watching %s: %s", elsewhere, protocol.Deref(watched.Error))
	}
	fsAskWrite(app, elsewhere, "a.md", "x", "")
	fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool { return m.Root == elsewhere && slices.Contains(m.Paths, "a.md") })
	if renamed := fsAskRename(app, elsewhere, "a.md", "b.md"); !renamed.Success {
		t.Fatalf("renaming outside the notebook: %s", protocol.Deref(renamed.Error))
	}
	if moved := fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool { return m.Root == elsewhere && slices.Contains(m.Paths, "b.md") }); moved.Origin != "ui" {
		t.Fatalf("the rename outside the notebook arrived as %+v", moved)
	}
	if deleted := fsAskDelete(app, elsewhere, "b.md"); !deleted.Success {
		t.Fatalf("deleting outside the notebook: %s", protocol.Deref(deleted.Error))
	}
	fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool {
		return m.Root == elsewhere && slices.Equal(m.Paths, []string{"b.md"})
	})

	fsAskWrite(app, "", "c.md", "x", "")
	fsAskRename(app, "", "c.md", "d.md")
	note := testworld.Await(watcher, protocol.EventNotebookChanged, func(m protocol.NotebookChangedMessage) bool {
		return slices.ContainsFunc(m.Paths, func(p string) bool { return slices.Contains([]string{"a.md", "b.md", "d.md"}, p) })
	})
	if !slices.Contains(note.Paths, "d.md") || slices.Contains(note.Paths, "a.md") || slices.Contains(note.Paths, "b.md") || note.Origin != "ui" {
		t.Fatalf("the first notebook_changed naming a renamed file was %+v; want only the notebook's rename to d.md", note)
	}
	fsAwaitChanged(watcher, func(m protocol.FsChangedMessage) bool { return m.Root == root && slices.Contains(m.Paths, "d.md") })
}

func TestNotebookImageAssetsAreServedWithinTheMessageCap(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	root := fsNotebookRoot(t, w)
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	fsWriteFile(t, filepath.Join(root, "assets", "pic.png"), png)
	fsWriteFile(t, filepath.Join(root, "assets", "doc.pdf"), []byte("not an image"))
	const messageCap = 8 << 20
	fsWriteFile(t, filepath.Join(root, "assets", "huge.png"), bytes.Repeat([]byte{0xFF}, messageCap))

	served := fsAskAsset(app, "assets/pic.png")
	if !served.Success || served.Result == nil || served.Result.MimeType != "image/png" {
		t.Fatalf("reading a png = %+v (%s)", served.Result, protocol.Deref(served.Error))
	}
	if decoded, err := base64.StdEncoding.DecodeString(served.Result.DataBase64); err != nil || !bytes.Equal(decoded, png) {
		t.Fatalf("the png arrived as %v (%v), want %v", decoded, err, png)
	}

	for _, c := range []struct {
		path string
		want string
	}{
		{"../outside.png", ""},
		{"assets/nope.png", ""},
		{"assets/doc.pdf", "not a supported image asset"},
		{"assets/huge.png", "cap"},
	} {
		if refused := fsAskAsset(app, c.path); refused.Success || refused.Error == nil || !strings.Contains(*refused.Error, c.want) {
			t.Errorf("reading %s = %+v (%s), want a refusal mentioning %q", c.path, refused.Result, protocol.Deref(refused.Error), c.want)
		}
	}

	refusal := protocol.Deref(fsAskAsset(app, "assets/huge.png").Error)
	named := regexp.MustCompile(`exceeds (?:the )?(\d+) byte read cap`).FindStringSubmatch(refusal)
	if named == nil {
		t.Fatalf("the oversize refusal %q does not name the read cap", refusal)
	}
	readCap, _ := strconv.Atoi(named[1])
	fsWriteFile(t, filepath.Join(root, "assets", "max.png"), bytes.Repeat([]byte{0xFF}, readCap))
	largest := fsAskAsset(app, "assets/max.png")
	if !largest.Success || largest.Result == nil {
		t.Fatalf("reading an asset of exactly the read cap = %s", protocol.Deref(largest.Error))
	}
	if message, err := json.Marshal(largest); err != nil || len(message) > messageCap {
		t.Fatalf("the largest asset took a %d byte message, over the %d byte cap", len(message), messageCap)
	}

	escapedPastTheEnvelopeAllowance := strings.Repeat(strings.Repeat("&", 250)+"/", 4) + "max.png"
	fsWriteFile(t, filepath.Join(root, filepath.FromSlash(escapedPastTheEnvelopeAllowance)), bytes.Repeat([]byte{0xFF}, readCap))
	deep := fsAskAsset(app, escapedPastTheEnvelopeAllowance)
	if deep.Success {
		if message, err := json.Marshal(deep); err != nil || len(message) > messageCap {
			t.Fatalf("the largest asset under a long nested path took a %d byte message, over the %d byte cap", len(message), messageCap)
		}
	} else if !strings.Contains(protocol.Deref(deep.Error), "message cap") {
		t.Fatalf("reading the largest asset under a long nested path was refused with %q, want a refusal naming the message cap", protocol.Deref(deep.Error))
	}
}

func TestExplicitFsRootsAreOnlyForTheAuthenticatedAppAndNeverTheDataDir(t *testing.T) {
	w := newFsWorld(t)
	app := pickerApp(w)
	notebookRoot := fsNotebookRoot(t, w)
	external := fsDir(t, "external")
	fsWriteFile(t, filepath.Join(external, "secret.txt"), []byte("top secret"))

	if watched := fsAskWatch(app, external); !watched.Success || protocol.Deref(watched.Root) != external {
		t.Fatalf("watching %s = %+v", external, watched)
	}
	written := fsAskWrite(app, external, "notes/todo.txt", "buy milk", "")
	if !written.Success || written.Result == nil || written.Result.Path != "notes/todo.txt" {
		t.Fatalf("writing under %s = %+v (%s)", external, written.Result, protocol.Deref(written.Error))
	}
	if _, err := os.Stat(filepath.Join(external, "notes", "todo.txt")); err != nil {
		t.Fatalf("the write did not land under the explicit root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(notebookRoot, "notes", "todo.txt")); err == nil {
		t.Fatal("the write landed under the notebook root")
	}
	if listed := fsAskList(app, external, "notes"); !listed.Success || len(listed.Entries) != 1 || listed.Entries[0].Path != "notes/todo.txt" {
		t.Fatalf("listing under the explicit root = %+v", listed.Entries)
	}
	if read := fsAskRead(app, external, "notes/todo.txt"); !read.Success || read.Result == nil || read.Result.Content != "buy milk" {
		t.Fatalf("reading under the explicit root = %+v", read.Result)
	}
	if changed := fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool { return m.Origin == "ui" }); changed.Root != external || !slices.Contains(changed.Paths, "notes/todo.txt") {
		t.Fatalf("the write was announced as %+v, want the explicit root and its relative path", changed)
	}

	links, target := fsDir(t, "links"), fsDir(t, "target")
	if err := os.MkdirAll(filepath.Join(w.Dir, "workers"), 0o755); err != nil {
		t.Fatal(err)
	}
	intoDataDir, intoWorkers, legitimate := filepath.Join(links, "data"), filepath.Join(links, "workers"), filepath.Join(links, "target")
	for link, to := range map[string]string{intoDataDir: w.Dir, intoWorkers: filepath.Join(w.Dir, "workers"), legitimate: target} {
		if err := os.Symlink(to, link); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		root string
		want string
	}{
		{"a relative root", "relative/path", ""},
		{"a root inside the data dir", filepath.Join(w.Dir, "notebook"), ""},
		{"a symlink to the data dir", intoDataDir, "outside the attn data dir"},
		{"a symlink into the data dir", intoWorkers, "outside the attn data dir"},
	} {
		if listed := fsAskList(app, c.root, ""); listed.Success || !strings.Contains(protocol.Deref(listed.Error), c.want) {
			t.Errorf("listing %s = %+v (%s), want a refusal mentioning %q", c.name, listed.Entries, protocol.Deref(listed.Error), c.want)
		}
	}
	if deleted := fsAskDelete(app, intoDataDir, "attn.db"); deleted.Success || !strings.Contains(protocol.Deref(deleted.Error), "outside the attn data dir") {
		t.Fatalf("deleting attn.db through a symlink to the data dir = %+v (%s)", deleted.Result, protocol.Deref(deleted.Error))
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "attn.db")); err != nil {
		t.Fatalf("the refused delete touched the data dir: %v", err)
	}
	if saved := fsAskWrite(app, legitimate, "note.md", "hello", ""); !saved.Success || saved.Result == nil || saved.Result.Conflict {
		t.Fatalf("writing through a symlink outside the data dir = %+v (%s)", saved.Result, protocol.Deref(saved.Error))
	}
	if _, err := os.Stat(filepath.Join(target, "note.md")); err != nil {
		t.Fatalf("the write through the symlink did not land at its target: %v", err)
	}

	remote := fsRemotePeer(w)
	if read := fsAskRead(remote, external, "secret.txt"); read.Success || !strings.Contains(protocol.Deref(read.Error), "authenticated") {
		t.Errorf("an unauthenticated client read through an explicit root: %+v (%s)", read.Result, protocol.Deref(read.Error))
	}
	if wrote := fsAskWrite(remote, external, "pwned.txt", "attacker-controlled content", ""); wrote.Success || !strings.Contains(protocol.Deref(wrote.Error), "authenticated") {
		t.Errorf("an unauthenticated client wrote through an explicit root: %+v (%s)", wrote.Result, protocol.Deref(wrote.Error))
	}
	if _, err := os.Stat(filepath.Join(external, "pwned.txt")); !os.IsNotExist(err) {
		t.Errorf("the refused write created the file: %v", err)
	}
	if wrote := fsAskWrite(remote, "", "notes/remote.txt", "buy milk", ""); !wrote.Success {
		t.Errorf("an unauthenticated client lost the notebook root: %s", protocol.Deref(wrote.Error))
	}
	if _, err := os.Stat(filepath.Join(notebookRoot, "notes", "remote.txt")); err != nil {
		t.Errorf("the omitted-root write did not land in the notebook: %v", err)
	}

	impostor := w.Connect(protocol.ClientHelloMessage{
		Cmd:              protocol.CmdClientHello,
		ClientKind:       "not-tauri-app",
		Version:          "protocol-" + protocol.ProtocolVersion,
		Capabilities:     []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:      protocol.Ptr(config.ClientToken()),
		BrowserHostToken: protocol.Ptr(config.BrowserHostToken()),
	}, http.Header{"Origin": {"tauri://localhost"}})
	testworld.Await[protocol.InitialStateMessage](impostor, protocol.EventInitialState, nil)
	if listed := fsAskList(impostor, external, ""); listed.Success || !strings.Contains(protocol.Deref(listed.Error), "authenticated") {
		t.Errorf("a client that is not the tauri app listed an explicit root: %+v (%s)", listed.Entries, protocol.Deref(listed.Error))
	}
}
