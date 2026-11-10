package review

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/git"
	"golang.org/x/tools/txtar"
)

var update = flag.Bool("update", false, "rewrite each corpus case's bundle.json from the bundle it resolves to")

// Each case in testdata/corpus is a txtar archive:
//
//	base/<path>    committed as base
//	head/<path>    the working tree when review start runs; absent, head is base
//	now/<path>     written after review start; absent, now is head
//	now-deleted    paths removed after review start, one per line
//	options        "tour" starts without a base; "commit after head" commits the working tree after start
//	review.md      the document, written before review start
//	bundle.json    the expected bundle, with base, head and repository masked
func TestCorpus(t *testing.T) {
	cases, err := filepath.Glob("testdata/corpus/*.txtar")
	if err != nil || len(cases) == 0 {
		t.Fatalf("no corpus cases: %v", err)
	}
	for _, path := range cases {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".txtar"), func(t *testing.T) {
			archive, err := txtar.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := resolveCase(t, archive)
			if *update {
				writeSection(archive, "bundle.json", got)
				if err := os.WriteFile(path, txtar.Format(archive), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want := section(archive, "bundle.json")
			if string(got) != string(want) {
				t.Errorf("bundle differs; rerun with -update to see the change in git diff.\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func resolveCase(t *testing.T, archive *txtar.Archive) []byte {
	t.Helper()
	ctx := context.Background()
	client := git.NewClient()
	repo := filepath.Join(t.TempDir(), "repo")
	document := filepath.Join(t.TempDir(), "review.md")
	runGit(t, repo, "init", "-q", "-b", "main", repo)
	base, head, now := files(archive, "base/"), files(archive, "head/"), files(archive, "now/")
	writeFiles(t, repo, base)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	if len(head) > 0 {
		for path := range base {
			if _, kept := head[path]; !kept {
				os.Remove(filepath.Join(repo, path))
			}
		}
		writeFiles(t, repo, head)
	}
	if err := os.WriteFile(document, section(archive, "review.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	options := string(section(archive, "options"))
	baseArg := "HEAD"
	if strings.Contains(options, "tour") {
		baseArg = ""
	}
	if _, err := Start(ctx, client, document, repo, baseArg); err != nil {
		t.Fatalf("review start: %v", err)
	}
	if strings.Contains(options, "commit after head") {
		runGit(t, repo, "add", "-A")
		runGit(t, repo, "commit", "-q", "-m", "after head")
	}
	writeFiles(t, repo, now)
	for _, path := range strings.Fields(string(section(archive, "now-deleted"))) {
		if err := os.Remove(filepath.Join(repo, path)); err != nil {
			t.Fatal(err)
		}
	}
	text, err := os.ReadFile(document)
	if err != nil {
		t.Fatal(err)
	}
	bundle := Resolve(ctx, client, document, string(text))
	if bundle.Base != nil {
		bundle.Base = new(string)
		*bundle.Base = "<base>"
	}
	if bundle.Head != "" {
		bundle.Head = "<head>"
	}
	bundle.Repository = "<repository>"
	out, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if args[0] != "init" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func files(archive *txtar.Archive, prefix string) map[string][]byte {
	out := map[string][]byte{}
	for _, file := range archive.Files {
		if path, ok := strings.CutPrefix(file.Name, prefix); ok {
			out[path] = file.Data
		}
	}
	return out
}

func writeFiles(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for path, data := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func section(archive *txtar.Archive, name string) []byte {
	for _, file := range archive.Files {
		if file.Name == name {
			return file.Data
		}
	}
	return nil
}

func writeSection(archive *txtar.Archive, name string, data []byte) {
	for i := range archive.Files {
		if archive.Files[i].Name == name {
			archive.Files[i].Data = data
			return
		}
	}
	archive.Files = append(archive.Files, txtar.File{Name: name, Data: bytes.Clone(data)})
}
