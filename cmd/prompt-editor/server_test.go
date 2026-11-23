package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
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

func request(t *testing.T, e *editor, route string, body any) *httptest.ResponseRecorder {
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

func saveFiles(t *testing.T, e *editor, files ...saveFile) (int, saveResponse) {
	t.Helper()
	w := request(t, e, "save", saveRequest{Files: files})
	var answer saveResponse
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, answer
}

func TestSaveWritesEveryFileAndSkipsConflicts(t *testing.T) {
	e := testEditor(t)
	first, second := "content/crew/v2/new-session.md", "content/delegation/opening.md"
	firstDisk, _ := e.root.ReadFile(first)
	secondDisk, _ := e.root.ReadFile(second)
	firstText, secondText := string(firstDisk)+"\nA maintainer edit.\n", string(secondDisk)+"\nAnother edit.\n"
	code, answer := saveFiles(t, e,
		saveFile{first, firstText, revision(firstDisk)},
		saveFile{second, secondText, revision(secondDisk)})
	if code != 200 || answer.Saved[first] != revision([]byte(firstText)) || answer.Saved[second] != revision([]byte(secondText)) || len(answer.Conflicts) != 0 {
		t.Fatalf("save both: %d %+v", code, answer)
	}
	for path, text := range map[string]string{first: firstText, second: secondText} {
		if disk, _ := e.root.ReadFile(path); string(disk) != text {
			t.Fatalf("%s on disk is %q", path, disk)
		}
	}

	agentText := firstText + "\nAn agent's edit.\n"
	if err := e.root.WriteFile(first, []byte(agentText), 0644); err != nil {
		t.Fatal(err)
	}
	code, answer = saveFiles(t, e,
		saveFile{first, firstText + "\nStale page edit.\n", revision([]byte(firstText))},
		saveFile{second, secondText + "\nFresh page edit.\n", revision([]byte(secondText))})
	if code != 200 || answer.Conflicts[first] != (source{agentText, revision([]byte(agentText))}) || answer.Saved[second] == "" {
		t.Fatalf("save with conflict: %d %+v", code, answer)
	}
	if disk, _ := e.root.ReadFile(first); string(disk) != agentText {
		t.Fatal("a conflicting save overwrote the agent's edit")
	}
	if disk, _ := e.root.ReadFile(second); string(disk) != secondText+"\nFresh page edit.\n" {
		t.Fatal("a conflict in one file kept the other from saving")
	}

	secondNow, _ := e.root.ReadFile(second)
	code, _ = saveFiles(t, e,
		saveFile{first, agentText + "\nValid.\n", revision([]byte(agentText))},
		saveFile{second, "{{undeclared}}", revision(secondNow)})
	if code != 422 {
		t.Fatalf("invalid save: %d", code)
	}
	if disk, _ := e.root.ReadFile(first); string(disk) != agentText {
		t.Fatal("an invalid catalog saved a valid file beside it")
	}
}

func TestInvalidAndUnregisteredWritesLeaveFilesAlone(t *testing.T) {
	e := testEditor(t)
	path := "content/crew/v2/new-session.md"
	original, _ := e.root.ReadFile(path)
	for _, test := range []struct {
		file   saveFile
		status int
	}{
		{saveFile{path, "{{undeclared}}", revision(original)}, 422},
		{saveFile{"../outside.md", "escape", ""}, 400},
		{saveFile{"content/new.md", "unregistered", ""}, 400},
	} {
		if code, _ := saveFiles(t, e, test.file); code != test.status {
			t.Fatalf("save %s: %d", test.file.Path, code)
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
	if code, _ := saveFiles(t, e, saveFile{path, "symlink", ""}); code != 400 {
		t.Fatalf("symlink save: %d", code)
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
	if err := e.root.WriteFile("content/crew/v2/new-session.md", []byte("{{oops}}"), 0644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:1234/api/catalog", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "missing bindings") {
		t.Fatalf("catalog: %d %s", w.Code, w.Body)
	}
}

func TestSecondEditorPrintsTheRunningEditorAndLeavesItServing(t *testing.T) {
	e := testEditor(t)
	ctx, stop := context.WithCancel(t.Context())
	output, writer := io.Pipe()
	served := make(chan int)
	go func() {
		served <- runCLI(ctx, []string{"--repo", e.repo, "serve"}, nil, writer, io.Discard)
		writer.Close()
	}()
	lines := bufio.NewScanner(output)
	if !lines.Scan() {
		t.Fatal("the first editor printed nothing")
	}
	address, ok := strings.CutPrefix(lines.Text(), "Prompt editor: ")
	if !ok {
		t.Fatalf("first editor: %q", lines.Text())
	}
	go func() {
		for lines.Scan() {
		}
	}()

	secondOutput, secondWriter := io.Pipe()
	secondDone := make(chan int, 1)
	go func() {
		secondDone <- runCLI(ctx, []string{"--repo", e.repo, "serve"}, nil, secondWriter, io.Discard)
		secondWriter.Close()
	}()
	second := bufio.NewScanner(secondOutput)
	second.Scan()
	if second.Text() != "Prompt editor already running: "+address+"/" {
		stop()
		t.Fatalf("second editor: %q", second.Text())
	}
	for second.Scan() {
	}
	if code := <-secondDone; code != 0 {
		t.Fatalf("second editor exited %d", code)
	}

	path := "content/crew/v2/new-session.md"
	disk, _ := e.root.ReadFile(path)
	text := string(disk) + "\nSaved through the running editor.\n"
	body, _ := json.Marshal(saveRequest{Files: []saveFile{{path, text, revision(disk)}}})
	save, _ := http.NewRequest("POST", address+"/api/save", bytes.NewReader(body))
	save.Header.Set("X-Prompt-Editor", "1")
	save.Header.Set("Origin", address)
	response, err := http.DefaultClient.Do(save)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var answer saveResponse
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&answer) != nil || answer.Saved[path] != revision([]byte(text)) {
		t.Fatalf("save through the running editor: %d %+v", response.StatusCode, answer)
	}
	if now, _ := e.root.ReadFile(path); string(now) != text {
		t.Fatalf("%s on disk is %q", path, now)
	}

	stop()
	if code := <-served; code != 0 {
		t.Fatalf("first editor exited %d", code)
	}
}
