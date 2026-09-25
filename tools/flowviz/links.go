package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

type commandRoute struct {
	Command   string
	Handler   *ssa.Function
	Transport string
	Switch    string
}

type busConsumer struct {
	Patterns []string
	Handler  *ssa.Function
	At       string
}

func (c busConsumer) matches(fact string) bool {
	for _, pattern := range c.Patterns {
		switch {
		case pattern == "*" || pattern == "":
			return true
		case strings.HasSuffix(pattern, ".*") && strings.HasPrefix(fact, pattern[:len(pattern)-1]):
			return true
		case pattern == fact:
			return true
		}
	}
	return false
}

func stringConst(info *types.Info, expr ast.Expr) (string, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

func daemonPackages(p *program) []*packages.Package {
	var out []*packages.Package
	for _, pkg := range p.pkgs {
		if pkg.PkgPath == modulePath+"/internal/daemon" {
			out = append(out, pkg)
		}
	}
	return out
}

func findDispatch(p *program) (map[string][]commandRoute, error) {
	routes := map[string][]commandRoute{}
	for _, pkg := range daemonPackages(p) {
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					clause, ok := n.(*ast.CaseClause)
					if !ok {
						return true
					}
					handler := firstOwnCall(p, pkg.TypesInfo, clause.Body)
					if handler == nil {
						return true
					}
					for _, expr := range clause.List {
						cmd, ok := stringConst(pkg.TypesInfo, expr)
						if !ok || !isCommandConst(expr) {
							continue
						}
						routes[cmd] = append(routes[cmd], commandRoute{
							Command:   cmd,
							Handler:   handler,
							Transport: transportFor(p.fset.Position(clause.Pos()).Filename),
							Switch:    fn.Name.Name,
						})
					}
					return true
				})
			}
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no command dispatch found")
	}
	return routes, nil
}

func isCommandConst(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && strings.HasPrefix(sel.Sel.Name, "Cmd")
}

func transportFor(filename string) string {
	if strings.HasSuffix(filename, "websocket.go") {
		return "WebSocket"
	}
	return "Unix socket"
}

func firstOwnCall(p *program, info *types.Info, body []ast.Stmt) *ssa.Function {
	var found *ssa.Function
	for _, stmt := range body {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if found != nil {
				return false
			}
			if call, ok := n.(*ast.CallExpr); ok {
				if fn := p.funcFor(info, call.Fun); isOwn(fn) {
					found = fn
					return false
				}
			}
			return true
		})
		if found != nil {
			return found
		}
	}
	return nil
}

func findBusConsumers(p *program) []busConsumer {
	var out []busConsumer
	for _, pkg := range p.pkgs {
		info := pkg.TypesInfo
		for _, file := range pkg.Syntax {
			var stack []ast.Node
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if lit, ok := n.(*ast.CompositeLit); ok && isBusFilter(info, lit) {
					if patterns := filterPatterns(info, lit); len(patterns) > 0 {
						for _, handler := range handlersBeside(p, info, stack) {
							out = append(out, busConsumer{Patterns: patterns, Handler: handler, At: p.relPos(lit.Pos())})
						}
					}
				}
				stack = append(stack, n)
				return true
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

func isBusFilter(info *types.Info, lit *ast.CompositeLit) bool {
	named, ok := info.TypeOf(lit).(*types.Named)
	return ok && named.Obj().Name() == "Filter" && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == modulePath+"/internal/bus"
}

func filterPatterns(info *types.Info, lit *ast.CompositeLit) []string {
	var out []string
	for _, elt := range lit.Elts {
		if s, ok := stringConst(info, elt); ok {
			out = append(out, s)
		}
	}
	return out
}

func handlersBeside(p *program, info *types.Info, ancestors []ast.Node) []*ssa.Function {
	var out []*ssa.Function
	collect := func(expr ast.Expr) {
		if _, isFunc := info.TypeOf(expr).Underlying().(*types.Signature); !isFunc {
			return
		}
		if fn := p.funcFor(info, expr); isOwn(fn) {
			out = append(out, fn)
		}
	}
	if len(ancestors) == 0 {
		return nil
	}
	switch parent := ancestors[len(ancestors)-1].(type) {
	case *ast.CallExpr:
		for _, arg := range parent.Args {
			collect(arg)
		}
	case *ast.KeyValueExpr:
		if len(ancestors) < 2 {
			return nil
		}
		if owner, ok := ancestors[len(ancestors)-2].(*ast.CompositeLit); ok {
			for _, elt := range owner.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok && kv != parent {
					collect(kv.Value)
				}
			}
		}
	}
	return out
}
