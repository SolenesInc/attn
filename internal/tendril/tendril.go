package tendril

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
)

const MaxSourceBytes = 1 << 20

func (e *Env) Compile(filename, src string) (*Program, error) {
	if len(src) > MaxSourceBytes {
		return nil, fmt.Errorf("%s: source is larger than %d bytes", filename, MaxSourceBytes)
	}
	fset := token.NewFileSet()
	if !declaresPackage(src) {
		src = "package script;" + src
	}
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	if err := checkSyntax(fset, f); err != nil {
		return nil, err
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Instances:  map[*ast.Ident]types.Instance{},
	}
	var errs []error
	conf := types.Config{
		Importer:  importerFunc(e.Import),
		GoVersion: "go1.24",
		Error:     func(err error) { errs = append(errs, err) },
	}
	pkg, _ := conf.Check(f.Name.Name, fset, []*ast.File{f}, info)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if err := checkTypes(fset, info); err != nil {
		return nil, err
	}
	p := &Program{
		env:     e,
		fset:    fset,
		pkg:     pkg,
		funcs:   map[string]*funcCode{},
		sigs:    map[string]*types.Signature{},
		methods: map[methodKey]*funcCode{},
		structs: map[types.Type]*structInfo{},
	}
	c := &compiler{prog: p, info: info, funcs: map[*types.Func]*funcCode{}, globals: map[*types.Var]int{}}
	if err := c.compileFile(f); err != nil {
		return nil, err
	}
	return p, nil
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

func declaresPackage(src string) bool {
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "", src, parser.PackageClauseOnly)
	return err == nil
}

func (p *Program) Signature(name string) (*types.Signature, bool) {
	sig, ok := p.sigs[name]
	return sig, ok
}

func (p *Program) Call(ctx context.Context, lim Limits, name string, args ...any) ([]any, Stats, error) {
	return p.call(ctx, lim, nil, name, args...)
}

func (p *Program) call(ctx context.Context, lim Limits, j *Journal, name string, args ...any) ([]any, Stats, error) {
	code, ok := p.funcs[name]
	if !ok {
		return nil, Stats{}, fmt.Errorf("script has no function %s", name)
	}
	sig := p.sigs[name]
	if sig.Params().Len() != len(args) || sig.Variadic() {
		return nil, Stats{}, fmt.Errorf("%s takes %d arguments, got %d", name, sig.Params().Len(), len(args))
	}
	for i, a := range args {
		if err := p.env.checkArg(a, sig.Params().At(i).Type()); err != nil {
			return nil, Stats{}, fmt.Errorf("%s argument %d: %w", name, i, err)
		}
	}
	m := newMachine(ctx, p, lim)
	m.journal = j
	var out Value
	var results []any
	err := m.run(func() {
		m.initGlobals()
		nf := m.newFrame(code, nil)
		for i, a := range args {
			nf.param(code, i, m.fromGo(reflect.ValueOf(&a).Elem(), sig.Params().At(i).Type()))
		}
		out = m.exec(code, nf)
		results = m.exportResults(out, sig.Results())
	})
	if err != nil {
		return nil, m.stats(), err
	}
	if n := sig.Results().Len(); n > 0 && types.Identical(sig.Results().At(n-1).Type(), errorType) {
		last := results[n-1]
		results = results[:n-1]
		if last != nil {
			return results, m.stats(), last.(error)
		}
	}
	return results, m.stats(), nil
}

func (e *Env) checkArg(a any, want types.Type) error {
	if a == nil {
		switch want.Underlying().(type) {
		case *types.Interface, *types.Slice, *types.Map, *types.Pointer:
			return nil
		}
		return fmt.Errorf("nil is not a %s", want)
	}
	t, err := e.runtimeTypeOf(reflect.TypeOf(a))
	if err != nil {
		return err
	}
	if !types.AssignableTo(t, want) {
		return fmt.Errorf("cannot use %s as %s", t, want)
	}
	return nil
}

func (m *machine) initGlobals() {
	p := m.prog
	m.globals = make([]Value, p.nglobals)
	for i, z := range p.zeros {
		m.globals[i] = z()
	}
	if len(p.init) == 0 {
		return
	}
	fr := m.newFrame(p.initCode, nil)
	for _, ini := range p.init {
		m.burn(1)
		ini(fr)
	}
}

func (m *machine) exportResults(out Value, res *types.Tuple) []any {
	switch res.Len() {
	case 0:
		return nil
	case 1:
		return []any{m.export(out, res.At(0).Type(), 0)}
	}
	t := out.(tuple)
	results := make([]any, len(t))
	for i, v := range t {
		results[i] = m.export(v, res.At(i).Type(), 0)
	}
	return results
}

const maxExportDepth = 64

func (m *machine) export(v Value, t types.Type, depth int) any {
	if depth > maxExportDepth {
		panic(fmt.Errorf("result is nested deeper than %d levels", maxExportDepth))
	}
	if rt, ok := m.prog.env.reflectFor(t); ok {
		return m.toGo(v, t, rt).Interface()
	}
	switch u := t.Underlying().(type) {
	case *types.Interface:
		if v == nil {
			return nil
		}
		iv := v.(iface)
		if gerr, ok := iv.v.(error); ok && iv.t == m.prog.env.hostErr {
			return gerr
		}
		if types.Implements(iv.t, errorType.Underlying().(*types.Interface)) && types.Identical(t, errorType) {
			return errors.New(scriptError{m: m, v: iv}.Error())
		}
		return m.export(iv.v, iv.t, depth+1)
	case *types.Struct:
		sv := v.(*structVal)
		out := make(map[string]any, len(sv.f))
		for i, f := range sv.f {
			out[u.Field(i).Name()] = m.export(f, u.Field(i).Type(), depth+1)
		}
		return out
	case *types.Pointer:
		if v.(*structVal) == nil {
			return nil
		}
		return m.export(v, u.Elem(), depth+1)
	case *types.Slice:
		sl := v.([]Value)
		if sl == nil {
			return []any(nil)
		}
		out := make([]any, len(sl))
		for i, e := range sl {
			out[i] = m.export(e, u.Elem(), depth+1)
		}
		return out
	case *types.Map:
		mv := v.(*mapVal)
		if mv == nil {
			return map[any]any(nil)
		}
		out := make(map[any]any, len(mv.m))
		for k, e := range mv.m {
			out[k] = m.export(e, u.Elem(), depth+1)
		}
		return out
	case *types.Basic:
		return v
	}
	panic(fmt.Errorf("a %s cannot leave the script", t))
}
