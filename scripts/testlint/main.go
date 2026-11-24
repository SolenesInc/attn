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

var harnessDirs = []string{"internal/testworld", "internal/fakeagent", "internal/fakessh", "internal/ptyhost/ptyhosttest"}

type rule struct {
	problem string
	find    func(fset *token.FileSet, body ast.Node, name string) []string
	allowed map[string]string
}

var rules = []rule{
	{
		problem: "tests waiting for real time; step a clock or wait on a signal (docs/internal/testing.md)",
		find:    waitsIn,
		allowed: allowedWaits,
	},
	{
		problem: "test code running cargo; read the binary's path from the environment scripts/test-go.sh sets (docs/internal/testing.md)",
		find:    cargoRunsIn,
		allowed: allowedCargo,
	},
}

// Each allowance names a function by file and receiver and says why it may break the rule.
var allowedCargo = map[string]string{}

var allowedWaits = map[string]string{
	"internal/daemon/world_test.go (*world).elapse":                                                                  "moves the synctest clock of a world inBubble; refuses outside a bubble",
	"internal/daemon/desktop_tiles_protocol_test.go (*desktopTilesWorld).deliver":                                    "runs in tileContentWorld's synctest bubble, so the sleep moves its clock past the tile watcher's tick",
	"internal/daemon/exit_screen_wire_test.go TestAnExitReleasesItsRuntimeAtOnceAndReportsTheExitOnceStorageReturns": "runs inBubbleWithAgents, so the sleep moves the synctest clock",
	"internal/daemon/recent_files_wire_test.go (*outpostFiles).answer":                                               "runs in the synctest bubble of a world inBubble, so the bridge round trip it simulates moves the synctest clock",
	"internal/jobs/stores_test.go TestDueJobsRunByPriorityThenByScheduleWithinASecond":                               "runs in onTheJobDatabase's synctest bubble",
	"internal/jobs/stores_test.go TestRetentionTrimsCompletedJobsBySubSecondAgeAndKeepsDeadOnes":                     "runs in onTheJobDatabase's synctest bubble",
	"internal/fakeagent/codex_remote.go (*codexRemote).stayConnected":                                                "the fake Codex's own reconnect backoff, which the real Codex has too",
	"internal/fakeagent/claude_mod.go (*fakeMod).pollLoop":                                                           "the fake Claude mod's own poll retry backoff, which the real mod has too",
	"internal/fakeagent/app_server.go (*Kit).AppServerAfter":                                                         "real-time poll for a launched app-server; replace with a signal",
	"internal/fakeagent/app_server.go (*serverView).begin":                                                           "real-time poll for the app-server socket; replace with a signal",
	"internal/ptybackend/shared_host_integration_test.go waitForSessionIDs":                                          "real-time poll of the shared host; replace with a signal",
	"internal/ptybackend/shared_host_integration_test.go waitForSessionExit":                                         "real-time poll of the shared host; replace with a signal",
	"internal/ptybackend/shared_host_perf_test.go TestSharedHostResourceExperiment":                                  "measures idle CPU over a real two-second window; runs only with ATTN_RESOURCE_PROBE",
}

func main() {
	broken, err := brokenRules()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, r := range rules {
		if violations := broken[r.problem]; len(violations) > 0 {
			fmt.Fprintf(os.Stderr, "%s:\n%s\n", r.problem, strings.Join(violations, "\n"))
		}
	}
	if len(broken) > 0 {
		os.Exit(1)
	}
}

func brokenRules() (map[string][]string, error) {
	paths, err := testFiles()
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no Go test files found; run from the repository root")
	}
	fset := token.NewFileSet()
	broken := make(map[string][]string)
	used := make(map[string]bool)
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, d := range declarations(file) {
			key := filepath.ToSlash(path) + " " + d.name
			for _, r := range rules {
				found := r.find(fset, d.body, d.name)
				if r.allowed[key] != "" && len(found) > 0 {
					used[r.problem+key] = true
					continue
				}
				broken[r.problem] = append(broken[r.problem], found...)
			}
		}
	}
	for _, r := range rules {
		for key := range r.allowed {
			if !used[r.problem+key] {
				broken[r.problem] = append(broken[r.problem], key+": allowance names no function that breaks this rule; remove it")
			}
		}
		if len(broken[r.problem]) == 0 {
			delete(broken, r.problem)
		}
		sort.Strings(broken[r.problem])
	}
	return broken, nil
}

type declaration struct {
	name string
	body ast.Node
}

func declarations(file *ast.File) []declaration {
	var found []declaration
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Body != nil {
				found = append(found, declaration{funcDeclName(decl), decl.Body})
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, v := range value.Values {
					name := "_"
					if i < len(value.Names) {
						name = value.Names[i].Name
					}
					found = append(found, declaration{name, v})
				}
			}
		}
	}
	return found
}

func cargoRunsIn(fset *token.FileSet, body ast.Node, name string) []string {
	var violations []string
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		program := -1
		switch {
		case isCall(call, "exec", "Command"):
			program = 0
		case isCall(call, "exec", "CommandContext"):
			program = 1
		}
		if program >= 0 && len(call.Args) > program {
			if literal, ok := call.Args[program].(*ast.BasicLit); ok && literal.Kind == token.STRING && literal.Value == `"cargo"` {
				violations = append(violations, fmt.Sprintf("%s in %s: runs cargo", fset.Position(call.Pos()), name))
			}
		}
		return true
	})
	return violations
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
			if deadline := selectDeadline(clause.Comm); deadline != nil {
				ast.Inspect(deadline, inspect)
			}
			for _, stmt := range clause.Body {
				ast.Inspect(stmt, inspect)
			}
			return false
		}
		if receive, ok := node.(*ast.UnaryExpr); ok && receive.Op == token.ARROW {
			if call, ok := receive.X.(*ast.CallExpr); ok && isCall(call, "time", "After") {
				violations = append(violations, fmt.Sprintf("%s in %s: <-time.After outside a select", fset.Position(call.Pos()), name))
				return false
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isCall(call, "synctest", "Test") {
			return false
		}
		if isCall(call, "time", "After") && (len(call.Args) != 1 || !isHangGuard(call.Args[0])) {
			violations = append(violations, fmt.Sprintf("%s in %s: time.After deadline other than fakeagent.HangGuard", fset.Position(call.Pos()), name))
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

func selectDeadline(comm ast.Stmt) *ast.CallExpr {
	var received ast.Expr
	switch comm := comm.(type) {
	case *ast.ExprStmt:
		received = comm.X
	case *ast.AssignStmt:
		if len(comm.Rhs) == 1 {
			received = comm.Rhs[0]
		}
	}
	receive, ok := received.(*ast.UnaryExpr)
	if !ok || receive.Op != token.ARROW {
		return nil
	}
	call, ok := receive.X.(*ast.CallExpr)
	if !ok || !isCall(call, "time", "After") {
		return nil
	}
	return call
}

func isHangGuard(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return isHangGuard(expr.X)
	case *ast.Ident:
		return expr.Name == "HangGuard"
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		return ok && pkg.Name == "fakeagent" && expr.Sel.Name == "HangGuard"
	case *ast.BinaryExpr:
		_, leftLiteral := expr.X.(*ast.BasicLit)
		_, rightLiteral := expr.Y.(*ast.BasicLit)
		return expr.Op == token.MUL && (leftLiteral && isHangGuard(expr.Y) || rightLiteral && isHangGuard(expr.X))
	}
	return false
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
