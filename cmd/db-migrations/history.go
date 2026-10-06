package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"golang.org/x/tools/go/ast/astutil"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func gitOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return data, nil
}

func repositoryRoot(cwd string) (string, error) {
	data, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(string(data)), err
}

func checkHistory(root, base string, stdout io.Writer) error {
	hash, err := gitOutput(root, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return err
	}
	base = strings.TrimSpace(string(hash))
	paths, err := gitOutput(root, "ls-tree", "-r", "--name-only", "-z", base, "--", "internal/store")
	if err != nil {
		return err
	}
	baseline := make(map[string][]byte)
	for _, path := range strings.Split(string(paths), "\x00") {
		if path == "" {
			continue
		}
		sqlMigration := strings.HasPrefix(path, "internal/store/migrations/") && strings.HasSuffix(path, ".sql")
		goMigration := filepath.Dir(path) == "internal/store" && goMigrationPattern.MatchString(filepath.Base(path))
		goSource := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
		if !sqlMigration && !goSource {
			continue
		}
		data, err := gitOutput(root, "show", base+":"+path)
		if err != nil {
			return err
		}
		if sqlMigration || goMigration {
			current, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				return fmt.Errorf("merged migration %s must remain at the same path: %w", path, err)
			}
			if !bytes.Equal(data, current) {
				return fmt.Errorf("merged migration %s was edited; add a new timestamped migration", path)
			}
		}
		if goSource {
			baseline[path] = data
		}
	}
	if _, frozen := baseline["internal/store/migrations.go"]; !frozen {
		_, err := fmt.Fprintln(stdout, "Legacy ladder freeze starts when the timestamped runner is merged.")
		return err
	}
	expected, names, err := legacyDefinitions(baseline)
	if err != nil {
		return fmt.Errorf("reading frozen migration definitions at %s: %w", base, err)
	}
	current := make(map[string][]byte)
	entries, err := os.ReadDir(filepath.Join(root, "internal", "store"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join("internal", "store", entry.Name())
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		current[path] = data
	}
	actual, _, err := legacyDefinitions(current)
	if err != nil {
		return fmt.Errorf("reading current legacy migration definitions: %w", err)
	}
	for _, name := range names {
		if !bytes.Equal(expected[name], actual[name]) {
			return fmt.Errorf("frozen legacy migration definition %s was changed or removed; add a timestamped migration", name)
		}
	}
	_, err = fmt.Fprintln(stdout, "Merged migration files and frozen legacy definitions are unchanged.")
	return err
}

func legacyDefinitions(files map[string][]byte) (map[string][]byte, []string, error) {
	set := token.NewFileSet()
	declarations := make(map[string]ast.Node)
	var ladder ast.Node
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file, err := parser.ParseFile(set, path, files[path], 0)
		if err != nil {
			return nil, nil, err
		}
		for _, declaration := range file.Decls {
			switch d := declaration.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					declarations[d.Name.Name] = d
				}
			case *ast.GenDecl:
				if d.Tok != token.CONST && d.Tok != token.VAR && d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					if definition, ok := spec.(*ast.TypeSpec); ok {
						declarations[definition.Name.Name] = d
						continue
					}
					value := spec.(*ast.ValueSpec)
					for _, name := range value.Names {
						if name.Name != "_" {
							declarations[name.Name] = d
						}
						if path == "internal/store/sqlite.go" && name.Name == "migrations" {
							ladder = value
						}
					}
				}
			}
		}
	}
	if ladder == nil {
		return nil, nil, fmt.Errorf("internal/store/sqlite.go has no legacy migrations ladder")
	}
	selected := map[string]ast.Node{"migrations": ladder}
	for _, name := range []string{"baseSchema", "lastLegacyVersion"} {
		d, ok := declarations[name]
		if !ok {
			return nil, nil, fmt.Errorf("missing frozen legacy definition %s", name)
		}
		selected[name] = d
	}
	pending := make([]ast.Node, 0, len(selected))
	for _, node := range selected {
		pending = append(pending, node)
	}
	for i := 0; i < len(pending); i++ {
		inspectReferences(pending[i], func(id *ast.Ident) {
			d, found := declarations[id.Name]
			if !found || !refersToDeclaration(id, d) {
				return
			}
			if _, known := selected[id.Name]; !known {
				selected[id.Name] = d
				pending = append(pending, d)
			}
		})
	}
	result := make(map[string][]byte)
	names := make([]string, 0, len(selected))
	for name, node := range selected {
		var data bytes.Buffer
		node = migrationStringIdentities(node)
		if err := format.Node(&data, set, node); err != nil {
			return nil, nil, err
		}
		result[name] = data.Bytes()
		names = append(names, name)
	}
	sort.Strings(names)
	return result, names, nil
}

func inspectReferences(node ast.Node, visit func(*ast.Ident)) {
	ast.Inspect(node, func(child ast.Node) bool {
		switch n := child.(type) {
		case *ast.SelectorExpr:
			inspectReferences(n.X, visit)
			return false
		case *ast.Ident:
			visit(n)
		}
		return true
	})
}

func refersToDeclaration(id *ast.Ident, node ast.Node) bool {
	if id.Name == "_" {
		return false
	}
	if id.Obj == nil {
		return true
	}
	switch declaration := node.(type) {
	case *ast.FuncDecl:
		return id.Obj.Decl == declaration
	case *ast.GenDecl:
		for _, spec := range declaration.Specs {
			if id.Obj.Decl == spec {
				return true
			}
		}
	}
	return false
}

func migrationStringIdentities(node ast.Node) ast.Node {
	return astutil.Apply(node, func(cursor *astutil.Cursor) bool {
		switch n := cursor.Node().(type) {
		case *ast.CallExpr:
			if migrationIdentityCast(node, n) {
				cursor.Replace(n.Args[0])
			}
		case *ast.SelectorExpr:
			if migrationIdentityType(n) {
				cursor.Replace(&ast.Ident{Name: "string", NamePos: n.Pos()})
			}
		}
		return true
	}, nil)
}

func migrationIdentityType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Name == "protocol" && (selector.Sel.Name == "SessionID" || selector.Sel.Name == "TerminalID")
}

func migrationIdentityCast(declaration ast.Node, call *ast.CallExpr) bool {
	function, ok := declaration.(*ast.FuncDecl)
	if !ok || len(call.Args) != 1 || call.Ellipsis.IsValid() || !migrationIdentityType(call.Fun) {
		return false
	}
	selector := call.Fun.(*ast.SelectorExpr)
	var expected string
	switch function.Name.Name {
	case "convertLegacyWorkspaces":
		if selector.Sel.Name == "SessionID" {
			expected = "candidate.sessionID"
		}
	case "legacyDesktopPane":
		if selector.Sel.Name == "SessionID" {
			expected = "pane.SessionID"
		}
		if selector.Sel.Name == "TerminalID" {
			expected = "cmp.Or(pane.RuntimeID, pane.SessionID)"
		}
	}
	if expected == "" {
		return false
	}
	var expression bytes.Buffer
	if err := format.Node(&expression, token.NewFileSet(), call.Args[0]); err != nil {
		return false
	}
	return expression.String() == expected
}
