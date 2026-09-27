package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func applyApp(t *testing.T, s *testworld.Stack, name, manifest, source string) {
	t.Helper()
	dir := s.Path(name)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{"attn-app.toml": manifest, "src/index.ts": source} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if applied := s.Attn("app", "apply", dir); applied.Code != 0 {
		t.Fatalf("attn app apply %s exited %d: %s", name, applied.Code, applied.Stderr)
	}
}

func subscribedApp(name, events string, reconcile bool) string {
	return fmt.Sprintf("name = %q\nattn_app_api = 1\nentrypoint = \"src/index.ts\"\nreconcile = %t\n\n[[subscribe]]\nevents = [%q]\n", name, reconcile, events)
}

func statusRow(t *testing.T, status testworld.Result, label string) string {
	t.Helper()
	if status.Code != 0 {
		t.Fatalf("attn app status exited %d: %s", status.Code, status.Stderr)
	}
	for _, line := range strings.Split(status.Stdout, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), label); found {
			return value
		}
	}
	t.Fatalf("attn app status has no %q row:\n%s", label, status.Stdout)
	return ""
}

func TestAppStatusSaysWhatTheRuntimeAndReconcileOwe(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	requireFailure(t, s.Attn("app", "runtime", "restart", "greeter"), "app runtime restart: ",
		`"greeter" looks like one`, "one shared runtime", "`attn app disable greeter`")

	s.Start()
	applyApp(t, s, "ledger", subscribedApp("ledger", "ticket.*", false), "export default {}\n")
	applyApp(t, s, "digest", subscribedApp("digest", "ticket.*", true), "export default { edition: 1 }\n")
	requireStdout(t, s.Attn("app", "disable", "digest"), "app digest disabled")
	applyApp(t, s, "digest", subscribedApp("digest", "ticket.*", true), "export default { edition: 2 }\n")

	ledger := s.Attn("app", "status", "ledger")
	requireLines(t, "ledger's runtime row", statusRow(t, ledger, "runtime:"), "not started", "`attn app runtime status`")
	requireLines(t, "ledger's reconcile row", statusRow(t, ledger, "reconcile:"), "unsupported", "declares and implements reconcile")

	var digest struct {
		Reconcile struct {
			State  string `json:"state"`
			Reason *struct {
				ThroughSeq int      `json:"through_seq"`
				Causes     []string `json:"causes"`
			} `json:"reason"`
		} `json:"reconcile"`
	}
	s.Attn("app", "status", "digest", "--json").JSON(t, &digest)
	owed := digest.Reconcile
	if owed.State != "owed" || owed.Reason == nil || owed.Reason.ThroughSeq <= 0 || fmt.Sprint(owed.Reason.Causes) != "[version_changed]" {
		t.Fatalf("digest's reconcile after a version move while disabled = %+v, want owed for the version change", owed)
	}
	requireLines(t, "digest's reconcile row", statusRow(t, s.Attn("app", "status", "digest"), "reconcile:"),
		fmt.Sprintf("owed through seq %d (version_changed)", owed.Reason.ThroughSeq))
}

func TestOwedAppReconcileSurvivesADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	applyApp(t, s, "digest", subscribedApp("digest", "ticket.*", true), "export default { edition: 1 }\n")
	requireStdout(t, s.Attn("app", "disable", "digest"), "app digest disabled")
	applyApp(t, s, "digest", subscribedApp("digest", "ticket.*", true), "export default { edition: 2 }\n")

	owedReconcile := func() string {
		var status struct {
			Reconcile struct {
				State  string `json:"state"`
				Reason *struct {
					Causes           []string `json:"causes"`
					Version          int      `json:"version"`
					ThroughSeq       int      `json:"through_seq"`
					PreviousVersions []int    `json:"previous_versions"`
				} `json:"reason"`
			} `json:"reconcile"`
		}
		s.Attn("app", "status", "digest", "--json").JSON(t, &status)
		if status.Reconcile.Reason == nil {
			return status.Reconcile.State
		}
		return fmt.Sprintf("%s %+v", status.Reconcile.State, *status.Reconcile.Reason)
	}
	before := owedReconcile()
	if want := "owed {Causes:[version_changed] Version:2 ThroughSeq:"; !strings.HasPrefix(before, want) || strings.Contains(before, "ThroughSeq:0 ") {
		t.Fatalf("digest's reconcile after a version move while disabled = %s, want owed for the version change through a fence", before)
	}

	s.Stop()
	s.Start()

	if after := owedReconcile(); after != before {
		t.Fatalf("digest's owed reconcile across a daemon restart:\nbefore %s\nafter  %s", before, after)
	}
}
