package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/victorarias/attn/internal/prompts"
)

func testEditor(t *testing.T) *editor {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".prompt-editor/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, "internal/prompts")
	err := fs.WalkDir(prompts.Files(), "content", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(path, 0755)
		}
		data, err := fs.ReadFile(prompts.Files(), name)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	e := newEditor(root, fstest.MapFS{}, "127.0.0.1:1234")
	e.repo = repo
	manifest, err := json.Marshal(prompts.Builtin().Manifest())
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(prompts.ManifestPath, manifest, 0644); err != nil {
		t.Fatal(err)
	}
	return e
}

func request(t *testing.T, e *editor, route string, body editRequest) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:1234/api/"+route, bytes.NewReader(data))
	r.Header.Set("X-Prompt-Editor", "1")
	r.Header.Set("Origin", "http://127.0.0.1:1234")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

func TestPreviewDraftSaveAndConflict(t *testing.T) {
	e := testEditor(t)
	path := "content/crew/wake.md"
	original, _ := e.root.ReadFile(path)
	draft := string(original) + "\nA maintainer edit.\n"
	w := request(t, e, "preview", editRequest{Recipient: "crew", Event: "wake", Drafts: map[string]string{path: draft}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "A maintainer edit.") {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	disk, _ := e.root.ReadFile(path)
	if !bytes.Equal(disk, original) {
		t.Fatal("preview wrote a file")
	}
	w = request(t, e, "save", editRequest{Path: path, Text: draft, Revision: revision(original)})
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	disk, _ = e.root.ReadFile(path)
	if string(disk) != draft {
		t.Fatal("save lost text or whitespace")
	}
	w = request(t, e, "save", editRequest{Path: path, Text: "stale edit", Revision: revision(original)})
	if w.Code != 409 {
		t.Fatalf("stale save: %d %s", w.Code, w.Body)
	}
	disk, _ = e.root.ReadFile(path)
	if string(disk) != draft {
		t.Fatal("stale save overwrote the file")
	}
}

func TestInvalidAndUnregisteredWritesLeaveFilesAlone(t *testing.T) {
	e := testEditor(t)
	path := "content/crew/wake.md"
	original, _ := e.root.ReadFile(path)
	for _, test := range []struct {
		body   editRequest
		status int
	}{
		{editRequest{Path: path, Text: "{{undeclared}}", Revision: revision(original)}, 422},
		{editRequest{Path: "../outside.md", Text: "escape"}, 400},
		{editRequest{Path: "content/new.md", Text: "unregistered"}, 400},
	} {
		w := request(t, e, "save", test.body)
		if w.Code != test.status {
			t.Fatalf("save: %d %s", w.Code, w.Body)
		}
	}
	disk, _ := e.root.ReadFile(path)
	if !bytes.Equal(disk, original) {
		t.Fatal("rejected save changed disk")
	}
	if err := e.root.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := e.root.Symlink("successor.md", path); err != nil {
		t.Fatal(err)
	}
	w := request(t, e, "save", editRequest{Path: path, Text: "symlink"})
	if w.Code != 400 {
		t.Fatalf("symlink save: %d %s", w.Code, w.Body)
	}
}

func TestEditorRejectsOtherOriginsAndHosts(t *testing.T) {
	e := testEditor(t)
	for _, test := range []struct{ host, origin, header string }{
		{"127.0.0.1:1234", "https://other.example", "1"},
		{"other.example", "", "1"},
		{"127.0.0.1:1234", "", ""},
	} {
		r := httptest.NewRequest("POST", "http://"+test.host+"/api/save", strings.NewReader("{}"))
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-Prompt-Editor", test.header)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("origin check: %d", w.Code)
		}
	}
}

func TestCatalogAllowsRepairingInvalidMarkdown(t *testing.T) {
	e := testEditor(t)
	if err := e.root.WriteFile("content/crew/wake.md", []byte("{{oops}}"), 0644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:1234/api/catalog", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "missing bindings") {
		t.Fatalf("catalog: %d %s", w.Code, w.Body)
	}
}
