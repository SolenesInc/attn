package tendril

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"slices"
)

type sourceFile struct {
	fset *token.FileSet
	file *ast.File
}

func (p *Package) Source(src string) *Package {
	p.source = src
	return p
}

func (p *Package) Native(name string, impl nativeImpl) *Package {
	if p.bound == nil {
		p.bound = map[string]nativeImpl{}
	}
	p.bound[name] = impl
	return p
}

func (e *Env) checkSource(p *Package) (*types.Package, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p.path+".go", p.source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("package %s: %w", p.path, err)
	}
	conf := types.Config{Importer: importerFunc(e.Import), GoVersion: "go1.24"}
	pkg, err := conf.Check(p.path, fset, []*ast.File{f}, nil)
	if err != nil {
		return nil, fmt.Errorf("package %s: %w", p.path, err)
	}
	if e.sources == nil {
		e.sources = map[string]*sourceFile{}
	}
	e.sources[p.path] = &sourceFile{fset: fset, file: f}
	e.pkgs[p.path] = pkg
	if err := e.bindSource(p, pkg); err != nil {
		return nil, fmt.Errorf("package %s: %w", p.path, err)
	}
	return pkg, nil
}

func (e *Env) bindSource(p *Package, pkg *types.Package) error {
	scope := pkg.Scope()
	for _, d := range p.types {
		named, ok := lookupNamed(scope, d.name)
		if !ok {
			return fmt.Errorf("bound type %s is not declared", d.name)
		}
		rt := reflect.TypeOf(d.value)
		e.toTypes[rt] = named
		e.toReflect[named] = rt
	}
	for _, d := range p.types {
		named, _ := lookupNamed(scope, d.name)
		if err := e.matchType(named, reflect.TypeOf(d.value)); err != nil {
			return fmt.Errorf("type %s: %w", d.name, err)
		}
	}
	goFuncs := map[string]any{}
	for _, d := range p.funcs {
		goFuncs[d.name] = d.value
	}
	used := map[string]bool{}
	bind := func(key string, fn *types.Func) error {
		if impl, ok := p.bound[key]; ok {
			e.natives[fn] = &nativeFunc{name: p.path + "." + key, impl: impl}
			used[key] = true
			return nil
		}
		gofn, ok := goFuncs[key]
		if !ok {
			return fmt.Errorf("%s is declared but not bound", key)
		}
		used[key] = true
		rv := reflect.ValueOf(gofn)
		sig, wantsCtx, err := e.signature(rv.Type())
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		want := receiverFirst(fn.Signature())
		if !types.Identical(sig, want) {
			return fmt.Errorf("%s is declared as %s but bound to %s", key, want, sig)
		}
		e.funcs[fn] = &hostFunc{name: p.path + "." + key, fn: rv, sig: want, ctx: wantsCtx, journaled: p.journaled, concurrent: p.concurrent}
		return nil
	}
	names := scope.Names()
	slices.Sort(names)
	for _, name := range names {
		switch obj := scope.Lookup(name).(type) {
		case *types.Func:
			if err := bind(name, obj); err != nil {
				return err
			}
		case *types.TypeName:
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			for m := range named.Methods() {
				if err := bind(name+"."+m.Name(), m); err != nil {
					return err
				}
			}
		case *types.Var:
			return fmt.Errorf("package variable %s is not supported; declare a function", name)
		}
	}
	for key := range goFuncs {
		if !used[key] {
			return fmt.Errorf("%s is bound but not declared", key)
		}
	}
	for key := range p.bound {
		if !used[key] {
			return fmt.Errorf("%s is bound but not declared", key)
		}
	}
	return nil
}

func lookupNamed(scope *types.Scope, name string) (*types.Named, bool) {
	tn, ok := scope.Lookup(name).(*types.TypeName)
	if !ok {
		return nil, false
	}
	named, ok := tn.Type().(*types.Named)
	return named, ok
}

func (e *Env) matchType(named *types.Named, rt reflect.Type) error {
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		goT, err := e.typeOf(unnamed(rt))
		if err != nil {
			return err
		}
		if !types.Identical(goT.Underlying(), named.Underlying()) {
			return fmt.Errorf("declared as %s but bound to %s", named.Underlying(), rt)
		}
		return nil
	}
	if rt.Kind() != reflect.Struct {
		return fmt.Errorf("declared as a struct but bound to %s", rt)
	}
	idx := make([]int, st.NumFields())
	for i := range st.NumFields() {
		f := st.Field(i)
		gf, ok := rt.FieldByName(f.Name())
		if !ok || !gf.IsExported() || len(gf.Index) != 1 {
			return fmt.Errorf("field %s has no exported counterpart in %s", f.Name(), rt)
		}
		goT, err := e.typeOf(gf.Type)
		if err != nil {
			return fmt.Errorf("field %s: %w", f.Name(), err)
		}
		if !types.Identical(goT, f.Type()) {
			return fmt.Errorf("field %s is declared as %s but bound to %s", f.Name(), f.Type(), gf.Type)
		}
		idx[i] = gf.Index[0]
	}
	e.fieldIdx[st] = idx
	return nil
}

func receiverFirst(sig *types.Signature) *types.Signature {
	recv := sig.Recv()
	if recv == nil {
		return sig
	}
	params := []*types.Var{types.NewParam(recv.Pos(), recv.Pkg(), recv.Name(), recv.Type())}
	for v := range sig.Params().Variables() {
		params = append(params, v)
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), sig.Results(), sig.Variadic())
}
