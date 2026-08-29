package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type testRepository struct {
	t    *testing.T
	root string
}

func newTestRepository(t *testing.T) *testRepository {
	t.Helper()
	repo := &testRepository{t: t, root: t.TempDir()}
	repo.git("init", "-q", "-b", "main")
	repo.git("config", "user.name", "Release Test")
	repo.git("config", "user.email", "release@example.com")
	repo.write("app/package.json", "{\n  \"name\": \"app\",\n  \"version\": \"0.11.1\"\n}\n")
	repo.write("app/pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	repo.write("app/src-tauri/tauri.conf.json", "{\n  \"productName\": \"attn\",\n  \"version\": \"0.11.1\"\n}\n")
	repo.write("app/src-tauri/Cargo.toml", "[package]\nname = \"app\"\nversion = \"0.11.1\"\n")
	repo.write("app/src-tauri/Cargo.lock", "version = 4\n\n[[package]]\nname = \"app\"\nversion = \"0.11.1\"\n")
	repo.write("CHANGELOG.md", "# Changelog\n\n## [2026-08-01]\n\n- Earlier release.\n")
	repo.write("changelog.d/README.md", "# fragments\n")
	repo.commit("baseline")
	return repo
}

func (repo *testRepository) git(args ...string) string {
	repo.t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo.root
	out, err := command.CombinedOutput()
	if err != nil {
		repo.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (repo *testRepository) write(path, content string) {
	repo.t.Helper()
	fullPath := filepath.Join(repo.root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		repo.t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		repo.t.Fatal(err)
	}
}

func (repo *testRepository) remove(path string) {
	repo.t.Helper()
	if err := os.Remove(filepath.Join(repo.root, path)); err != nil {
		repo.t.Fatal(err)
	}
}

func (repo *testRepository) exists(path string) bool {
	repo.t.Helper()
	_, err := os.Stat(filepath.Join(repo.root, path))
	return err == nil
}

func (repo *testRepository) commit(subject string) string {
	repo.t.Helper()
	repo.git("add", "-A")
	repo.git("commit", "-q", "-m", subject)
	return repo.git("rev-parse", "HEAD")
}

func (repo *testRepository) writeManifest(manifest candidateManifest) {
	repo.t.Helper()
	data := "version: " + manifest.Version + "\nkind: " + manifest.Kind + "\nsource_sha: " + manifest.SourceSHA + "\nmain_sha: " + manifest.MainSHA + "\n"
	repo.write(defaultManifestPath, data)
}

func (repo *testRepository) prepareCandidate(kind string) (mainSHA, sourceSHA, headSHA string) {
	repo.t.Helper()
	mainSHA = repo.git("rev-parse", "main")
	branch := "next"
	if kind == "hotfix" {
		branch = "hotfix/fix-release"
	}
	repo.git("switch", "-q", "-c", branch)
	repo.write("changelog.d/accepted.yaml", "kind: fixed\narea: release\nchange: accepted change\n")
	if kind == "hotfix" {
		repo.write("fix.txt", "the urgent fix\n")
	}
	sourceSHA = repo.commit("accepted source")
	repo.git("switch", "-q", "-c", "release/v0.12.0")
	receipt, err := fragmentReceipt(repo.root, sourceSHA)
	if err != nil {
		repo.t.Fatal(err)
	}
	if err := setVersions(repo.root, "0.12.0"); err != nil {
		repo.t.Fatal(err)
	}
	repo.remove("changelog.d/accepted.yaml")
	repo.write("CHANGELOG.md", "# Changelog\n\n## [2026-08-28]\n\n- Accepted change.\n\n"+receipt+"\n")
	repo.writeManifest(candidateManifest{Version: "0.12.0", Kind: kind, SourceSHA: sourceSHA, MainSHA: mainSHA})
	headSHA = repo.commit("prepare release")
	return mainSHA, sourceSHA, headSHA
}

func TestVersionSetUpdatesEverySource(t *testing.T) {
	repo := newTestRepository(t)
	if err := setVersions(repo.root, "0.12.0"); err != nil {
		t.Fatal(err)
	}
	if err := checkVersions(repo.root, "", "0.12.0"); err != nil {
		t.Fatal(err)
	}
	for _, source := range versionSources() {
		data, err := os.ReadFile(filepath.Join(repo.root, source.path))
		if err != nil {
			t.Fatal(err)
		}
		version, err := source.read(data)
		if err != nil || version != "0.12.0" {
			t.Fatalf("%s version = %q, %v", source.path, version, err)
		}
	}
}

func TestVersionSetRefusesAnExistingDisagreement(t *testing.T) {
	repo := newTestRepository(t)
	repo.write("app/package.json", "{\"version\": \"9.9.9\"}\n")
	err := setVersions(repo.root, "0.12.0")
	if err == nil || !strings.Contains(err.Error(), "inconsistent versions") {
		t.Fatalf("expected version disagreement, got %v", err)
	}
}

func TestManifestValidation(t *testing.T) {
	sha := strings.Repeat("a", 40)
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "promotion", body: "version: 1.2.3\nkind: promotion\nsource_sha: " + sha + "\nmain_sha: " + sha + "\n"},
		{name: "hotfix", body: "version: 1.2.3\nkind: hotfix\nsource_sha: " + sha + "\nmain_sha: " + sha + "\n"},
		{name: "unknown kind", body: "version: 1.2.3\nkind: patch\nsource_sha: " + sha + "\nmain_sha: " + sha + "\n", wantErr: "kind must be"},
		{name: "short sha", body: "version: 1.2.3\nkind: promotion\nsource_sha: abc123\nmain_sha: " + sha + "\n", wantErr: "full commit SHA"},
		{name: "unknown field", body: "version: 1.2.3\nkind: promotion\nsource_sha: " + sha + "\nmain_sha: " + sha + "\nbranch: next\n", wantErr: "field branch not found"},
		{name: "second document", body: "version: 1.2.3\nkind: promotion\nsource_sha: " + sha + "\nmain_sha: " + sha + "\n---\nversion: 1.2.4\n", wantErr: "more than one YAML document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "candidate.yml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := readManifest(path)
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestManifestWriteRecordsExactCommits(t *testing.T) {
	repo := newTestRepository(t)
	mainSHA := repo.git("rev-parse", "main")
	repo.git("switch", "-q", "-c", "next")
	repo.write("feature.txt", "accepted\n")
	sourceSHA := repo.commit("accepted source")
	var output strings.Builder
	err := runManifest(repo.root, []string{
		"write", "--version", "v0.12.0", "--kind", "promotion",
		"--source", "HEAD", "--main", "main",
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(filepath.Join(repo.root, defaultManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.12.0" || manifest.Kind != "promotion" || manifest.SourceSHA != sourceSHA || manifest.MainSHA != mainSHA {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestCandidateValidationAcceptsPromotionAndHotfix(t *testing.T) {
	for _, kind := range []string{"promotion", "hotfix"} {
		t.Run(kind, func(t *testing.T) {
			repo := newTestRepository(t)
			mainSHA, _, headSHA := repo.prepareCandidate(kind)
			acceptance := "success"
			if kind == "hotfix" {
				acceptance = ""
			}
			err := validateCandidate(repo.root, candidateValidation{
				manifestPath: defaultManifestPath, currentMainRef: mainSHA, headRef: headSHA,
				sourceAcceptance: acceptance, otherOpenCandidates: 0,
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCandidateValidationRejectsUnsafeState(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*testRepository, string, string)
		input   func(string, string) candidateValidation
		wantErr string
	}{
		{
			name: "missing acceptance",
			input: func(main, head string) candidateValidation {
				return validCandidateInput(main, head, "")
			},
			wantErr: "acceptance is missing",
		},
		{
			name: "red acceptance",
			input: func(main, head string) candidateValidation {
				return validCandidateInput(main, head, "failure")
			},
			wantErr: "expected success",
		},
		{
			name: "another candidate is open",
			input: func(main, head string) candidateValidation {
				input := validCandidateInput(main, head, "success")
				input.otherOpenCandidates = 1
				return input
			},
			wantErr: "other open release candidate",
		},
		{
			name: "main moved",
			mutate: func(repo *testRepository, _, _ string) {
				repo.git("switch", "-q", "main")
				repo.write("main-moved.txt", "new main\n")
				repo.commit("move main")
				repo.git("switch", "-q", "release/v0.12.0")
			},
			input: func(_ string, head string) candidateValidation {
				return validCandidateInput("main", head, "success")
			},
			wantErr: "main moved",
		},
		{
			name: "tag exists",
			mutate: func(repo *testRepository, _, head string) {
				repo.git("tag", "v0.12.0", head)
			},
			input: func(main, head string) candidateValidation {
				return validCandidateInput(main, head, "success")
			},
			wantErr: "already exists",
		},
		{
			name: "version disagreement",
			mutate: func(repo *testRepository, _, _ string) {
				repo.write("app/package.json", "{\"version\": \"0.12.1\"}\n")
				repo.commit("mismatch version")
			},
			input: func(main, _ string) candidateValidation {
				return validCandidateInput(main, "HEAD", "success")
			},
			wantErr: "expected 0.12.0",
		},
		{
			name: "non-release change after acceptance",
			mutate: func(repo *testRepository, _, _ string) {
				repo.write("surprise.txt", "late change\n")
				repo.commit("late code change")
			},
			input: func(main, _ string) candidateValidation {
				return validCandidateInput(main, "HEAD", "success")
			},
			wantErr: "non-release file",
		},
		{
			name: "user-facing fragments dropped without changelog",
			mutate: func(repo *testRepository, _, _ string) {
				manifest, err := readManifest(filepath.Join(repo.root, defaultManifestPath))
				if err != nil {
					repo.t.Fatal(err)
				}
				data, err := readRepositoryFile(repo.root, manifest.SourceSHA, "CHANGELOG.md")
				if err != nil {
					repo.t.Fatal(err)
				}
				repo.write("CHANGELOG.md", string(data))
				repo.commit("drop release notes")
			},
			input: func(main, _ string) candidateValidation {
				return validCandidateInput(main, "HEAD", "success")
			},
			wantErr: "fragments were removed without updating CHANGELOG.md",
		},
		{
			name: "unrelated changelog edit does not compile fragments",
			mutate: func(repo *testRepository, _, _ string) {
				manifest, err := readManifest(filepath.Join(repo.root, defaultManifestPath))
				if err != nil {
					repo.t.Fatal(err)
				}
				data, err := readRepositoryFile(repo.root, manifest.SourceSHA, "CHANGELOG.md")
				if err != nil {
					repo.t.Fatal(err)
				}
				repo.write("CHANGELOG.md", string(data)+"\nUnrelated cleanup.\n")
				repo.commit("edit changelog without compiling fragments")
			},
			input: func(main, _ string) candidateValidation {
				return validCandidateInput(main, "HEAD", "success")
			},
			wantErr: "does not contain the frozen fragment receipt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepository(t)
			mainSHA, _, headSHA := repo.prepareCandidate("promotion")
			if tc.mutate != nil {
				tc.mutate(repo, mainSHA, headSHA)
			}
			err := validateCandidate(repo.root, tc.input(mainSHA, headSHA))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCandidateValidationAllowsInternalFragmentsWithoutChangelogUpdate(t *testing.T) {
	repo := newTestRepository(t)
	mainSHA := repo.git("rev-parse", "main")
	repo.git("switch", "-q", "-c", "next")
	repo.write("changelog.d/internal.yaml", "kind: internal\narea: release\nchange: internal change\n")
	sourceSHA := repo.commit("accepted internal source")
	repo.git("switch", "-q", "-c", "release/v0.12.0")
	if err := setVersions(repo.root, "0.12.0"); err != nil {
		t.Fatal(err)
	}
	repo.remove("changelog.d/internal.yaml")
	repo.writeManifest(candidateManifest{Version: "0.12.0", Kind: "promotion", SourceSHA: sourceSHA, MainSHA: mainSHA})
	headSHA := repo.commit("prepare internal release")

	if err := validateCandidate(repo.root, validCandidateInput(mainSHA, headSHA, "success")); err != nil {
		t.Fatal(err)
	}
}

func validCandidateInput(main, head, acceptance string) candidateValidation {
	return candidateValidation{
		manifestPath: defaultManifestPath, currentMainRef: main, headRef: head,
		sourceAcceptance: acceptance, otherOpenCandidates: 0,
	}
}

func TestAcceptedMainValidationSurvivesSquashAndRepair(t *testing.T) {
	repo := newTestRepository(t)
	_, sourceSHA, _ := repo.prepareCandidate("promotion")
	repo.git("switch", "-q", "main")
	repo.git("merge", "--squash", "release/v0.12.0")
	mainSHA := repo.commit("release: accept v0.12.0")

	command := exec.Command("git", "merge-base", "--is-ancestor", sourceSHA, mainSHA)
	command.Dir = repo.root
	if command.Run() == nil {
		t.Fatal("test setup retained source ancestry across a squash merge")
	}
	manifest, err := validateAcceptedMain(repo.root, mainSHA, defaultManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.12.0" {
		t.Fatalf("version = %q", manifest.Version)
	}

	repo.write("repair.txt", "repair the accepted main tree\n")
	repairedSHA := repo.commit("fix(release): repair accepted main")
	if _, err := validateAcceptedMain(repo.root, repairedSHA, defaultManifestPath); err != nil {
		t.Fatalf("repaired main: %v", err)
	}
}

func TestAcceptedMainValidationRejectsUnsafeState(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*testRepository)
		wantErr string
	}{
		{
			name: "version disagreement",
			mutate: func(repo *testRepository) {
				repo.write("app/package.json", "{\"version\": \"0.12.1\"}\n")
			},
			wantErr: "expected 0.12.0",
		},
		{
			name: "pending fragment",
			mutate: func(repo *testRepository) {
				repo.write("changelog.d/late.yaml", "kind: fixed\narea: release\nchange: late repair\n")
			},
			wantErr: "pending changelog fragments",
		},
		{
			name: "unrelated recorded main",
			mutate: func(repo *testRepository) {
				manifest, err := readManifest(filepath.Join(repo.root, defaultManifestPath))
				if err != nil {
					repo.t.Fatal(err)
				}
				repo.git("switch", "-q", "--orphan", "unrelated")
				repo.write("unrelated.txt", "unrelated history\n")
				manifest.MainSHA = repo.commit("unrelated root")
				repo.git("switch", "-q", "main")
				repo.writeManifest(manifest)
			},
			wantErr: "recorded main is not an ancestor",
		},
		{
			name: "user-facing fragments dropped without changelog",
			mutate: func(repo *testRepository) {
				manifest, err := readManifest(filepath.Join(repo.root, defaultManifestPath))
				if err != nil {
					repo.t.Fatal(err)
				}
				data, err := readRepositoryFile(repo.root, manifest.SourceSHA, "CHANGELOG.md")
				if err != nil {
					repo.t.Fatal(err)
				}
				repo.write("CHANGELOG.md", string(data))
			},
			wantErr: "fragments were removed without updating CHANGELOG.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepository(t)
			repo.prepareCandidate("promotion")
			repo.git("switch", "-q", "main")
			repo.git("merge", "--squash", "release/v0.12.0")
			repo.commit("release: accept v0.12.0")
			tc.mutate(repo)
			headSHA := repo.commit("unsafe accepted main")
			_, err := validateAcceptedMain(repo.root, headSHA, defaultManifestPath)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestFragmentRenderingIsStableAndCarriesCommitSubjects(t *testing.T) {
	repo := newTestRepository(t)
	repo.write("changelog.d/z-last.yaml", "kind: fixed\narea: z\nchange: last\n")
	repo.commit("fix(z): add the last fragment")
	repo.write("changelog.d/a-first.yaml", "kind: added\narea: a\nchange: first\n")
	repo.commit("feat(a): add the first fragment")

	rendered, err := renderFragments(repo.root)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Index(rendered, "changelog.d/a-first.yaml")
	last := strings.Index(rendered, "changelog.d/z-last.yaml")
	if first < 0 || last < 0 || first > last {
		t.Fatalf("fragments are not sorted:\n%s", rendered)
	}
	for _, subject := range []string{"feat(a): add the first fragment", "fix(z): add the last fragment"} {
		if !strings.Contains(rendered, subject) {
			t.Fatalf("rendered facts omit %q:\n%s", subject, rendered)
		}
	}
}

func TestFragmentReceiptBindsPathsAndBlobs(t *testing.T) {
	repo := newTestRepository(t)
	repo.write("changelog.d/b.yaml", "kind: fixed\narea: b\nchange: second\n")
	repo.write("changelog.d/a.yaml", "kind: added\narea: a\nchange: first\n")
	first := repo.commit("add receipt inputs")
	receipt, err := fragmentReceipt(repo.root, first)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^<!-- changelog-fragments-sha256: [0-9a-f]{64} -->$`).MatchString(receipt) {
		t.Fatalf("receipt = %q", receipt)
	}

	repo.write("changelog.d/a.yaml", "kind: added\narea: a\nchange: changed\n")
	second := repo.commit("change one receipt input")
	changed, err := fragmentReceipt(repo.root, second)
	if err != nil {
		t.Fatal(err)
	}
	if changed == receipt {
		t.Fatal("fragment receipt did not change with a source blob")
	}
}

func TestPromotionSyncConsumesOnlyTheFrozenFragments(t *testing.T) {
	repo := newTestRepository(t)
	mainAtCut := repo.git("rev-parse", "main")
	repo.git("switch", "-q", "-c", "next")
	repo.write("changelog.d/frozen.yaml", "kind: added\narea: release\nchange: frozen change\n")
	repo.write("feature.txt", "frozen feature\n")
	sourceSHA := repo.commit("feature: accepted for release")

	repo.git("switch", "-q", "-c", "release/v0.12.0")
	if err := setVersions(repo.root, "0.12.0"); err != nil {
		t.Fatal(err)
	}
	repo.remove("changelog.d/frozen.yaml")
	repo.write("CHANGELOG.md", "# Changelog\n\n## [2026-08-28]\n\n- Frozen feature.\n")
	repo.writeManifest(candidateManifest{Version: "0.12.0", Kind: "promotion", SourceSHA: sourceSHA, MainSHA: mainAtCut})
	candidateHead := repo.commit("release: v0.12.0")

	repo.git("switch", "-q", "next")
	repo.write("changelog.d/later.yaml", "kind: added\narea: release\nchange: later change\n")
	repo.write("later.txt", "not in the frozen candidate\n")
	repo.commit("feature: after the freeze")
	if repo.git("ls-tree", "-r", "--name-only", candidateHead, "--", "changelog.d/later.yaml") != "" {
		t.Fatal("the frozen candidate included a later next change")
	}

	repo.git("switch", "-q", "main")
	repo.git("merge", "--squash", "release/v0.12.0")
	mainRelease := repo.commit("release: v0.12.0")
	repo.git("switch", "-q", "next")
	repo.git("merge", "--no-ff", "main", "-m", "sync main after v0.12.0")

	if !repo.exists("changelog.d/frozen.yaml") || !repo.exists("changelog.d/later.yaml") {
		t.Fatal("test setup did not reproduce squash merge retaining both fragments")
	}
	if _, err := syncReleasedFragments(repo.root, defaultManifestPath, mainRelease, "HEAD", false); err == nil || !strings.Contains(err.Error(), "released fragments remain") {
		t.Fatalf("check should catch the retained released fragment, got %v", err)
	}
	removed, err := syncReleasedFragments(repo.root, defaultManifestPath, mainRelease, "HEAD", true)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d fragments, want 1", removed)
	}
	if repo.exists("changelog.d/frozen.yaml") {
		t.Fatal("released fragment survived sync")
	}
	if !repo.exists("changelog.d/later.yaml") {
		t.Fatal("post-candidate fragment was consumed")
	}
	repo.commit("chore(release): consume v0.12.0 fragments")
	if _, err := syncReleasedFragments(repo.root, defaultManifestPath, mainRelease, "HEAD", false); err != nil {
		t.Fatal(err)
	}
	repo.git("merge-base", "--is-ancestor", mainRelease, "HEAD")
}

func TestPromotionSyncRefusesARewrittenReleasedFragment(t *testing.T) {
	repo := newTestRepository(t)
	mainAtCut := repo.git("rev-parse", "main")
	repo.git("switch", "-q", "-c", "next")
	repo.write("changelog.d/frozen.yaml", "kind: added\narea: release\nchange: original\n")
	sourceSHA := repo.commit("accepted source")
	repo.git("switch", "-q", "-c", "release/v0.11.1")
	repo.remove("changelog.d/frozen.yaml")
	repo.writeManifest(candidateManifest{Version: "0.11.1", Kind: "promotion", SourceSHA: sourceSHA, MainSHA: mainAtCut})
	repo.commit("prepare candidate")
	repo.git("switch", "-q", "main")
	repo.git("merge", "--squash", "release/v0.11.1")
	mainRelease := repo.commit("release: v0.11.1")
	repo.git("switch", "-q", "next")
	repo.write("changelog.d/frozen.yaml", "kind: added\narea: release\nchange: rewritten\n")
	repo.commit("rewrite fragment after freeze")
	repo.git("merge", "--no-ff", "main", "-m", "sync main")

	_, err := syncReleasedFragments(repo.root, defaultManifestPath, mainRelease, "HEAD", true)
	if err == nil || !strings.Contains(err.Error(), "changed after the candidate") {
		t.Fatalf("expected rewritten-fragment refusal, got %v", err)
	}
}

func TestPromotionSyncRefusesToDeleteFromADirtyWorktree(t *testing.T) {
	repo := newTestRepository(t)
	mainSHA := repo.git("rev-parse", "main")
	repo.git("switch", "-q", "-c", "next")
	repo.write("changelog.d/frozen.yaml", "kind: added\narea: release\nchange: original\n")
	sourceSHA := repo.commit("accepted source")
	repo.writeManifest(candidateManifest{Version: "0.11.1", Kind: "promotion", SourceSHA: sourceSHA, MainSHA: mainSHA})
	repo.commit("carry release manifest")
	repo.write("changelog.d/frozen.yaml", "kind: added\narea: release\nchange: uncommitted rewrite\n")

	_, err := syncReleasedFragments(repo.root, defaultManifestPath, mainSHA, "HEAD", true)
	if err == nil || !strings.Contains(err.Error(), "working tree must be clean") {
		t.Fatalf("expected dirty-worktree refusal, got %v", err)
	}
	if !repo.exists("changelog.d/frozen.yaml") {
		t.Fatal("dirty fragment was deleted")
	}
}
