package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func fsIndex(t *testing.T, d *Daemon, client *wsClient, requestID, root string, extensions ...string) protocol.FsIndexResultMessage {
	t.Helper()
	d.handleFsIndex(client, requestID, root, extensions)
	var res protocol.FsIndexResultMessage
	readNotebookWSEvent(t, client.send, &res)
	return res
}

func TestFsIndexListsDotFilesButNotGitOrNodeModules(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()

	mustWrite := func(rel string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("top.md")
	mustWrite("nested/dir/deep.md")
	mustWrite("nested/dir/deep2.txt")
	mustWrite(".hidden-dir/inside.md")
	mustWrite(".dotfile")
	mustWrite("node_modules/pkg/index.js")
	mustWrite(".git/objects/ab/cdef.md")

	symlinkTarget := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(symlinkTarget, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(symlinkTarget, filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}

	// A FIFO: not a symlink, but fs_read cannot open it either.
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	client := trustedFsClient(4)
	res := fsIndex(t, d, client, "i1", root)
	if !res.Success {
		t.Fatalf("fs_index(root) failed: %v", res.Error)
	}
	if res.Root != root {
		t.Fatalf("fs_index.root = %q, want %q", res.Root, root)
	}
	if res.Truncated {
		t.Fatalf("fs_index.truncated = true, want false")
	}
	want := []string{".dotfile", ".hidden-dir/inside.md", "nested/dir/deep.md", "nested/dir/deep2.txt", "top.md"}
	if !equalStrings(res.Files, want) {
		t.Fatalf("fs_index.files = %v, want %v", res.Files, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIndexRootTruncatesAtInjectedCap(t *testing.T) {
	root := t.TempDir()
	const total = 12
	const cap = 5
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("file%02d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, truncated, err := indexRoot(root, cap, nil)
	if err != nil {
		t.Fatalf("indexRoot: %v", err)
	}
	if !truncated {
		t.Fatalf("truncated = false, want true (total=%d cap=%d)", total, cap)
	}
	if len(files) != cap {
		t.Fatalf("len(files) = %d, want %d", len(files), cap)
	}
}

// resolveFsRoot only canonicalizes the deepest existing ancestor and WalkDir's
// error-recovery branch swallows the error: the walk "succeeds" with zero files.
func TestFsIndexRejectsNonexistentAndNonDirectoryRoots(t *testing.T) {
	d := newFsDaemon(t)
	base := t.TempDir()

	client := trustedFsClient(4)
	missing := filepath.Join(base, "does-not-exist")
	res := fsIndex(t, d, client, "i1", missing)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_index(nonexistent root) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, missing) {
		t.Fatalf("fs_index(nonexistent root) error = %q, want it to mention the root %q", *res.Error, missing)
	}

	regularFile := filepath.Join(base, "not-a-dir.txt")
	if err := os.WriteFile(regularFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileClient := trustedFsClient(4)
	fileRes := fsIndex(t, d, fileClient, "i2", regularFile)
	if fileRes.Success {
		t.Fatalf("fs_index(root = regular file) = %+v, want failure", fileRes)
	}
}

func TestFsIndexWithExplicitRootDeniedForUntrustedClient(t *testing.T) {
	d := newFsDaemon(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	untrusted := &wsClient{send: make(chan outboundMessage, 8)}
	res := fsIndex(t, d, untrusted, "i1", root)
	if res.Success || res.Error == nil {
		t.Fatalf("fs_index(explicit root, untrusted client) = %+v, want failure", res)
	}
	if !strings.Contains(*res.Error, "authenticated") {
		t.Fatalf("fs_index(explicit root, untrusted client) error = %q, want it to mention the authenticated app", *res.Error)
	}
	if len(res.Files) != 0 {
		t.Fatalf("fs_index(explicit root, untrusted client) files = %v, want empty", res.Files)
	}

	omittedRes := fsIndex(t, d, untrusted, "i2", "")
	if !omittedRes.Success {
		t.Fatalf("fs_index(omitted root, untrusted client) = %+v, want success", omittedRes)
	}
}

// The cap counts only files that survive the extension filter: capping before it
// truncates a large repository on files nobody asked for.
func TestIndexRootAppliesCapAfterExtensionFilter(t *testing.T) {
	root := t.TempDir()
	for i := range 40 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("noise%02d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"zz-late.md", "aa-early.MD"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, truncated, err := indexRoot(root, 5, []string{".MD"})
	if err != nil {
		t.Fatalf("indexRoot: %v", err)
	}
	if truncated {
		t.Fatalf("truncated = true, want false: only 2 markdown files exist")
	}
	if want := []string{"aa-early.MD", "zz-late.md"}; !slicesEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestIndexRootUsesGitAndHonorsGitignore(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(".gitignore", "build/\n")
	mustWrite("tracked.md", "x")
	mustWrite("untracked.md", "x")
	mustWrite("build/generated.md", "x")
	mustWrite(".claude/notes.md", "x")

	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "tracked.md", ".claude/notes.md"},
		{"commit", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	files, truncated, err := indexRoot(root, maxFsIndexEntries, []string{"md"})
	if err != nil {
		t.Fatalf("indexRoot: %v", err)
	}
	if truncated {
		t.Fatal("truncated = true, want false")
	}
	if want := []string{".claude/notes.md", "tracked.md", "untracked.md"}; !slicesEqual(files, want) {
		t.Fatalf("files = %v, want %v (gitignored paths excluded, dot-dirs kept)", files, want)
	}
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
