package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	daemonPkg = "./internal/daemon"
	daemonDir = "internal/daemon"
)

type trackedField struct {
	pkgSuffix string
	typeName  string
	field     string
}

var remotePathFields = []trackedField{
	{pkgSuffix: "/internal/protocol", typeName: "Session", field: "Directory"},
	{pkgSuffix: "/internal/protocol", typeName: "SessionLedgerEntry", field: "Directory"},
	{pkgSuffix: "/internal/garden", typeName: "Execution", field: "Cwd"},
	{pkgSuffix: "/internal/garden", typeName: "Execution", field: "RepositoryRoot"},
	{pkgSuffix: "/internal/garden", typeName: "RepoPath", field: "Path"},
	{pkgSuffix: "/internal/store", typeName: "SessionConversation", field: "TranscriptPath"},
}

type sinkSpec struct {
	pkgPath   string
	pkgSuffix string
	name      string
}

var localFSSinks = []sinkSpec{
	{pkgPath: "os", name: "Stat"},
	{pkgPath: "os", name: "Lstat"},
	{pkgPath: "os", name: "Open"},
	{pkgSuffix: "/internal/git", name: "ReadRepoTraits"},
	{pkgSuffix: "/internal/daemon", name: "repoTraits"},
}

var allowed = map[string]string{
	"internal/daemon/session_transcript.go (*Daemon).openInspectableTranscript": "checks that the session is local or copied from the home before opening the session's transcript path",
	"internal/daemon/terminal_machine.go (*Daemon).startTerminal":               "runs on the machine that owns the session's terminal",
}

func main() {
	violations, err := remotePathViolations()
	if err != nil {
		fmt.Fprintln(os.Stderr, "remotepathlint:", err)
		os.Exit(1)
	}
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "filesystem path lookup without verified machine ownership:\n%s\n", strings.Join(violations, "\n"))
		os.Exit(1)
	}
}

func remotePathViolations() ([]string, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
	}
	pkgs, err := packages.Load(cfg, daemonPkg)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no package found at %s; run from the repository root", daemonPkg)
	}
	var loadErrors []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			loadErrors = append(loadErrors, e.Error())
		}
	}
	if len(loadErrors) > 0 {
		return nil, fmt.Errorf("the package does not type-check:\n  %s", strings.Join(loadErrors, "\n  "))
	}

	var violations []string
	used := map[string]bool{}
	for _, pkg := range pkgs {
		checkedFiles := make(map[string]bool, len(pkg.Syntax))
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.Position(file.Pos()).Filename
			relPath, err := filepath.Rel(root, filename)
			if err != nil {
				return nil, err
			}
			relPath = filepath.ToSlash(relPath)
			base := filepath.Base(relPath)
			if !strings.HasPrefix(relPath, daemonDir+"/") || strings.HasSuffix(base, "_test.go") || base == "machine_path.go" {
				continue
			}
			checkedFiles[filename] = true
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				name := funcDeclName(fn)
				key := relPath + " " + name
				c := &funcChecker{
					fset:    pkg.Fset,
					info:    pkg.TypesInfo,
					fnName:  name,
					tainted: map[types.Object]string{},
				}
				c.walkStmt(fn.Body)
				if len(c.violations) == 0 {
					continue
				}
				if allowed[key] != "" {
					used[key] = true
					continue
				}
				violations = append(violations, c.violations...)
			}
		}
		for ident, obj := range pkg.TypesInfo.Uses {
			fn, ok := obj.(*types.Func)
			if !ok || !isRawSymlinkResolver(fn) {
				continue
			}
			where := pkg.Fset.Position(ident.Pos())
			if checkedFiles[where.Filename] {
				violations = append(violations, fmt.Sprintf(
					"%s: uses %s.%s outside machine_path.go; use the path's machine to resolve it",
					where, fn.Pkg().Name(), fn.Name(),
				))
			}
		}
	}
	for key := range allowed {
		if !used[key] {
			violations = append(violations, key+": allowance names no function that breaks this rule; remove it")
		}
	}
	sort.Strings(violations)
	return violations, nil
}

type funcChecker struct {
	fset       *token.FileSet
	info       *types.Info
	fnName     string
	tainted    map[types.Object]string
	violations []string
}

func (c *funcChecker) walkStmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		for _, item := range s.List {
			c.walkStmt(item)
		}
	case *ast.DeclStmt:
		gen, ok := s.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, val := range vs.Values {
				c.inspectExpr(val)
			}
			if len(vs.Values) == len(vs.Names) {
				for i, ident := range vs.Names {
					c.assignVar(ident, c.taintOf(vs.Values[i]))
				}
			}
		}
	case *ast.AssignStmt:
		for _, rhs := range s.Rhs {
			c.inspectExpr(rhs)
		}
		if len(s.Lhs) == len(s.Rhs) {
			for i, lhs := range s.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					c.assignVar(ident, c.taintOf(s.Rhs[i]))
				}
			}
		}
	case *ast.ExprStmt:
		c.inspectExpr(s.X)
	case *ast.ReturnStmt:
		for _, res := range s.Results {
			c.inspectExpr(res)
		}
	case *ast.DeferStmt:
		c.inspectExpr(s.Call)
	case *ast.GoStmt:
		c.inspectExpr(s.Call)
	case *ast.SendStmt:
		c.inspectExpr(s.Chan)
		c.inspectExpr(s.Value)
	case *ast.IfStmt:
		c.walkStmt(s.Init)
		c.inspectExpr(s.Cond)
		c.walkStmt(s.Body)
		c.walkStmt(s.Else)
	case *ast.ForStmt:
		c.walkStmt(s.Init)
		c.inspectExpr(s.Cond)
		c.walkStmt(s.Post)
		c.walkStmt(s.Body)
	case *ast.RangeStmt:
		c.inspectExpr(s.X)
		if ident, ok := s.Value.(*ast.Ident); ok {
			c.assignVar(ident, c.taintOf(s.X))
		}
		c.walkStmt(s.Body)
	case *ast.SwitchStmt:
		c.walkStmt(s.Init)
		c.inspectExpr(s.Tag)
		c.walkStmt(s.Body)
	case *ast.TypeSwitchStmt:
		c.walkStmt(s.Init)
		c.walkStmt(s.Assign)
		c.walkStmt(s.Body)
	case *ast.CaseClause:
		for _, expr := range s.List {
			c.inspectExpr(expr)
		}
		for _, item := range s.Body {
			c.walkStmt(item)
		}
	case *ast.SelectStmt:
		c.walkStmt(s.Body)
	case *ast.CommClause:
		c.walkStmt(s.Comm)
		for _, item := range s.Body {
			c.walkStmt(item)
		}
	case *ast.LabeledStmt:
		c.walkStmt(s.Stmt)
	}
}

func (c *funcChecker) assignVar(ident *ast.Ident, source string) {
	if ident == nil || ident.Name == "_" {
		return
	}
	obj := c.info.ObjectOf(ident)
	if obj == nil {
		return
	}
	if source == "" {
		delete(c.tainted, obj)
		return
	}
	c.tainted[obj] = source
}

func (c *funcChecker) inspectExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			c.walkStmt(n.Body)
			return false
		case *ast.CallExpr:
			if sink, ok := c.sinkName(n); ok {
				for _, arg := range n.Args {
					if source := c.taintOf(arg); source != "" {
						c.violations = append(c.violations, fmt.Sprintf(
							"%s in %s: passes %s to %s; guard with d.localDirectory, machine.local, or machine.normalize",
							c.fset.Position(n.Pos()), c.fnName, source, sink,
						))
					}
				}
			}
		}
		return true
	})
}

func isRawSymlinkResolver(fn *types.Func) bool {
	if fn.Pkg() == nil {
		return false
	}
	pkg, name := fn.Pkg().Path(), fn.Name()
	return pkg == "path/filepath" && name == "EvalSymlinks" ||
		strings.HasSuffix(pkg, "/internal/git") && name == "CanonicalizePath"
}

func (c *funcChecker) taintOf(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return c.taintOf(e.X)
	case *ast.BinaryExpr:
		if source := c.taintOf(e.X); source != "" {
			return source
		}
		return c.taintOf(e.Y)
	case *ast.Ident:
		if obj := c.info.ObjectOf(e); obj != nil {
			return c.tainted[obj]
		}
	case *ast.SelectorExpr:
		if source := c.trackedFieldName(e); source != "" {
			return source
		}
	case *ast.CompositeLit:
		for _, elt := range e.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}
			if source := c.taintOf(elt); source != "" {
				return source
			}
		}
	case *ast.CallExpr:
		if isPassThroughCall(c.info, e) {
			for _, arg := range e.Args {
				if source := c.taintOf(arg); source != "" {
					return source
				}
			}
		}
	}
	return ""
}

func isPassThroughCall(info *types.Info, call *ast.CallExpr) bool {
	fn := calledFunc(info, call)
	if fn == nil || fn.Pkg() == nil {
		return false
	}
	pkg, name := fn.Pkg().Path(), fn.Name()
	switch pkg {
	case "strings":
		return name == "TrimSpace" || name == "TrimRight" || name == "TrimPrefix" || name == "TrimSuffix"
	case "path/filepath":
		return name == "Clean" || name == "Join" || name == "Dir" || name == "Abs"
	}
	return strings.HasSuffix(pkg, "/internal/protocol") && name == "Deref"
}

func (c *funcChecker) trackedFieldName(sel *ast.SelectorExpr) string {
	selection := c.info.Selections[sel]
	if selection == nil || selection.Kind() != types.FieldVal {
		return ""
	}
	named := namedType(selection.Recv())
	if named == nil || named.Obj().Pkg() == nil {
		return ""
	}
	pkgPath := named.Obj().Pkg().Path()
	typeName := named.Obj().Name()
	field := sel.Sel.Name
	for _, tracked := range remotePathFields {
		if strings.HasSuffix(pkgPath, tracked.pkgSuffix) && typeName == tracked.typeName && field == tracked.field {
			return named.Obj().Pkg().Name() + "." + typeName + "." + field
		}
	}
	return ""
}

func (c *funcChecker) sinkName(call *ast.CallExpr) (string, bool) {
	fn := calledFunc(c.info, call)
	if fn == nil || fn.Pkg() == nil {
		return "", false
	}
	pkgPath, name := fn.Pkg().Path(), fn.Name()
	for _, sink := range localFSSinks {
		if name != sink.name {
			continue
		}
		if sink.pkgPath != "" && pkgPath == sink.pkgPath {
			return fn.Pkg().Name() + "." + name, true
		}
		if sink.pkgSuffix != "" && strings.HasSuffix(pkgPath, sink.pkgSuffix) {
			if sink.pkgSuffix == "/internal/daemon" {
				return name, true
			}
			return fn.Pkg().Name() + "." + name, true
		}
	}
	return "", false
}

func calledFunc(info *types.Info, call *ast.CallExpr) *types.Func {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		fn, _ := info.Uses[fun].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := info.Uses[fun.Sel].(*types.Func)
		return fn
	case *ast.IndexExpr:
		return calledFunc(info, &ast.CallExpr{Fun: fun.X})
	case *ast.IndexListExpr:
		return calledFunc(info, &ast.CallExpr{Fun: fun.X})
	}
	return nil
}

func namedType(t types.Type) *types.Named {
	for {
		switch u := t.(type) {
		case *types.Pointer:
			t = u.Elem()
		case *types.Alias:
			t = types.Unalias(u)
		case *types.Named:
			return u
		default:
			return nil
		}
	}
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
