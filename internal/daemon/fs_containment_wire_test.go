package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestFsCommandsNeverReachPastTheirRoot(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	root := fsNotebookRoot(t, w)
	outside := fsDir(t, "outside")
	secret := filepath.Join(outside, "secret.txt")
	fsWriteFile(t, secret, []byte("top secret"))
	fsWriteFile(t, filepath.Join(root, "a.txt"), []byte("aaa"))
	fsWriteFile(t, filepath.Join(root, "b.md"), []byte("bb"))
	fsWriteFile(t, filepath.Join(root, ".hidden"), []byte("x"))
	fsWriteFile(t, filepath.Join(root, "sub", "deep.md"), []byte("deep"))
	for link, target := range map[string]string{"leak.txt": secret, "linkdir": outside} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}

	listed := fsAskList(app, "", "")
	var names []string
	for _, entry := range listed.Entries {
		names = append(names, entry.Name)
	}
	if !listed.Success || !slices.Equal(names, []string{"crew", "journal", "knowledge", "sub", "a.txt", "b.md", "index.md", "log.md"}) {
		t.Errorf("listing the root = %v (%s), want the directories, the Chief's among them, then the files by name, without dotfiles or links leaving the root", names, protocol.Deref(listed.Error))
	}
	for _, path := range []string{"leak.txt", "linkdir/secret.txt", "../outside/secret.txt"} {
		if read := fsAskRead(app, "", path); read.Success || (read.Result != nil && strings.Contains(read.Result.Content, "top secret")) {
			t.Errorf("reading %s = %+v, want a refusal", path, read.Result)
		}
	}
	if written := fsAskWrite(app, "", "linkdir/planted.txt", "planted", ""); written.Success {
		t.Errorf("writing through a link to outside the root = %+v, want a refusal", written.Result)
	}
	fsAskWrite(app, "", "../planted.txt", "planted", "")
	for _, escaped := range []string{filepath.Join(outside, "planted.txt"), filepath.Join(filepath.Dir(root), "planted.txt")} {
		if _, err := os.Stat(escaped); err == nil {
			t.Errorf("a command wrote %s, outside the root", escaped)
		}
	}
	if content, err := os.ReadFile(secret); err != nil || string(content) != "top secret" {
		t.Errorf("the file outside the root now reads %q (%v)", content, err)
	}
}
