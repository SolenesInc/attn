package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/testworld"
)

func applyAppDeclaration(t *testing.T, s *testworld.Stack, cli *client.Client, name, declaration, bundle string) {
	t.Helper()
	hash := appbuild.VersionHash(declaration, []byte(bundle), nil)
	path := appbuild.ArtifactPath(filepath.Join(s.Dir, "apps"), name, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(bundle), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.AppApply(name, hash, declaration, ""); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
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
	cli := s.Client()
	const subscribed = `{"name":"ledger","attn_app_api":1,"entrypoint":"src/index.ts","subscribe":[{"events":["ticket.*"]}]}`
	applyAppDeclaration(t, s, cli, "ledger", subscribed, "export default {}")
	const reconciling = `{"name":"digest","attn_app_api":1,"entrypoint":"src/index.ts","reconcile":true,"subscribe":[{"events":["ticket.*"]}]}`
	applyAppDeclaration(t, s, cli, "digest", reconciling, "export default {} // first")
	if _, err := cli.AppSetEnabled("digest", false); err != nil {
		t.Fatal(err)
	}
	applyAppDeclaration(t, s, cli, "digest", reconciling, "export default {} // second")

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
