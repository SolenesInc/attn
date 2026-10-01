package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestTheDaemonStartsGoroutinesAndTimersOnlyThroughItsLifetime(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var violations []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || path == "lifetime.go" {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			name, body := "package-level declaration", ast.Node(decl)
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if fn.Body == nil {
					continue
				}
				name, body = funcDeclName(fn), fn.Body
			}
			ast.Inspect(body, func(node ast.Node) bool {
				var problem string
				switch node := node.(type) {
				case *ast.GoStmt:
					problem = "bare go statement: start it with d.life.Go so Daemon.stop waits for it (goTransport only for I/O that ends with its peer)"
				case *ast.CallExpr:
					if isTimeAfterFunc(node) {
						problem = "time.AfterFunc: use d.life.AfterFunc so a timer that fires during stop does nothing"
					} else if isUnownedGoCall(node) {
						problem = "group.Go: use d.life.Go so Daemon.stop waits for it, or runJoined for children joined before their shutdown-drained caller returns"
					}
				}
				if problem == "" {
					return true
				}
				violations = append(violations, fset.Position(node.Pos()).String()+" in "+name+": "+problem)
				return true
			})
		}
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("goroutines and timers outside the daemon lifetime (lifetime.go):\n%s", strings.Join(violations, "\n"))
	}
}

func isUnownedGoCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Go" {
		return false
	}
	owner, ok := sel.X.(*ast.SelectorExpr)
	return !ok || owner.Sel.Name != "life"
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

func isTimeAfterFunc(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "AfterFunc" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "time"
}
