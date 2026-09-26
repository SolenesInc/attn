package daemon_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const fsWatchedRootsCap = 16

func fsAskWatch(p *testworld.Peer, root string) protocol.FsWatchResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsWatchResultMessage](p, protocol.EventFsWatchResult, func(id *string) any {
		return protocol.FsWatchMessage{Cmd: protocol.CmdFsWatch, RequestID: id, Root: fsRoot(root)}
	})
}

func fsAskUnwatch(p *testworld.Peer, root string) protocol.FsUnwatchResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsUnwatchResultMessage](p, protocol.EventFsUnwatchResult, func(id *string) any {
		return protocol.FsUnwatchMessage{Cmd: protocol.CmdFsUnwatch, RequestID: id, Root: fsRoot(root)}
	})
}

func fsMustWatch(t *testing.T, p *testworld.Peer, root string) {
	t.Helper()
	if watched := fsAskWatch(p, root); !watched.Success || protocol.Deref(watched.Root) != root {
		t.Fatalf("watching %s = %+v (%s)", root, watched, protocol.Deref(watched.Error))
	}
}

func fsAwaitLaterChange(t *testing.T, subscriber *testworld.Peer, root string) {
	t.Helper()
	fsWriteFile(t, filepath.Join(root, "later.txt"), []byte("later"))
	fsAwaitChanged(subscriber, func(m protocol.FsChangedMessage) bool {
		return m.Root == root && slices.Contains(m.Paths, "later.txt")
	})
}

func fsChangesHeardByNow(p *testworld.Peer) int {
	p.T.Helper()
	fsAskList(p, "", "")
	heard := 0
	for _, e := range p.Received() {
		if e.Event == protocol.EventFsChanged {
			heard++
		}
	}
	return heard
}

func TestFsWatchReportsExternalEditsOnlyToItsSubscribers(t *testing.T) {
	w := newFsWorld(t)
	watching, other := pickerApp(w), pickerApp(w)
	watched := fsDir(t, "watched")
	fsMustWatch(t, watching, watched)
	fsMustWatch(t, other, fsDir(t, "quiet"))

	fsWriteFile(t, filepath.Join(watched, "note.txt"), []byte("hello"))
	if edit := fsAwaitChanged(watching, func(m protocol.FsChangedMessage) bool { return m.Root == watched }); edit.Origin != "external" || !slices.Contains(edit.Paths, "note.txt") {
		t.Fatalf("the subscriber heard %+v, want note.txt as an external edit", edit)
	}

	fsAwaitLaterChange(t, watching, watched)
	if heard := fsChangesHeardByNow(other); heard != 0 {
		t.Fatalf("a client that never watched %s heard %d changes", watched, heard)
	}
}

func TestAWatchedRootStaysWatchedUntilItsLastClientUnwatches(t *testing.T) {
	w := newFsWorld(t)
	leaving, staying := pickerApp(w), pickerApp(w)
	shared := fsDir(t, "shared")
	fsMustWatch(t, leaving, shared)
	fsMustWatch(t, staying, shared)

	if unwatched := fsAskUnwatch(leaving, shared); !unwatched.Success || protocol.Deref(unwatched.Root) != shared {
		t.Fatalf("the first client unwatching %s = %+v", shared, unwatched)
	}
	fsWriteFile(t, filepath.Join(shared, "still-watched.txt"), []byte("x"))
	fsAwaitChanged(staying, func(m protocol.FsChangedMessage) bool {
		return m.Root == shared && m.Origin == "external" && slices.Contains(m.Paths, "still-watched.txt")
	})

	if unwatched := fsAskUnwatch(staying, shared); !unwatched.Success || protocol.Deref(unwatched.Root) != shared {
		t.Fatalf("unwatching %s = %+v", shared, unwatched)
	}
	fsWriteFile(t, filepath.Join(shared, "no-longer-watched.txt"), []byte("x"))
	fsMustWatch(t, staying, shared)
	fsWriteFile(t, filepath.Join(shared, "watched-again.txt"), []byte("x"))
	if first := fsAwaitChanged(staying, func(m protocol.FsChangedMessage) bool {
		return m.Root == shared && (slices.Contains(m.Paths, "no-longer-watched.txt") || slices.Contains(m.Paths, "watched-again.txt"))
	}); slices.Contains(first.Paths, "no-longer-watched.txt") {
		t.Fatalf("after its last client unwatched it, %s still reported %+v", shared, first)
	}
}

func TestOwnWritesAndNotebookNotesSurfaceWithTheRightOrigin(t *testing.T) {
	w := newFsWorld(t)
	app := pickerApp(w)
	watched := fsDir(t, "watched")
	fsMustWatch(t, app, watched)

	fsAskWrite(app, watched, "own.txt", "attn wrote this", "")
	fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool {
		return m.Root == watched && m.Origin == "ui" && slices.Contains(m.Paths, "own.txt")
	})
	fsWriteFile(t, filepath.Join(watched, "sentinel.txt"), []byte("edited outside"))
	if external := fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool {
		return m.Root == watched && m.Origin == "external" && (slices.Contains(m.Paths, "own.txt") || slices.Contains(m.Paths, "sentinel.txt"))
	}); slices.Contains(external.Paths, "own.txt") || !slices.Contains(external.Paths, "sentinel.txt") {
		t.Fatalf("the first external change was %v, want the outside edit without attn's own write", external.Paths)
	}

	root := fsNotebookRoot(t, w)
	fsAskList(app, "", "")
	fsWriteFile(t, filepath.Join(root, "plain.txt"), []byte("hi"))
	fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool {
		return m.Root == root && m.Origin == "external" && slices.Contains(m.Paths, "plain.txt")
	})
	fsWriteFile(t, filepath.Join(root, "note.md"), []byte("---\ntype: note\n---\nhi\n"))
	fsAwaitChanged(app, func(m protocol.FsChangedMessage) bool {
		return m.Root == root && m.Origin == "external" && slices.Contains(m.Paths, "note.md")
	})
	if note := testworld.Await(app, protocol.EventNotebookChanged, func(m protocol.NotebookChangedMessage) bool {
		return slices.Contains(m.Paths, "plain.txt") || slices.Contains(m.Paths, "note.md")
	}); slices.Contains(note.Paths, "plain.txt") || !slices.Contains(note.Paths, "note.md") || note.Origin != "external" {
		t.Fatalf("the notebook heard %+v, want the note alone as an external change", note)
	}
}

func TestFsWatchRefusesPastItsCapAndGuardsExplicitRoots(t *testing.T) {
	w := newFsWorld(t)
	app := pickerApp(w)
	notebookRoot := fsNotebookRoot(t, w)
	watched := make([]string, fsWatchedRootsCap)
	for i := range watched {
		watched[i] = fsDir(t, fmt.Sprintf("watched-%d", i))
		fsMustWatch(t, app, watched[i])
	}
	oneTooMany := fsDir(t, "one-too-many")
	if overflow := fsAskWatch(app, oneTooMany); overflow.Success || protocol.Deref(overflow.Error) != "too many watched roots" {
		t.Fatalf("watching one root past the cap = %+v (%s)", overflow, protocol.Deref(overflow.Error))
	}
	if unwatched := fsAskUnwatch(app, watched[0]); !unwatched.Success {
		t.Fatalf("unwatching %s = %+v", watched[0], unwatched)
	}
	fsMustWatch(t, app, oneTooMany)
	fsAskUnwatch(app, oneTooMany)

	remote := fsRemotePeer(w)
	refusedRoot := fsDir(t, "refused")
	if refused := fsAskWatch(remote, refusedRoot); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "authenticated") {
		t.Fatalf("an unauthenticated client watched an explicit root: %+v (%s)", refused, protocol.Deref(refused.Error))
	}
	if omitted := fsAskWatch(remote, ""); !omitted.Success || protocol.Deref(omitted.Root) != notebookRoot {
		t.Fatalf("an unauthenticated client could not watch the notebook root: %+v (%s)", omitted, protocol.Deref(omitted.Error))
	}

	fsWriteFile(t, filepath.Join(refusedRoot, "unwatched.txt"), []byte("x"))
	fsMustWatch(t, app, refusedRoot)
	fsAwaitLaterChange(t, app, refusedRoot)
	if heard := fsChangesHeardByNow(remote); heard != 0 {
		t.Fatalf("the refused root was watched after all: the unauthenticated client heard %d changes", heard)
	}
}
