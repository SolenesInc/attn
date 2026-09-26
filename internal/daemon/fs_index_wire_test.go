package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func fsAskIndex(p *testworld.Peer, root string, extensions ...string) protocol.FsIndexResultMessage {
	p.T.Helper()
	return fsRequest[protocol.FsIndexResultMessage](p, protocol.EventFsIndexResult, func(id *string) any {
		return protocol.FsIndexMessage{Cmd: protocol.CmdFsIndex, RequestID: id, Root: fsRoot(root), Extensions: extensions}
	})
}

func TestFsIndexListsWhatAnEditorWouldOpen(t *testing.T) {
	w := newFsWorld(t)
	app := pickerApp(w)
	fsNotebookRoot(t, app)

	plain := fsDir(t, "plain")
	for _, rel := range []string{"top.md", "nested/dir/deep.md", "nested/dir/deep2.txt", ".hidden-dir/inside.md", ".dotfile", "node_modules/pkg/index.js", ".git/objects/ab/cdef.md"} {
		fsWriteFile(t, filepath.Join(plain, rel), []byte("x"))
	}
	outside := filepath.Join(fsDir(t, "outside"), "target.md")
	fsWriteFile(t, outside, []byte("target"))
	if err := os.Symlink(outside, filepath.Join(plain, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(plain, "pipe.fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := fsDir(t, "repo")
	for rel, body := range map[string]string{".gitignore": "build/\n", "tracked.md": "x", "untracked.md": "x", "build/generated.md": "x", ".claude/notes.md": "x"} {
		fsWriteFile(t, filepath.Join(repo, rel), []byte(body))
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "add", "tracked.md", ".claude/notes.md")
	runGit(t, repo, "commit", "-m", "seed")

	for _, c := range []struct {
		name       string
		root       string
		extensions []string
		want       []string
	}{
		{"a plain directory", plain, nil, []string{".dotfile", ".hidden-dir/inside.md", "nested/dir/deep.md", "nested/dir/deep2.txt", "top.md"}},
		{"a git repository", repo, []string{"md"}, []string{".claude/notes.md", "tracked.md", "untracked.md"}},
	} {
		indexed := fsAskIndex(app, c.root, c.extensions...)
		if !indexed.Success || indexed.Root != c.root || indexed.Truncated || !slices.Equal(indexed.Files, c.want) {
			t.Errorf("indexing %s = success %v root %s truncated %v files %v (%s); want %v under %s",
				c.name, indexed.Success, indexed.Root, indexed.Truncated, indexed.Files, protocol.Deref(indexed.Error), c.want, c.root)
		}
	}

	missing := filepath.Join(plain, "does-not-exist")
	if refused := fsAskIndex(app, missing); refused.Success || !strings.Contains(protocol.Deref(refused.Error), missing) {
		t.Errorf("indexing a missing root = %+v (%s), want a refusal naming it", refused, protocol.Deref(refused.Error))
	}
	if refused := fsAskIndex(app, filepath.Join(plain, "top.md")); refused.Success {
		t.Errorf("indexing a file as a root = %+v, want a refusal", refused)
	}

	remote := fsRemotePeer(w)
	if refused := fsAskIndex(remote, plain); refused.Success || len(refused.Files) != 0 || !strings.Contains(protocol.Deref(refused.Error), "authenticated") {
		t.Errorf("an unauthenticated client indexed an explicit root: %v (%s)", refused.Files, protocol.Deref(refused.Error))
	}
	if omitted := fsAskIndex(remote, ""); !omitted.Success {
		t.Errorf("an unauthenticated client could not index the notebook: %s", protocol.Deref(omitted.Error))
	}
}

func TestFsIndexCapsAfterFilteringAndSaysWhenItTruncated(t *testing.T) {
	const indexCap = 25000
	w := newFsWorld(t)
	app := pickerApp(w)
	root := fsDir(t, "many")
	for i := range indexCap - 1 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%05d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"aa-early.MD", "zz-late.md"} {
		fsWriteFile(t, filepath.Join(root, name), nil)
	}

	if everything := fsAskIndex(app, root); !everything.Success || !everything.Truncated || len(everything.Files) != indexCap {
		t.Fatalf("indexing %d files = %d files, truncated %v (%s); want the first %d and truncated", indexCap+1, len(everything.Files), everything.Truncated, protocol.Deref(everything.Error), indexCap)
	}
	if markdown := fsAskIndex(app, root, ".MD"); !markdown.Success || markdown.Truncated || !slices.Equal(markdown.Files, []string{"aa-early.MD", "zz-late.md"}) {
		t.Fatalf("indexing .MD = %v, truncated %v (%s); want both markdown files whatever their case", markdown.Files, markdown.Truncated, protocol.Deref(markdown.Error))
	}
}
