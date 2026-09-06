package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/logging"
)

func newDaemonWithRealGitHubHost(t *testing.T) (*Daemon, func() string) {
	t.Helper()
	realClient, err := github.NewClientForHost("github.com", "https://api.github.com", "real-token")
	if err != nil {
		t.Fatalf("NewClientForHost error: %v", err)
	}
	d := NewWithGitHubClient(filepath.Join(shortTempDir(t), "attn.sock"), realClient)
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new test logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	d.logger = logger
	return d, func() string {
		body, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read daemon log: %v", err)
		}
		return string(body)
	}
}

func TestRefreshGitHubHosts_NamedProfileDropsRealClientsUntilOptedIn(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_PROFILE", "dev")
	t.Setenv(GitHubPollingOptInEnv, "")
	d, readLog := newDaemonWithRealGitHubHost(t)

	for i := 0; i < 2; i++ {
		if err := d.refreshGitHubHosts(); err != nil {
			t.Fatalf("refreshGitHubHosts error: %v", err)
		}
	}

	if hosts := d.gitHubHosts(); len(hosts) != 0 {
		t.Fatalf("hosts = %v, want none for a named profile without opt-in", hosts)
	}
	if warnings := d.getWarnings(); len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none: the gh CLI must not be consulted", warnings)
	}
	want := "GitHub polling is off for profile dev. Start its daemon with ATTN_GITHUB_POLLING=on"
	if got := readLog(); strings.Count(got, want) != 1 {
		t.Fatalf("daemon log should say once how to enable polling, got:\n%s", got)
	}
	msg := d.gitHubHostsUpdatedMessage()
	if msg.GithubPollingOffReason == nil || !strings.HasPrefix(*msg.GithubPollingOffReason, want) {
		t.Fatalf("github_hosts_updated reason = %v, want %q", msg.GithubPollingOffReason, want)
	}
}

func TestRefreshGitHubHosts_OptInRestoresDiscoveryForNamedProfile(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_PROFILE", "dev")
	t.Setenv(GitHubPollingOptInEnv, "on")
	d, readLog := newDaemonWithRealGitHubHost(t)

	if err := d.refreshGitHubHosts(); err != nil {
		t.Fatalf("refreshGitHubHosts error: %v", err)
	}

	warnings := d.getWarnings()
	if len(warnings) != 1 || warnings[0].Code != warnGHNotInstalled {
		t.Fatalf("warnings = %v, want the gh lookup that proves discovery ran", warnings)
	}
	if got := readLog(); strings.Contains(got, "GitHub polling is off") {
		t.Fatalf("daemon log should not report polling off when opted in:\n%s", got)
	}
	if reason := d.gitHubHostsUpdatedMessage().GithubPollingOffReason; reason != nil && *reason != "" {
		t.Fatalf("github_hosts_updated reason = %q, want empty", *reason)
	}
}

func TestRefreshGitHubHosts_ProductionProfileStillDiscovers(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ATTN_MOCK_GH_URL", "")
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv(GitHubPollingOptInEnv, "")
	d, _ := newDaemonWithRealGitHubHost(t)

	if err := d.refreshGitHubHosts(); err != nil {
		t.Fatalf("refreshGitHubHosts error: %v", err)
	}

	warnings := d.getWarnings()
	if len(warnings) != 1 || warnings[0].Code != warnGHNotInstalled {
		t.Fatalf("warnings = %v, want the gh lookup that proves discovery ran", warnings)
	}
}
