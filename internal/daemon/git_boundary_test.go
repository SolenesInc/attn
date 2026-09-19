package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDaemonGitExecutionBoundary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	pureGitCalls := map[string]bool{
		"CanonicalizePath":         true,
		"ExpandPath":               true,
		"GenerateWorktreePath":     true,
		"NewestTreeModTimeContext": true,
		"RepositoryCacheKey":       true,
		"SetLogFunc":               true,
	}
	directAdapterCalls := map[string]map[string]bool{
		"github.com/victorarias/attn/internal/present": {
			"Pin": true, "ResolveAnnotations": true, "RenderFeedback": true,
		},
		"github.com/victorarias/attn/internal/automode": {
			"DetectFromRepo": true, "LoadRepositoryRules": true,
		},
	}

	files := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(files, name, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		imports := make(map[string]string)
		for _, spec := range parsed.Imports {
			path, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				t.Fatal(unquoteErr)
			}
			alias := filepath.Base(path)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			imports[alias] = path
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			path := imports[ident.Name]
			switch path {
			case "github.com/victorarias/attn/internal/git":
				if pureGitCalls[selector.Sel.Name] || name == "git_executor.go" && selector.Sel.Name == "NewClient" {
					return true
				}
				t.Errorf("%s calls package Git helper %s.%s outside executor admission", files.Position(call.Pos()), ident.Name, selector.Sel.Name)
			case "os/exec":
				if rawGitCommand(call) {
					t.Errorf("%s launches Git through os/exec", files.Position(call.Pos()))
				}
			default:
				if directAdapterCalls[path][selector.Sel.Name] {
					t.Errorf("%s calls direct Git adapter %s.%s", files.Position(call.Pos()), ident.Name, selector.Sel.Name)
				}
			}
			return true
		})
	}
}

func rawGitCommand(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	command, ok := stringLiteral(call.Args[0])
	if !ok {
		return false
	}
	if command == "git" {
		return true
	}
	if command != "/usr/bin/env" || len(call.Args) < 2 {
		return false
	}
	nested, ok := stringLiteral(call.Args[1])
	return ok && nested == "git"
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}
