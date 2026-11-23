package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prompts"
)

func gitTest(t *testing.T, e *editor, args ...string) string {
	t.Helper()
	output, err := gitRead(context.Background(), e.repo, nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func commitTest(t *testing.T, e *editor) string {
	t.Helper()
	gitTest(t, e, "add", ".")
	gitTest(t, e, "-c", "user.name=Editor test", "-c", "user.email=editor@example.test", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "commit", "--no-gpg-sign", "-m", "Prompt fixture")
	return gitTest(t, e, "rev-parse", "HEAD")
}

func writeTest(t *testing.T, e *editor, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(e.root.Name(), path)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := e.root.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func manifestTest(t *testing.T, e *editor, recipients ...prompts.Recipient) {
	t.Helper()
	data, err := json.Marshal(prompts.Manifest{Version: prompts.ManifestVersion, Recipients: recipients})
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, e, prompts.ManifestPath, string(data))
}

func TestPageDiffsAgainstTheMergeBaseOfARef(t *testing.T) {
	e := testEditor(t)
	gitTest(t, e, "init", "-b", "next")
	path := "content/crew/v2/new-session.md"
	writeTest(t, e, path, "Wake from the base.\n")
	manifestTest(t, e, prompts.Builtin().Recipients()...)
	base := commitTest(t, e)
	gitTest(t, e, "checkout", "-b", "feature")
	writeTest(t, e, path, "Wake from the feature.\n")
	commitTest(t, e)
	gitTest(t, e, "checkout", "next")
	writeTest(t, e, path, "Wake from the moved base.\n")
	commitTest(t, e)
	gitTest(t, e, "checkout", "feature")
	writeTest(t, e, path, "Wake from disk.\n")
	beforeStatus := gitTest(t, e, "status", "--porcelain")
	w := request(t, e, "page", pageRequest{Event: "crew/new-session", Base: "next"})
	var page pageResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
		t.Fatalf("page: %d %s", w.Code, w.Body)
	}
	if page.BaseCommit != base || page.Base[path] != "Wake from the base.\n" || page.Sources[path].Text != "Wake from disk.\n" {
		t.Fatalf("page base: %s %q %q", page.BaseCommit, page.Base[path], page.Sources[path].Text)
	}
	if gitTest(t, e, "status", "--porcelain") != beforeStatus {
		t.Fatal("a diff changed the checkout")
	}
	for _, ref := range []string{"--output=/tmp/unwanted", "missing"} {
		if w := request(t, e, "page", pageRequest{Event: "crew/new-session", Base: ref}); w.Code != 422 {
			t.Fatalf("base %s: %d %s", ref, w.Code, w.Body)
		}
	}
}

func TestBaseModesPinCommitsAndReportMissingManifests(t *testing.T) {
	e := testEditor(t)
	gitTest(t, e, "init", "-b", "next")
	if err := e.root.Remove(prompts.ManifestPath); err != nil {
		t.Fatal(err)
	}
	ancestor := commitTest(t, e)
	gitTest(t, e, "checkout", "-b", "feature")
	writeTest(t, e, "content/crew/v2/new-session.md", "Feature\n")
	head := commitTest(t, e)
	gitTest(t, e, "checkout", "next")
	writeTest(t, e, "content/crew/v2/new-session.md", "New base tip\n")
	tip := commitTest(t, e)
	gitTest(t, e, "checkout", "feature")
	for mode, want := range map[string]string{"merge-base": ancestor, "tip": tip} {
		base, err := e.selectBase(context.Background(), "next", mode)
		if err != nil || base.Commit != want || base.Head != head || base.Tip != tip || !strings.Contains(base.Unavailable, "no catalog manifest") {
			t.Fatalf("%s: %+v / %v", mode, base, err)
		}
	}
	gitTest(t, e, "branch", "-f", "next", head)
	manifestTest(t, e, prompts.Builtin().Recipients()...)
	base, err := e.readBase(context.Background(), tip)
	if err != nil || base.Sources["content/crew/v2/new-session.md"].Text != "New base tip\n" {
		t.Fatalf("moving ref changed pinned revision: %+v / %v", base, err)
	}
}

func TestBaseFailuresAreExplicitAndCannotReadOutsideSources(t *testing.T) {
	e := testEditor(t)
	gitTest(t, e, "init", "-b", "next")
	writeTest(t, e, prompts.ManifestPath, `{"version":999,"recipients":[]}`)
	commit := commitTest(t, e)
	base, err := e.readBase(context.Background(), commit)
	if err != nil || !strings.Contains(base.Unavailable, "unsupported catalog version") {
		t.Fatalf("version: %+v / %v", base, err)
	}
	if _, err := e.selectBase(context.Background(), "next", "unknown"); err == nil {
		t.Fatal("an unknown comparison mode was accepted")
	}
	writeTest(t, e, prompts.ManifestPath, `{"version":1,"recipients":[{"id":"broken","events":[{"id":"wake","delivery":"user_message","body":{"kind":"text","id":"missing","source":"content/missing.md"}}]}]}`)
	commit = commitTest(t, e)
	base, err = e.readBase(context.Background(), commit)
	if err != nil || !strings.Contains(base.Unavailable, "missing.md") {
		t.Fatalf("missing source: %+v / %v", base, err)
	}
}

func TestUnifiedDiffPreservesWhitespaceAndFinalNewline(t *testing.T) {
	for _, change := range [][2]string{{"hello\n", "hello"}, {"a\nb\n", "a\n b\n"}, {"", "new\n"}, {"old\n", ""}} {
		patch, err := unifiedDiff(context.Background(), change[0], change[1])
		if err != nil || !strings.Contains(patch, "@@") {
			t.Fatalf("diff: %q / %v", patch, err)
		}
		if change[1] == "hello" && !strings.Contains(patch, `\ No newline at end of file`) {
			t.Fatal("lost final newline difference")
		}
	}
}
