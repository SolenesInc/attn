package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var harnessDirs = []string{"internal/testworld", "internal/fakeagent", "internal/fakessh"}

// Each allowance names a function by file and receiver and says why it may wait real time.
var allowed = map[string]string{
	"internal/daemon/world_test.go (*world).elapse":                                                                  "moves the synctest clock of a world inBubble; refuses outside a bubble",
	"internal/daemon/exit_screen_wire_test.go TestAnExitReleasesItsRuntimeAtOnceAndReportsTheExitOnceStorageReturns": "runs inBubbleWithAgents, so the sleep moves the synctest clock",
	"internal/jobs/stores_test.go TestDueJobsRunByPriorityThenByScheduleWithinASecond":                               "runs in onTheJobDatabase's synctest bubble",
	"internal/jobs/stores_test.go TestRetentionTrimsCompletedJobsBySubSecondAgeAndKeepsDeadOnes":                     "runs in onTheJobDatabase's synctest bubble",
	"cmd/attn/daemon_stop_stack_test.go holdDaemonLockForever":                                                       "a helper process that holds a lock until it is killed; nothing waits on the sleep",
	"cmd/attn/instance_app_stop_test.go TestInstanceAppStopHelperProcess":                                            "a helper process standing in for the app until it is signalled; nothing waits on the sleep",
	"internal/fakeagent/codex_remote.go (*codexRemote).stayConnected":                                                "the fake Codex's own reconnect backoff, which the real Codex has too",
	"internal/fakeagent/nisse.go (*nisseTUI).begin":                                                                  "real-time poll for the Nisse server socket; replace with a signal",
	"cmd/attn/instance_app_stop_test.go spawnStubbornApp":                                                            "real-time poll for the helper's ready file; replace with a signal",
	"internal/procreap/procreap_test.go orphan":                                                                      "real-time poll for a ready file; replace with a signal",
	"internal/procreap/procreap_test.go TestReapCooperativeTeardownReachesTheDetachedChild":                          "real-time poll for a child pid file; replace with a signal",
	"internal/procreap/procreap_test.go TestReapNeverGroupSignalsASharedGroupEntry":                                  "real-time poll for a ready file; replace with a signal",
	"internal/ptybackend/integration_support_test.go waitForPIDsGone":                                                "real-time poll for processes to exit; replace with a signal",
	"internal/ptybackend/shared_host_integration_test.go waitForSessionIDs":                                          "real-time poll of the shared host; replace with a signal",
	"internal/ptybackend/shared_host_integration_test.go waitForSessionExit":                                         "real-time poll of the shared host; replace with a signal",
	"internal/ptybackend/worker_test.go TestWorkerBackend_Spawn_CleansUpUnreadyWorkerProcess":                        "real-time poll for a worker pid file and its exit; replace with a signal",
	"internal/supervise/supervise_test.go waitFor":                                                                   "real-time poll for a goroutine to react to the fake clock; replace with a signal",
	"internal/ptybackend/shared_host_perf_test.go TestSharedHostResourceExperiment":                                  "measures idle CPU over a real two-second window; runs only with ATTN_RESOURCE_PROBE",
	"internal/testworld/stack_kill.go (*Stack).Reboot":                                                               "real-time poll for SIGKILLed processes to be gone; replace with a signal",
}

func main() {
	violations, err := realTimeWaits()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "tests waiting for real time; step a clock or wait on a signal (docs/testing.md):\n%s\n", strings.Join(violations, "\n"))
		os.Exit(1)
	}
}

func realTimeWaits() ([]string, error) {
	paths, err := testFiles()
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no Go test files found; run from the repository root")
	}
	fset := token.NewFileSet()
	var violations []string
	used := make(map[string]bool)
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := funcDeclName(fn)
			waits := waitsIn(fset, fn.Body, name)
			if key := filepath.ToSlash(path) + " " + name; allowed[key] != "" && len(waits) > 0 {
				used[key] = true
				continue
			}
			violations = append(violations, waits...)
		}
	}
	for key := range allowed {
		if !used[key] {
			violations = append(violations, key+": allowance names no function that waits real time; remove it")
		}
	}
	sort.Strings(violations)
	return violations, nil
}

func testFiles() ([]string, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("go", "list", "-e", "-f", `{{.Dir}}{{range .TestGoFiles}} {{.}}{{end}}{{range .XTestGoFiles}} {{.}}{{end}}{{range .IgnoredGoFiles}} {{.}}{{end}}{{range .GoFiles}} {{.}}{{end}}`, "./...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for _, name := range fields[1:] {
			path, err := filepath.Rel(root, filepath.Join(fields[0], name))
			if err != nil {
				return nil, err
			}
			if strings.HasSuffix(name, "_test.go") || inHarness(path) {
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

func inHarness(path string) bool {
	path = filepath.ToSlash(path)
	for _, dir := range harnessDirs {
		if strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

func waitsIn(fset *token.FileSet, body ast.Node, name string) []string {
	var violations []string
	var inspect func(ast.Node) bool
	inspect = func(node ast.Node) bool {
		if clause, ok := node.(*ast.CommClause); ok {
			for _, stmt := range clause.Body {
				ast.Inspect(stmt, inspect)
			}
			return false
		}
		if receive, ok := node.(*ast.UnaryExpr); ok && receive.Op == token.ARROW {
			if call, ok := receive.X.(*ast.CallExpr); ok && isCall(call, "time", "After") {
				violations = append(violations, fmt.Sprintf("%s in %s: <-time.After outside a select", fset.Position(call.Pos()), name))
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isCall(call, "synctest", "Test") {
			return false
		}
		for _, wait := range []string{"Sleep", "Tick", "NewTicker"} {
			if isCall(call, "time", wait) {
				violations = append(violations, fmt.Sprintf("%s in %s: time.%s", fset.Position(call.Pos()), name, wait))
			}
		}
		return true
	}
	ast.Inspect(body, inspect)
	return violations
}

func isCall(call *ast.CallExpr, pkg, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

func funcDeclName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return "(" + receiverName(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}

func receiverName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.StarExpr:
		return "*" + receiverName(expr.X)
	case *ast.IndexExpr:
		return receiverName(expr.X)
	case *ast.IndexListExpr:
		return receiverName(expr.X)
	case *ast.Ident:
		return expr.Name
	}
	return "?"
}
