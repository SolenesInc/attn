package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (repo *testRepository) commitMessage(paragraphs ...string) string {
	repo.t.Helper()
	repo.git("add", "-A")
	args := []string{"commit", "-q"}
	for _, paragraph := range paragraphs {
		args = append(args, "-m", paragraph)
	}
	repo.git(args...)
	return repo.git("rev-parse", "HEAD")
}

func (repo *testRepository) releaseTrain(args ...string) string {
	repo.t.Helper()
	repo.t.Chdir(repo.root)
	var out strings.Builder
	if err := run(args, &out); err != nil {
		repo.t.Fatalf("release-train %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

func fakeAttn(t *testing.T, seeds map[string]string) {
	t.Helper()
	bin := t.TempDir()
	records := t.TempDir()
	for id, record := range seeds {
		if err := os.WriteFile(filepath.Join(records, id+".json"), []byte(record), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
record="` + records + `/$3.json"
if [ "$1 $2 $4" = "seed show --json" ] && [ -f "$record" ]; then
  cat "$record"
  exit 0
fi
echo "no seed $3" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "attn"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestNotesGroupTheCommitsSinceTheLastReleaseByTheirSeeds(t *testing.T) {
	repo := newTestRepository(t)
	repo.write("shipped.txt", "already released\n")
	previousMain := repo.commit("feat: shipped in the previous release")
	repo.writeManifest(candidateManifest{Version: "0.11.1", MainSHA: previousMain})
	repo.commit("chore(release): prepare v0.11.1")

	repo.write("band.txt", "one\n")
	repo.commitMessage("feat(queue): first slice of the band", "Attn-Session: s1\nAttn-Seed: seed-band")
	repo.write("band.txt", "two\n")
	repo.commitMessage("feat(queue): second slice of the band", "Wires the band into the queue.", "Attn-Session: s2\nAttn-Seed: seed-band\nAttn-Seed: seed-palette")
	repo.write("gone.txt", "fix\n")
	repo.commitMessage("fix(palette): keep the selection", "Attn-Seed: seed-gone")
	repo.write("changelog.d/tidy.yaml", "kind: fixed\narea: queue\nchange: The queue keeps its order.\n")
	head := repo.commitMessage("fix(queue): keep the order")

	fakeAttn(t, map[string]string{
		"seed-band":    `{"seed":{"title":"Queue band","body":"Show who is waiting.","status":"harvested"},"notes":[{"kind":"note","body":"Users asked for it."}]}`,
		"seed-palette": `{"seed":{"title":"Palette","body":"","status":"growing"},"notes":[]}`,
	})

	if got, want := repo.releaseTrain("notes", "range"), "<!-- changelog-commits: "+previousMain+".."+head+" -->\n"; got != want {
		t.Fatalf("range = %q, want %q", got, want)
	}
	rendered := repo.releaseTrain("notes", "render")

	groups := strings.Split(rendered, "\n=== ")
	if !strings.HasPrefix(groups[0], "4 commits since the last release") {
		t.Fatalf("header = %q", groups[0])
	}
	want := []struct {
		heading string
		holds   []string
	}{
		{"seed seed-band: Queue band [harvested]", []string{"Show who is waiting.", "--- note: Users asked for it.", "first slice of the band", "second slice of the band", "Wires the band into the queue."}},
		{"seed seed-palette: Palette [growing]", []string{"second slice of the band"}},
		{"seed seed-gone: unknown (no seed seed-gone)", []string{"fix(palette): keep the selection"}},
		{"commits without a seed", []string{"fix(queue): keep the order", "release note the author wrote (changelog.d/tidy.yaml)", "The queue keeps its order."}},
	}
	if len(groups) != len(want)+1 {
		t.Fatalf("got %d groups, want %d:\n%s", len(groups)-1, len(want), rendered)
	}
	for i, group := range want {
		got := groups[i+1]
		if !strings.HasPrefix(got, group.heading) {
			t.Errorf("group %d starts %q, want %q", i, firstLine(got), group.heading)
		}
		for _, text := range group.holds {
			if !strings.Contains(got, text) {
				t.Errorf("group %q lacks %q:\n%s", group.heading, text, got)
			}
		}
	}
	for _, released := range []string{"shipped in the previous release", "prepare v0.11.1"} {
		if strings.Contains(rendered, released) {
			t.Errorf("notes include %q, which is not part of this release", released)
		}
	}
}
