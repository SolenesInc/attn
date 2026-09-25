package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os/exec"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const modulePath = "github.com/victorarias/attn"

type program struct {
	root     string
	fset     *token.FileSet
	pkgs     []*packages.Package
	ssa      *ssa.Program
	graph    *callgraph.Graph
	byName   map[string]*ssa.Function
	bySyntax map[ast.Node]*ssa.Function
}

func loadProgram(root string) (*program, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: root,
	}
	pkgs, err := packages.Load(cfg, "./cmd/...", "./internal/...")
	if err != nil {
		return nil, err
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		return nil, fmt.Errorf("%d package errors", n)
	}
	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	all := ssautil.AllFunctions(prog)
	graph := vta.CallGraph(all, cha.CallGraph(prog))
	graph.DeleteSyntheticNodes()

	p := &program{
		root:     root,
		fset:     prog.Fset,
		ssa:      prog,
		graph:    graph,
		byName:   map[string]*ssa.Function{},
		bySyntax: map[ast.Node]*ssa.Function{},
	}
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		if strings.HasPrefix(pkg.PkgPath, modulePath) {
			p.pkgs = append(p.pkgs, pkg)
		}
	})
	for fn := range all {
		if !isOwn(fn) {
			continue
		}
		p.byName[fn.String()] = fn
		if syntax := fn.Syntax(); syntax != nil {
			p.bySyntax[syntax] = fn
		}
	}
	return p, nil
}

func isOwn(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	pkg := fn.Pkg
	if pkg == nil && fn.Origin() != nil {
		pkg = fn.Origin().Pkg
	}
	return pkg != nil && strings.HasPrefix(pkg.Pkg.Path(), modulePath) && fn.Syntax() != nil
}

func (p *program) funcFor(info *types.Info, expr ast.Expr) *ssa.Function {
	switch e := ast.Unparen(expr).(type) {
	case *ast.FuncLit:
		return p.bySyntax[e]
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[e]; ok {
			if obj, ok := sel.Obj().(*types.Func); ok {
				return p.ssa.FuncValue(obj)
			}
		}
		if obj, ok := info.Uses[e.Sel].(*types.Func); ok {
			return p.ssa.FuncValue(obj)
		}
	case *ast.Ident:
		if obj, ok := info.Uses[e].(*types.Func); ok {
			return p.ssa.FuncValue(obj)
		}
	}
	return nil
}

func git(root string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func githubURL(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")
	if rest, ok := strings.CutPrefix(remote, "git@github.com:"); ok {
		return "https://github.com/" + rest
	}
	if strings.HasPrefix(remote, "https://github.com/") {
		return remote
	}
	return ""
}

func (p *program) relPos(pos token.Pos) string {
	position := p.fset.Position(pos)
	return fmt.Sprintf("%s:%d", p.rel(position.Filename), position.Line)
}

func (p *program) rel(filename string) string {
	return strings.TrimPrefix(strings.TrimPrefix(filename, p.root), "/")
}
