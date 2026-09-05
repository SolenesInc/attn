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
	gitTest(t, e, "-c", "user.name=Editor test", "-c", "user.email=editor@example.test", "commit", "--no-gpg-sign", "-m", "Prompt fixture")
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

func TestComparisonUsesEachRevisionsCompositionAndDrafts(t *testing.T) {
	e := testEditor(t)
	gitTest(t, e, "init", "-b", "next")
	path := "content/crew/wake.md"
	writeTest(t, e, path, "Wake from the base.\n")
	writeTest(t, e, "content/removed.md", "A retired event.\n")
	flag := prompts.FlagField("old_flag", "Only the base has this input")
	manifestTest(t, e, prompts.Recipient{ID: "crew", Events: []prompts.Event{
		prompts.On("wake", "launch_instructions", "Old composition", prompts.Compose(
			prompts.When(prompts.Enabled(flag), prompts.Use("old.wake", path)),
			prompts.Input(prompts.TextField("base_suffix", "A base-only input")))),
		prompts.On("retired", "user_message", "Removed", prompts.Document("retired", "content/removed.md")),
	}})
	base := commitTest(t, e)
	manifestTest(t, e, prompts.Builtin().Recipients()...)
	writeTest(t, e, path, "Wake from disk.\n")
	beforeStatus := gitTest(t, e, "status", "--porcelain")
	w := request(t, e, "base", editRequest{Ref: "next", Mode: "merge-base"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), base) {
		t.Fatalf("base: %d %s", w.Code, w.Body)
	}
	body := editRequest{BaseCommit: base, Recipient: "crew", Event: "wake", Path: path,
		Values: prompts.Values{"old_flag": "true", "base_suffix": "Base tail", "current_only": "ignored"},
		Drafts: map[string]string{path: "Wake from the unsaved draft.\n"}}
	w = request(t, e, "compare", body)
	var comparison comparison
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &comparison) != nil {
		t.Fatalf("compare: %d %s", w.Code, w.Body)
	}
	if comparison.Base.Result.Text != "Wake from the base.\n\nBase tail" || comparison.Current.Result.Text != "Wake from the unsaved draft." {
		t.Fatalf("rendered with wrong declarations: %+v / %+v", comparison.Base, comparison.Current)
	}
	if comparison.Base.Result.Delivery != "launch_instructions" || !strings.Contains(comparison.SourceDiff, "+Wake from the unsaved draft.") || !strings.Contains(comparison.PromptDiff, "-Base tail") {
		t.Fatalf("missing comparison details: %+v", comparison)
	}
	body.Values["old_flag"] = "false"
	w = request(t, e, "compare", body)
	if err := json.Unmarshal(w.Body.Bytes(), &comparison); err != nil || comparison.Base.Result.Text != "Base tail" {
		t.Fatalf("old condition not respected: %s", w.Body)
	}
	body.Event, body.Path = "retired", "content/removed.md"
	w = request(t, e, "compare", body)
	if !strings.Contains(w.Body.String(), `"status":"absent"`) || !strings.Contains(w.Body.String(), "-A retired event.") {
		t.Fatalf("removed event: %s", w.Body)
	}
	body.Event, body.Path = "heartbeat", "content/crew/heartbeat.md"
	w = request(t, e, "compare", body)
	if !strings.Contains(w.Body.String(), `"base":{"status":"absent"}`) {
		t.Fatalf("added event: %s", w.Body)
	}
	body.Event, body.Path = "wake", path
	body.Drafts[path] = "{{invalid}}"
	w = request(t, e, "compare", body)
	if !strings.Contains(w.Body.String(), `"current":{"status":"invalid"`) || !strings.Contains(w.Body.String(), "+{{invalid}}") {
		t.Fatalf("invalid draft hid source diff: %s", w.Body)
	}
	if gitTest(t, e, "status", "--porcelain") != beforeStatus || gitTest(t, e, "rev-parse", "HEAD") != base {
		t.Fatal("comparison changed the checkout")
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
	writeTest(t, e, "content/crew/wake.md", "Feature\n")
	head := commitTest(t, e)
	gitTest(t, e, "checkout", "next")
	writeTest(t, e, "content/crew/wake.md", "New base tip\n")
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
	if err != nil || base.Sources["content/crew/wake.md"].Text != "New base tip\n" {
		t.Fatalf("moving ref changed pinned revision: %+v / %v", base, err)
	}
	w := request(t, e, "compare", editRequest{BaseCommit: ancestor, Recipient: "crew", Event: "wake", Path: "content/crew/wake.md"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"unavailable"`) || !strings.Contains(w.Body.String(), "+Feature") {
		t.Fatalf("legacy source comparison: %d %s", w.Code, w.Body)
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
	for _, body := range []editRequest{{Ref: "--output=/tmp/unwanted", Mode: "tip"}, {Ref: "missing", Mode: "tip"}, {Ref: "next", Mode: "unknown"}} {
		w := request(t, e, "base", body)
		if w.Code != 422 {
			t.Fatalf("invalid base: %d %s", w.Code, w.Body)
		}
	}
	for _, body := range []editRequest{{BaseCommit: "HEAD"}, {BaseCommit: commit, Path: "../../go.mod"}} {
		w := request(t, e, "compare", body)
		if w.Code != 422 {
			t.Fatalf("invalid compare: %d %s", w.Code, w.Body)
		}
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
