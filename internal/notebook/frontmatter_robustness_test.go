package notebook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePermissiveKeepsBodyOfNoteWithRecursiveAlias(t *testing.T) {
	doc := ParsePermissive([]byte("---\na: &a [*a]\n---\nbody\n"))
	if doc.Body == "" {
		t.Fatal("body lost")
	}
}

func TestListSkipsUnreadableSubdirectory(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "b.md"), []byte("# b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if f, err := os.Open(locked); err == nil {
		f.Close()
		t.Skip("directory permissions are not enforced for this user")
	}
	entries, err := NewStore(root).List("")
	if err != nil || len(entries) != 1 {
		t.Fatalf("err=%v entries=%v", err, entries)
	}
}
