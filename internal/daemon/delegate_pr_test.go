package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func delegationPRRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := initDelegationRepo(t, t.TempDir(), "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	runGitDaemon(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")
	runGitDaemon(t, repo, "switch", "-c", "target-source")
	if err := os.WriteFile(filepath.Join(repo, "target.txt"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "add", "target.txt")
	runGitDaemon(t, repo, "commit", "-m", "target")
	target, err := attngit.GetHeadCommit(repo)
	if err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "switch", "main")
	runGitDaemon(t, repo, "branch", "-D", "target-source")
	return repo, target
}

func gitOutputDelegationPR(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func delegationPRFetchStub(t *testing.T) func(string, string, string, string, string, string) error {
	t.Helper()
	return func(repo, remote, _, branch, sha, _ string) error {
		runGitDaemon(t, repo, "update-ref", "refs/remotes/"+remote+"/"+branch, sha)
		return nil
	}
}

func delegationPRTargetFor(repo, sha, headRepo string) *delegationPRTarget {
	receipt := &protocol.DelegatePullRequestReceipt{
		Source: "42", URL: "https://github.com/owner/repo/pull/42", Number: 42, State: "open",
		BaseRepository: "github.com/owner/repo", HeadRepository: "github.com/" + headRepo,
		HeadBranch: "feature", HeadSHA: sha, LocalBranch: "feature",
	}
	return &delegationPRTarget{host: "github.com", ownerRepo: "owner/repo", number: 42, source: "42", mainRepo: repo, receipt: receipt}
}

func TestResolveDelegationPullRequestPersistsLiveSnapshotAndRecoveryReusesIt(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/repos/owner/repo/pulls/42" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = fmt.Fprintf(w, `{"number":42,"html_url":"https://github.com/owner/repo/pull/42","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, targetSHA)
	}))
	defer server.Close()
	client, err := github.NewClientForHost("github.com", server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ghRegistry.Register("github.com", client)
	if _, _, err := d.store.ClaimDelegationOperation("request-pr", "op-pr", "session-pr", "", "", `{}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	resolved, err := d.resolveDelegationPullRequest("42", repo, "op-pr", nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := d.store.GetDelegationOperation("op-pr")
	if err != nil || record.Operation.PullRequest == nil || record.Operation.PullRequest.HeadSHA != targetSHA {
		t.Fatalf("persisted receipt = %+v, err=%v", record, err)
	}
	d.ghRegistry.Remove("github.com")
	recovered, err := d.resolveDelegationPullRequest("42", repo, "op-pr", resolved.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || recovered.receipt.HeadSHA != targetSHA {
		t.Fatalf("requests=%d recovered=%+v", requests, recovered.receipt)
	}
}

func TestResolveDelegationPullRequestValidatesSnapshotIdentity(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	for _, test := range []struct {
		name    string
		payload string
	}{
		{"short SHA", `{"number":42,"html_url":"https://github.com/owner/repo/pull/42","state":"open","head":{"sha":"abc","ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`},
		{"wrong URL", fmt.Sprintf(`{"number":42,"html_url":"https://github.com/owner/repo/pull/41","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, targetSHA)},
		{"unsafe head repository", fmt.Sprintf(`{"number":42,"html_url":"https://github.com/owner/repo/pull/42","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"../repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, targetSHA)},
		{"wrong base", fmt.Sprintf(`{"number":42,"html_url":"https://github.com/owner/repo/pull/42","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"repo":{"full_name":"other/repo"}}}`, targetSHA)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, test.payload) }))
			defer server.Close()
			client, err := github.NewClientForHost("github.com", server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			d.ghRegistry.Register("github.com", client)
			if _, err := d.resolveDelegationPullRequest("42", repo, "", nil); err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
		})
	}
}

func TestResolveDelegationPullRequestRecordsOpenDraftClosedAndMergedStates(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	for _, test := range []struct {
		name, fields, want string
	}{
		{"open", `"state":"open"`, "open"},
		{"draft", `"state":"open","draft":true`, "draft"},
		{"closed", `"state":"closed"`, "closed"},
		{"merged", `"state":"closed","merged":true`, "merged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"number":42,"html_url":"https://github.com/owner/repo/pull/42",%s,"head":{"sha":%q,"ref":"feature","repo":{"full_name":"owner/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, test.fields, targetSHA)
			}))
			defer server.Close()
			client, err := github.NewClientForHost("github.com", server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			d.ghRegistry.Register("github.com", client)
			target, err := d.resolveDelegationPullRequest("42", repo, "", nil)
			if err != nil || target.receipt.State != test.want {
				t.Fatalf("state=%q want=%q err=%v", target.receipt.State, test.want, err)
			}
		})
	}
}

func TestDelegatePullRequestLaunchIncludesDurableReceipt(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"number":42,"html_url":"https://github.com/owner/repo/pull/42","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"owner/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, targetSHA)
	}))
	defer server.Close()
	client, err := github.NewClientForHost("github.com", server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ghRegistry.Register("github.com", client)
	d.delegationPRFetch = delegationPRFetchStub(t)
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, repo)
	var prompt string
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		body, readErr := os.ReadFile(opts.InitialPromptFile)
		if readErr != nil {
			t.Fatal(readErr)
		}
		prompt = string(body)
		_ = os.Remove(opts.InitialPromptFile)
	}
	worktree := filepath.Join(filepath.Dir(repo), "launch-checkout")
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceID, Brief: "Review the resolved pull request.", Agent: protocol.Ptr("codex"),
		Placement: protocol.Ptr(delegationPlacementNew), PullRequest: protocol.Ptr("42"),
		Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PullRequest == nil || result.PullRequest.VerifiedHead != targetSHA || result.Directory != worktree {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(prompt, "Pull request checkout receipt") || !strings.Contains(prompt, targetSHA) || !strings.Contains(prompt, "do not synchronize it again") {
		t.Fatalf("initial prompt missing receipt:\n%s", prompt)
	}
}

func TestDelegationPullRequestRequestIDRetryConvergesOnPersistedSHA(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = fmt.Fprintf(w, `{"number":42,"html_url":"https://github.com/owner/repo/pull/42","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"owner/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, targetSHA)
	}))
	defer server.Close()
	client, err := github.NewClientForHost("github.com", server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	d := newDelegationDaemon(t)
	d.ghRegistry.Register("github.com", client)
	d.delegationPRFetch = func(repo, remote, _, branch, sha, _ string) error {
		record, receiptErr := d.store.GetDelegationOperation("stable-pr-request")
		if receiptErr != nil || record.Operation.PullRequest == nil || record.Operation.PullRequest.HeadSHA != sha {
			return fmt.Errorf("Git mutation started before receipt persistence: record=%+v err=%v", record, receiptErr)
		}
		runGitDaemon(t, repo, "update-ref", "refs/remotes/"+remote+"/"+branch, sha)
		return nil
	}
	backend := &fakeSpawnBackend{}
	_, sourceID, _ := setupDelegationSourceAt(t, d, backend, repo)
	consumeDelegatedPrompt(t, backend)
	worktree := filepath.Join(filepath.Dir(repo), "retry-checkout")
	msg := &protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "stable-pr-request", SourceSessionID: sourceID,
		Brief: "Review the stable PR head.", Agent: protocol.Ptr("codex"), Placement: protocol.Ptr(delegationPlacementNew),
		PullRequest: protocol.Ptr("42"), Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)},
	}
	first, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.startDelegation(msg)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID || first.SessionID != second.SessionID {
		t.Fatalf("retries diverged: first=%+v second=%+v", first, second)
	}
	done := waitDelegationOperation(t, d, first.OperationID)
	if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil || done.Result.PullRequest == nil || done.Result.PullRequest.HeadSHA != targetSHA {
		t.Fatalf("operation=%+v", done)
	}
	if requests != 1 {
		t.Fatalf("GitHub requests=%d want=1", requests)
	}
}

func TestDelegationPullRequestRestartBeforeAndAfterResolvedReceipt(t *testing.T) {
	for _, persistReceipt := range []bool{false, true} {
		name := "before receipt"
		if persistReceipt {
			name = "after receipt"
		}
		t.Run(name, func(t *testing.T) {
			repo, targetSHA := delegationPRRepo(t)
			dbPath := filepath.Join(t.TempDir(), "attn.db")
			persistent, err := store.NewWithDB(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			d1 := NewForTesting(filepath.Join(t.TempDir(), "one.sock"))
			_ = d1.store.Close()
			d1.store = persistent
			backend := &fakeSpawnBackend{}
			_, sourceID, _ := setupDelegationSourceAt(t, d1, backend, repo)
			worktree := filepath.Join(filepath.Dir(repo), "restart-checkout")
			msg := protocol.DelegateMessage{
				Cmd: protocol.CmdDelegate, RequestID: "restart-pr", SourceSessionID: sourceID, Brief: "Resume PR launch.", Agent: protocol.Ptr("codex"),
				Placement: protocol.Ptr(delegationPlacementNew), PullRequest: protocol.Ptr("42"),
				Worktree: &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)},
			}
			requestJSON, _ := json.Marshal(msg)
			record, claimed, err := d1.store.ClaimDelegationOperation(msg.RequestID, "operation-restart-pr", "session-restart-pr", "", "", string(requestJSON), time.Now())
			if err != nil || !claimed {
				t.Fatalf("claim: claimed=%v err=%v", claimed, err)
			}
			if persistReceipt {
				receipt := delegationPRTargetFor(repo, targetSHA, "owner/repo").receipt
				if _, err := d1.store.SaveDelegationPullRequestReceipt(record.Operation.OperationID, *receipt, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			if err := d1.store.Close(); err != nil {
				t.Fatal(err)
			}

			reopened, err := store.NewWithDB(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			d2 := NewForTesting(filepath.Join(t.TempDir(), "two.sock"))
			_ = d2.store.Close()
			d2.store = reopened
			d2.delegationPRFetch = delegationPRFetchStub(t)
			d2.ptyBackend = &fakeSpawnBackend{}
			consumeDelegatedPrompt(t, d2.ptyBackend.(*fakeSpawnBackend))
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = fmt.Fprintf(w, `{"number":42,"html_url":"https://github.com/owner/repo/pull/42","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"owner/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, targetSHA)
			}))
			defer server.Close()
			client, err := github.NewClientForHost("github.com", server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			d2.ghRegistry.Register("github.com", client)
			d2.loadWorkspacesFromStore()
			d2.resumePendingDelegations()
			done := waitDelegationOperation(t, d2, record.Operation.OperationID)
			if done.State != protocol.DelegationOperationStateCompleted || done.Result == nil || done.Result.PullRequest == nil || done.Result.PullRequest.HeadSHA != targetSHA {
				t.Fatalf("operation=%+v", done)
			}
			wantRequests := 1
			if persistReceipt {
				wantRequests = 0
			}
			if requests != wantRequests {
				t.Fatalf("GitHub requests=%d want=%d", requests, wantRequests)
			}
		})
	}
}

func TestParseDelegationPullRequestURLRejectsRepositoryMismatch(t *testing.T) {
	repo, _ := delegationPRRepo(t)
	_, _, _, err := parseDelegationPRSource("https://github.com/other/repo/pull/42", repo)
	if err == nil || !strings.Contains(err.Error(), "does not match a remote") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateDelegationPullRequestAppliesOnlyToNewLaunches(t *testing.T) {
	base := protocol.DelegateMessage{
		PullRequest: protocol.Ptr("42"),
		Worktree:    &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr("/repo")},
	}
	if err := validateDelegationPullRequestRequest(&base, delegationPlacementNew); err != nil {
		t.Fatalf("new launch: %v", err)
	}
	for _, placement := range []string{delegationPlacementCurrent, delegationPlacementExisting} {
		msg := base
		if err := validateDelegationPullRequestRequest(&msg, placement); err == nil || !strings.Contains(err.Error(), "only to a new worktree launch") {
			t.Fatalf("placement %q error=%v", placement, err)
		}
	}
	handover := base
	handover.Handover = &protocol.SeedHandoverRequest{}
	if err := validateDelegationPullRequestRequest(&handover, delegationPlacementNew); err == nil || !strings.Contains(err.Error(), "only to a new worktree launch") {
		t.Fatalf("handover error=%v", err)
	}
}

func TestParseDelegationPullRequestURLRejectsNonCanonicalForms(t *testing.T) {
	repo, _ := delegationPRRepo(t)
	for _, source := range []string{
		"https://user@github.com/owner/repo/pull/42",
		"https://github.com:443/owner/repo/pull/42",
		"https://github.com/owner/repo/pull/42?view=files",
		"https://github.com/owner/repo/pull/42#discussion",
		"https://github.com/owner%2Frepo/pull/42",
	} {
		if _, _, _, err := parseDelegationPRSource(source, repo); err == nil {
			t.Fatalf("parseDelegationPRSource(%q) succeeded", source)
		}
	}
}

func TestParseDelegationPullRequestURLAcceptsConfiguredEnterpriseRemoteIdentity(t *testing.T) {
	repo, _ := delegationPRRepo(t)
	runGitDaemon(t, repo, "remote", "set-url", "origin", "git@ghe.example.test:owner/repo.git")
	host, ownerRepo, number, err := parseDelegationPRSource("https://ghe.example.test/owner/repo/pull/17", repo)
	if err != nil || host != "ghe.example.test" || ownerRepo != "owner/repo" || number != 17 {
		t.Fatalf("host=%q repo=%q number=%d err=%v", host, ownerRepo, number, err)
	}
}

func TestResolveDelegationPullRequestFromConfiguredEnterpriseHost(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "remote", "set-url", "origin", "git@ghe.example.test:owner/repo.git")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/pulls/17" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = fmt.Fprintf(w, `{"number":17,"html_url":"https://ghe.example.test/owner/repo/pull/17","state":"open","head":{"sha":%q,"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"repo":{"full_name":"owner/repo"}}}`, targetSHA)
	}))
	defer server.Close()
	client, err := github.NewClientForHost("ghe.example.test", server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ghRegistry.Register("ghe.example.test", client)
	target, err := d.resolveDelegationPullRequest("https://ghe.example.test/owner/repo/pull/17", repo, "", nil)
	if err != nil || target.receipt.HeadRepository != "ghe.example.test/fork/repo" || target.receipt.HeadSHA != targetSHA {
		t.Fatalf("target=%+v err=%v", target, err)
	}
}

func TestMaterializeDelegationPullRequestUsesForkAndExactSHA(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	worktree := filepath.Join(filepath.Dir(repo), "checkout")
	request := &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}
	target := delegationPRTargetFor(repo, targetSHA, "fork/repo")
	path, created, err := d.materializeDelegationPullRequest(target, request, "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !created || path != worktree || protocol.Deref(request.StartingFrom) != targetSHA {
		t.Fatalf("path=%q created=%v starting_from=%q", path, created, protocol.Deref(request.StartingFrom))
	}
	if head, _ := attngit.GetHeadCommit(path); head != targetSHA {
		t.Fatalf("HEAD=%s want %s", head, targetSHA)
	}
	if upstream := strings.TrimSpace(gitOutputDelegationPR(t, path, "rev-parse", "--abbrev-ref", "@{upstream}")); upstream != "attn-fork-repo/feature" {
		t.Fatalf("upstream = %q", upstream)
	}
}

func TestMaterializeDelegationPullRequestPassesResolvedSHAtoProvider(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	client, done := startPluginPipe(t, d, "pr-create-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()
	worktree := filepath.Join(filepath.Dir(repo), "provider-checkout")
	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		if params.StartingFrom != targetSHA {
			t.Fatalf("provider starting_from=%q, want %s", params.StartingFrom, targetSHA)
		}
		runGitDaemon(t, repo, "worktree", "add", "-b", params.Branch, worktree, params.StartingFrom)
		return worktreeCreateProviderResult{Status: providerStatusHandled, Path: worktree, Branch: params.Branch}
	})
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	_, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderResponse(t, responseDone)
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("provider connection did not close")
	}
}

func TestMaterializeDelegationPullRequestRejectsProviderAtWrongHEAD(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	client, done := startPluginPipe(t, d, "wrong-head-provider", []string{worktreeCreateProviderSurface})
	defer client.Close()
	worktree := filepath.Join(filepath.Dir(repo), "wrong-head-checkout")
	responseDone := respondToCreateProviderCall(t, client, func(params worktreeCreateProviderParams) worktreeCreateProviderResult {
		runGitDaemon(t, repo, "worktree", "add", "-b", params.Branch, worktree, "main")
		return worktreeCreateProviderResult{Status: providerStatusHandled, Path: worktree, Branch: params.Branch}
	})
	_, _, err := d.materializeDelegationPullRequest(delegationPRTargetFor(repo, targetSHA, "owner/repo"), &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "", "", false, "")
	if err == nil || !strings.Contains(err.Error(), "HEAD mismatch") {
		t.Fatalf("error=%v", err)
	}
	waitForProviderResponse(t, responseDone)
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatalf("wrong provider worktree was not rolled back: %v", statErr)
	}
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("provider connection did not close")
	}
}

func TestMaterializeDelegationPullRequestPreservesDivergentBranchWithoutWorktree(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(repo, "divergent.txt"), []byte("local commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "add", "divergent.txt")
	runGitDaemon(t, repo, "commit", "-m", "divergent")
	localHead, _ := attngit.GetHeadCommit(repo)
	runGitDaemon(t, repo, "switch", "main")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	worktree := filepath.Join(filepath.Dir(repo), "divergent-checkout")
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	_, created, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "", "", false, "")
	if err != nil || !created || protocol.Deref(target.receipt.BackupBranch) == "" || protocol.Deref(target.receipt.BackupHead) != localHead {
		t.Fatalf("created=%v receipt=%+v err=%v", created, target.receipt, err)
	}
	if backupHead, ok, _ := attngit.BranchHead(repo, protocol.Deref(target.receipt.BackupBranch)); !ok || backupHead != localHead {
		t.Fatalf("backup head=%s ok=%v want=%s", backupHead, ok, localHead)
	}
}

func TestMaterializeDelegationPullRequestPreservesConflictingWorktree(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(repo, "local.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	request := &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(repo)}
	path, created, err := d.materializeDelegationPullRequest(target, request, "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	backup := protocol.Deref(target.receipt.BackupBranch)
	if path != repo || created || backup == "" || target.receipt.Disposition != "preserved" {
		t.Fatalf("path=%q created=%v receipt=%+v", path, created, target.receipt)
	}
	if got := strings.TrimSpace(gitOutputDelegationPR(t, repo, "show", backup+":local.txt")); got != "uncommitted" {
		t.Fatalf("backup local.txt = %q", got)
	}
	if head, _ := attngit.GetHeadCommit(repo); head != targetSHA {
		t.Fatalf("HEAD=%s want %s", head, targetSHA)
	}
}

func TestMaterializeDelegationPullRequestReusesExactCleanWorktree(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature", targetSHA)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	path, created, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err != nil || path != repo || created || target.receipt.Disposition != "reused" {
		t.Fatalf("path=%q created=%v receipt=%+v err=%v", path, created, target.receipt, err)
	}
}

func TestMaterializeDelegationPullRequestStrictlyFastForwardsCleanWorktree(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	path, created, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err != nil || path != repo || created || target.receipt.Disposition != "fast-forwarded" {
		t.Fatalf("path=%q created=%v receipt=%+v err=%v", path, created, target.receipt, err)
	}
}

func TestMaterializeDelegationPullRequestRefusesLiveBranchOwnerBeforeMutation(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, _, _ = setupDelegationSourceAt(t, d, backend, repo)
	backend.sessionIDs = []string{"session-source"}
	fetched := false
	d.delegationPRFetch = func(string, string, string, string, string, string) error {
		fetched = true
		return nil
	}
	target := delegationPRTargetFor(repo, targetSHA, "fork/repo")
	_, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err == nil || !strings.Contains(err.Error(), "owned by live attn session") {
		t.Fatalf("error = %v", err)
	}
	if fetched {
		t.Fatal("live-owner refusal fetched the pull request")
	}
	if _, ok := attngit.RemoteForIdentity(repo, "github.com/fork/repo"); ok {
		t.Fatal("live-owner refusal added the fork remote")
	}
}

func TestMaterializeDelegationPullRequestRefusesCheckoutLockBeforeFetch(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	release, err := attngit.AcquirePullRequestCheckoutLock(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	fetched := false
	d.delegationPRFetch = func(string, string, string, string, string, string) error { fetched = true; return nil }
	_, _, err = d.materializeDelegationPullRequest(delegationPRTargetFor(repo, targetSHA, "owner/repo"), &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err == nil || !strings.Contains(err.Error(), "already changing") || fetched {
		t.Fatalf("fetched=%v error=%v", fetched, err)
	}
}

func TestMaterializeDelegationPullRequestRefusesInProgressOperationBeforeFetch(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	gitDir := strings.TrimSpace(gitOutputDelegationPR(t, repo, "rev-parse", "--git-dir"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repo, gitDir)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte(targetSHA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	fetched := false
	d.delegationPRFetch = func(string, string, string, string, string, string) error { fetched = true; return nil }
	_, _, err := d.materializeDelegationPullRequest(delegationPRTargetFor(repo, targetSHA, "fork/repo"), &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err == nil || !strings.Contains(err.Error(), "in-progress merge") || fetched {
		t.Fatalf("fetched=%v error=%v", fetched, err)
	}
}

func TestMaterializeDelegationPullRequestPreservesIgnoredDescendantCollision(t *testing.T) {
	repo := initDelegationRepo(t, t.TempDir(), "repo")
	runGitDaemon(t, repo, "branch", "-M", "main")
	runGitDaemon(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("foo/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "add", ".gitignore")
	runGitDaemon(t, repo, "commit", "-m", "ignore generated directory")
	runGitDaemon(t, repo, "switch", "-c", "target-source")
	if err := os.WriteFile(filepath.Join(repo, "foo"), []byte("target file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "add", "-f", "foo")
	runGitDaemon(t, repo, "commit", "-m", "target")
	targetSHA, _ := attngit.GetHeadCommit(repo)
	runGitDaemon(t, repo, "switch", "main")
	runGitDaemon(t, repo, "branch", "-D", "target-source")
	runGitDaemon(t, repo, "switch", "-c", "feature")
	if err := os.MkdirAll(filepath.Join(repo, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(repo, "foo", "precious.txt")
	if err := os.WriteFile(precious, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	_, _, err := d.materializeDelegationPullRequest(delegationPRTargetFor(repo, targetSHA, "fork/repo"), &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err == nil || !strings.Contains(err.Error(), "collide") {
		t.Fatalf("error=%v", err)
	}
	if body, readErr := os.ReadFile(precious); readErr != nil || string(body) != "keep\n" {
		t.Fatalf("precious ignored file = %q, %v", body, readErr)
	}
	if _, ok := attngit.RemoteForIdentity(repo, "github.com/fork/repo"); ok {
		t.Fatal("failed checkout retained the added fork remote")
	}
}

func TestFinishDelegationPullRequestRejectsWrongBranchAtExactHEAD(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "reset", "--hard", targetSHA)
	runGitDaemon(t, repo, "update-ref", "refs/remotes/origin/feature", targetSHA)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	_, _, err := d.finishDelegationPRCheckout(target, repo, false, &protocol.DelegateWorktreeRequest{}, "")
	if err == nil || !strings.Contains(err.Error(), "branch mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestPullRequestGenericFallbackRejectsMissingResolvedSHA(t *testing.T) {
	repo, _ := delegationPRRepo(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	path := filepath.Join(filepath.Dir(repo), "must-not-exist")
	_, err := d.doCreateWorktreeWithOptions(&protocol.CreateWorktreeMessage{
		MainRepo: repo, Branch: "feature", Path: protocol.Ptr(path),
		StartingFrom: protocol.Ptr("0123456789abcdef0123456789abcdef01234567"),
	}, true)
	if err == nil || !strings.Contains(err.Error(), "no longer resolvable") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("fallback created %s: %v", path, statErr)
	}
}

func TestMaterializeDelegationPullRequestRecoversPersistedBackupAtSameSHA(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	sourceHead, _ := attngit.GetHeadCommit(repo)
	runGitDaemon(t, repo, "switch", "-c", "feature--attn-backup-test")
	fingerprint, _ := attngit.StatusFingerprint(repo)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	target.receipt.BackupBranch = protocol.Ptr("feature--attn-backup-test")
	target.receipt.BackupHead = protocol.Ptr(sourceHead)
	target.receipt.BackupSourceHead = protocol.Ptr(sourceHead)
	target.receipt.BackupFingerprint = protocol.Ptr(fingerprint)
	target.receipt.WorktreePath = repo
	runGitDaemon(t, repo, "update-ref", "refs/remotes/origin/feature", targetSHA)
	path, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != repo || target.receipt.VerifiedHead != targetSHA {
		t.Fatalf("path=%q receipt=%+v", path, target.receipt)
	}
}

func TestMaterializeDelegationPullRequestResumesBeforeBackupMutation(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(repo, "local.txt"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	target.receipt.BackupBranch = protocol.Ptr("feature--attn-backup-planned")
	sourceHead, _ := attngit.GetHeadCommit(repo)
	fingerprint, _ := attngit.StatusFingerprint(repo)
	target.receipt.BackupSourceHead = protocol.Ptr(sourceHead)
	target.receipt.BackupFingerprint = protocol.Ptr(fingerprint)
	target.receipt.WorktreePath = repo
	_, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitOutputDelegationPR(t, repo, "show", "feature--attn-backup-planned:local.txt")); got != "keep me" {
		t.Fatalf("planned backup local.txt = %q", got)
	}
}

func TestMaterializeDelegationPullRequestFinishesPartialBackupBeforeCheckout(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(repo, "partial.txt"), []byte("preserve me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceHead, _ := attngit.GetHeadCommit(repo)
	fingerprint, _ := attngit.StatusFingerprint(repo)
	runGitDaemon(t, repo, "switch", "-c", "feature--attn-backup-partial")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	target.receipt.BackupBranch = protocol.Ptr("feature--attn-backup-partial")
	target.receipt.BackupSourceHead = protocol.Ptr(sourceHead)
	target.receipt.BackupFingerprint = protocol.Ptr(fingerprint)
	target.receipt.WorktreePath = repo
	_, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	backupHead := protocol.Deref(target.receipt.BackupHead)
	if backupHead == "" || strings.TrimSpace(gitOutputDelegationPR(t, repo, "show", backupHead+":partial.txt")) != "preserve me" {
		t.Fatalf("receipt=%+v", target.receipt)
	}
}

func TestMaterializeDelegationPullRequestVerifiedRecoveryOnlyChecksHEAD(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature", targetSHA)
	runGitDaemon(t, repo, "update-ref", "refs/remotes/origin/feature", targetSHA)
	runGitDaemon(t, repo, "branch", "--set-upstream-to", "origin/feature", "feature")
	fingerprint, _ := attngit.StatusFingerprint(repo)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = func(string, string, string, string, string, string) error {
		t.Fatal("verified recovery fetched the pull request again")
		return nil
	}
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	target.receipt.WorktreePath = repo
	target.receipt.VerifiedHead = targetSHA
	target.receipt.CheckoutFingerprint = protocol.Ptr(fingerprint)
	path, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err != nil || path != repo {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestMaterializeDelegationPullRequestVerifiedRecoveryRefusesLiveOwner(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature", targetSHA)
	runGitDaemon(t, repo, "update-ref", "refs/remotes/origin/feature", targetSHA)
	runGitDaemon(t, repo, "branch", "--set-upstream-to", "origin/feature", "feature")
	fingerprint, _ := attngit.StatusFingerprint(repo)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	_, _, _ = setupDelegationSourceAt(t, d, backend, repo)
	backend.sessionIDs = []string{"session-source"}
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	target.receipt.WorktreePath = repo
	target.receipt.VerifiedHead = targetSHA
	target.receipt.CheckoutFingerprint = protocol.Ptr(fingerprint)
	_, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err == nil || !strings.Contains(err.Error(), "owned by live attn session") {
		t.Fatalf("error=%v", err)
	}
}

func TestMaterializeDelegationPullRequestVerifiedRecoveryRefusesUpstreamDrift(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	runGitDaemon(t, repo, "switch", "-c", "feature", targetSHA)
	runGitDaemon(t, repo, "update-ref", "refs/remotes/origin/feature", targetSHA)
	runGitDaemon(t, repo, "branch", "--set-upstream-to", "origin/feature", "feature")
	fingerprint, _ := attngit.StatusFingerprint(repo)
	runGitDaemon(t, repo, "remote", "add", "other", "https://github.com/other/repo.git")
	runGitDaemon(t, repo, "update-ref", "refs/remotes/other/feature", targetSHA)
	runGitDaemon(t, repo, "branch", "--set-upstream-to", "other/feature", "feature")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	target.receipt.WorktreePath = repo
	target.receipt.VerifiedHead = targetSHA
	target.receipt.CheckoutFingerprint = protocol.Ptr(fingerprint)
	_, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo)}, "", "", false, "")
	if err == nil || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("error=%v", err)
	}
}

func TestVerifyDelegationPullRequestLaunchRefusesPostCheckoutEdit(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	worktree := filepath.Join(filepath.Dir(repo), "edited-after-checkout")
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	if _, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "", "", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "target.txt"), []byte("changed after checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.verifyDelegationPRLaunchReceipt(target, target.receipt); err == nil || !strings.Contains(err.Error(), "changed after verification") {
		t.Fatalf("error=%v", err)
	}
}

func TestMaterializeDelegationPullRequestRetryRequiresDurableWorktreeOwnership(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	if _, _, err := d.store.ClaimDelegationOperation("request-owned", "op-owned", "session-owned", "", "", `{}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(filepath.Dir(repo), "owned-checkout")
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	if _, err := d.store.SaveDelegationPullRequestReceipt("op-owned", *target.receipt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "op-owned", "", false, ""); err != nil {
		t.Fatal(err)
	}
	record, err := d.store.GetDelegationOperation("op-owned")
	if err != nil || !record.WorktreeOwned {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	ownerPath, err := delegationWorktreeOwnerPath(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ownerPath); err != nil {
		t.Fatal(err)
	}
	_, _, err = d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "op-owned", worktree, true, record.WorktreeToken)
	if err == nil || !strings.Contains(err.Error(), "ownership cannot be proven") {
		t.Fatalf("error=%v", err)
	}
}

func TestMaterializeDelegationPullRequestRetryRetainsProvenCreatedOwnership(t *testing.T) {
	repo, targetSHA := delegationPRRepo(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.delegationPRFetch = delegationPRFetchStub(t)
	if _, _, err := d.store.ClaimDelegationOperation("request-owned-crash", "op-owned-crash", "session-owned-crash", "", "", `{}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(filepath.Dir(repo), "owned-crash-checkout")
	target := delegationPRTargetFor(repo, targetSHA, "owner/repo")
	if _, err := d.store.SaveDelegationPullRequestReceipt("op-owned-crash", *target.receipt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "op-owned-crash", "", false, ""); err != nil {
		t.Fatal(err)
	}
	record, err := d.store.GetDelegationOperation("op-owned-crash")
	if err != nil || !record.WorktreeOwned {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	target.receipt.VerifiedHead = ""
	target.receipt.CheckoutFingerprint = nil
	target.receipt.Disposition = ""
	if err := d.store.UpdateDelegationPullRequestReceipt("op-owned-crash", *target.receipt, time.Now()); err != nil {
		t.Fatal(err)
	}
	path, created, err := d.materializeDelegationPullRequest(target, &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(repo), Path: protocol.Ptr(worktree)}, "op-owned-crash", worktree, true, record.WorktreeToken)
	if err != nil || path != worktree || !created || target.receipt.Disposition != "created" {
		t.Fatalf("path=%q created=%v receipt=%+v err=%v", path, created, target.receipt, err)
	}
}
