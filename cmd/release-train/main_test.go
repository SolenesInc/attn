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
	repo.write(defaultManifestPath, "version: "+manifest.Version+"\nmain_sha: "+manifest.MainSHA+"\n")
}

func (repo *testRepository) prepareCandidate() (mainSHA, headSHA string) {
	repo.t.Helper()
	repo.write("changelog.d/accepted.yaml", "kind: fixed\narea: release\nchange: accepted change\n")
	mainSHA = repo.commit("accepted change on main")
	repo.git("switch", "-q", "-c", "release/v0.12.0")
	receipt, err := fragmentReceipt(repo.root, mainSHA)
	if err != nil {
		repo.t.Fatal(err)
	}
	if err := setVersions(repo.root, "0.12.0"); err != nil {
		repo.t.Fatal(err)
	}
	repo.remove("changelog.d/accepted.yaml")
	repo.write("CHANGELOG.md", "# Changelog\n\n## [2026-08-28]\n\n- Accepted change.\n\n"+receipt+"\n")
	repo.writeManifest(candidateManifest{Version: "0.12.0", MainSHA: mainSHA})
	headSHA = repo.commit("prepare release")
	return mainSHA, headSHA
}

func (repo *testRepository) squashRelease() string {
	repo.t.Helper()
	repo.git("switch", "-q", "main")
	repo.git("merge", "--squash", "release/v0.12.0")
	return repo.commit("chore(release): prepare v0.12.0")
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
		{name: "release", body: "version: 1.2.3\nmain_sha: " + sha + "\n"},
		{name: "bad version", body: "version: v1.2\nmain_sha: " + sha + "\n", wantErr: "version must look like"},
		{name: "short sha", body: "version: 1.2.3\nmain_sha: abc123\n", wantErr: "full commit SHA"},
		{name: "retired train field", body: "version: 1.2.3\nkind: promotion\nmain_sha: " + sha + "\n", wantErr: "field kind not found"},
		{name: "second document", body: "version: 1.2.3\nmain_sha: " + sha + "\n---\nversion: 1.2.4\n", wantErr: "more than one YAML document"},
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

func TestManifestWriteRecordsMain(t *testing.T) {
	repo := newTestRepository(t)
	mainSHA := repo.git("rev-parse", "main")
	var output strings.Builder
	if err := runManifest(repo.root, []string{"write", "--version", "v0.12.0", "--main", "main"}, &output); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(filepath.Join(repo.root, defaultManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.12.0" || manifest.MainSHA != mainSHA {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestCandidateValidationAcceptsAPreparedRelease(t *testing.T) {
	repo := newTestRepository(t)
	mainSHA, headSHA := repo.prepareCandidate()
	if err := validateCandidate(repo.root, validCandidateInput(mainSHA, headSHA)); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateValidationAllowsMainToMoveOn(t *testing.T) {
	repo := newTestRepository(t)
	_, headSHA := repo.prepareCandidate()
	repo.git("switch", "-q", "main")
	repo.write("changelog.d/later.yaml", "kind: fixed\narea: queue\nchange: later work\n")
	repo.commit("fix(queue): later work")
	if err := validateCandidate(repo.root, validCandidateInput("main", headSHA)); err != nil {
		t.Fatal(err)
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
			name: "another candidate is open",
			input: func(main, head string) candidateValidation {
				input := validCandidateInput(main, head)
				input.otherOpenCandidates = 1
				return input
			},
			wantErr: "other open release candidate",
		},
		{
			name: "recorded main is not on current main",
			mutate: func(repo *testRepository, _, _ string) {
				emptyTree := repo.git("hash-object", "-t", "tree", "/dev/null")
				repo.git("update-ref", "refs/heads/unrelated", repo.git("commit-tree", emptyTree, "-m", "unrelated root"))
			},
			input: func(_ string, head string) candidateValidation {
				return validCandidateInput("unrelated", head)
			},
			wantErr: "recorded main is not on current main",
		},
		{
			name: "remote tag exists",
			input: func(main, head string) candidateValidation {
				input := validCandidateInput(main, head)
				input.tagStatus = "present"
				return input
			},
			wantErr: "expected absent",
		},
		{
			name: "version disagreement",
			mutate: func(repo *testRepository, _, _ string) {
				repo.write("app/package.json", "{\"version\": \"0.12.1\"}\n")
				repo.commit("mismatch version")
			},
			input: func(main, _ string) candidateValidation {
				return validCandidateInput(main, "HEAD")
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
				return validCandidateInput(main, "HEAD")
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
				data, err := readRepositoryFile(repo.root, manifest.MainSHA, "CHANGELOG.md")
				if err != nil {
					repo.t.Fatal(err)
				}
				repo.write("CHANGELOG.md", string(data))
				repo.commit("drop release notes")
			},
			input: func(main, _ string) candidateValidation {
				return validCandidateInput(main, "HEAD")
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
				data, err := readRepositoryFile(repo.root, manifest.MainSHA, "CHANGELOG.md")
				if err != nil {
					repo.t.Fatal(err)
				}
				repo.write("CHANGELOG.md", string(data)+"\nUnrelated cleanup.\n")
				repo.commit("edit changelog without compiling fragments")
			},
			input: func(main, _ string) candidateValidation {
				return validCandidateInput(main, "HEAD")
			},
			wantErr: "does not contain the frozen fragment receipt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepository(t)
			mainSHA, headSHA := repo.prepareCandidate()
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
	repo.write("changelog.d/internal.yaml", "kind: internal\narea: release\nchange: internal change\n")
	mainSHA := repo.commit("internal change on main")
	repo.git("switch", "-q", "-c", "release/v0.12.0")
	if err := setVersions(repo.root, "0.12.0"); err != nil {
		t.Fatal(err)
	}
	repo.remove("changelog.d/internal.yaml")
	repo.writeManifest(candidateManifest{Version: "0.12.0", MainSHA: mainSHA})
	headSHA := repo.commit("prepare internal release")

	if err := validateCandidate(repo.root, validCandidateInput(mainSHA, headSHA)); err != nil {
		t.Fatal(err)
	}
}

func validCandidateInput(main, head string) candidateValidation {
	return candidateValidation{
		manifestPath: defaultManifestPath, currentMainRef: main, headRef: head,
		tagStatus: "absent", otherOpenCandidates: 0,
	}
}

func TestAcceptedMainValidatesOnlyTheReleaseCommit(t *testing.T) {
	repo := newTestRepository(t)
	repo.prepareCandidate()
	releaseSHA := repo.squashRelease()
	manifest, err := validateAcceptedMain(repo.root, releaseSHA, defaultManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.12.0" {
		t.Fatalf("version = %q", manifest.Version)
	}

	repo.write("later.txt", "ordinary work after the release\n")
	laterSHA := repo.commit("feat: later work on main")
	_, err = validateAcceptedMain(repo.root, laterSHA, defaultManifestPath)
	if err == nil || !strings.Contains(err.Error(), "is not a release commit") {
		t.Fatalf("expected later main commit to be refused, got %v", err)
	}
}

func TestAcceptedMainKeepsFragmentsThatLandedDuringTheRelease(t *testing.T) {
	repo := newTestRepository(t)
	repo.prepareCandidate()
	repo.git("switch", "-q", "main")
	repo.write("changelog.d/later.yaml", "kind: fixed\narea: queue\nchange: later work\n")
	repo.commit("fix(queue): later work")
	releaseSHA := repo.squashRelease()
	if _, err := validateAcceptedMain(repo.root, releaseSHA, defaultManifestPath); err != nil {
		t.Fatal(err)
	}
	if !repo.exists("changelog.d/later.yaml") {
		t.Fatal("release consumed a fragment it did not compile")
	}
}

func TestAcceptedMainValidatesAReleaseMergedWithAMergeCommit(t *testing.T) {
	repo := newTestRepository(t)
	repo.prepareCandidate()
	repo.git("switch", "-q", "main")
	repo.git("merge", "-q", "--no-ff", "-m", "Merge release/v0.12.0", "release/v0.12.0")
	if _, err := validateAcceptedMain(repo.root, repo.git("rev-parse", "HEAD"), defaultManifestPath); err != nil {
		t.Fatal(err)
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
			name: "compiled fragment left behind",
			mutate: func(repo *testRepository) {
				repo.write("changelog.d/accepted.yaml", "kind: fixed\narea: release\nchange: accepted change\n")
			},
			wantErr: "still contains the changelog fragments it compiled: changelog.d/accepted.yaml",
		},
		{
			name: "unrelated recorded main",
			mutate: func(repo *testRepository) {
				manifest, err := readManifest(filepath.Join(repo.root, defaultManifestPath))
				if err != nil {
					repo.t.Fatal(err)
				}
				emptyTree := repo.git("hash-object", "-t", "tree", "/dev/null")
				manifest.MainSHA = repo.git("commit-tree", emptyTree, "-m", "unrelated root")
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
				data, err := readRepositoryFile(repo.root, manifest.MainSHA, "CHANGELOG.md")
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
			repo.prepareCandidate()
			repo.git("switch", "-q", "main")
			repo.git("merge", "--squash", "release/v0.12.0")
			tc.mutate(repo)
			headSHA := repo.commit("chore(release): prepare v0.12.0")
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
