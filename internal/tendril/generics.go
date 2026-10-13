package tendril

import (
	"go/ast"
	"go/types"
	"strings"
)

type instKey struct {
	fn   *types.Func
	args string
}

func argsKey(args []types.Type) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = types.TypeString(a, nil)
	}
	return strings.Join(parts, ",")
}

func typeListOf(l *types.TypeList) []types.Type {
	out := make([]types.Type, l.Len())
	for i := range out {
		out[i] = l.At(i)
	}
	return out
}

func isGeneric(fn *types.Func) bool {
	sig := fn.Signature()
	return sig.TypeParams().Len() > 0 || sig.RecvTypeParams().Len() > 0
}

func (s *scope) subst(t types.Type) types.Type {
	if len(s.tmap) == 0 || t == nil {
		return t
	}
	return s.c.substitute(t, s.tmap)
}

func (c *compiler) substitute(t types.Type, tmap map[*types.TypeParam]types.Type) types.Type {
	sub := func(t types.Type) types.Type { return c.substitute(t, tmap) }
	switch t := t.(type) {
	case *types.TypeParam:
		if r, ok := tmap[t]; ok {
			return r
		}
	case *types.Alias:
		return sub(types.Unalias(t))
	case *types.Pointer:
		if e := sub(t.Elem()); e != t.Elem() {
			return types.NewPointer(e)
		}
	case *types.Slice:
		if e := sub(t.Elem()); e != t.Elem() {
			return types.NewSlice(e)
		}
	case *types.Array:
		if e := sub(t.Elem()); e != t.Elem() {
			return types.NewArray(e, t.Len())
		}
	case *types.Map:
		k, v := sub(t.Key()), sub(t.Elem())
		if k != t.Key() || v != t.Elem() {
			return types.NewMap(k, v)
		}
	case *types.Chan:
		if e := sub(t.Elem()); e != t.Elem() {
			return types.NewChan(t.Dir(), e)
		}
	case *types.Tuple:
		return c.substTuple(t, tmap)
	case *types.Signature:
		params, results := c.substTuple(t.Params(), tmap), c.substTuple(t.Results(), tmap)
		if params != t.Params() || results != t.Results() {
			return types.NewSignatureType(nil, nil, nil, params, results, t.Variadic())
		}
	case *types.Struct:
		changed := false
		fields := make([]*types.Var, t.NumFields())
		tags := make([]string, t.NumFields())
		for i := range fields {
			f := t.Field(i)
			ft := sub(f.Type())
			changed = changed || ft != f.Type()
			fields[i] = types.NewField(f.Pos(), f.Pkg(), f.Name(), ft, f.Embedded())
			tags[i] = t.Tag(i)
		}
		if changed {
			return types.NewStruct(fields, tags)
		}
	case *types.Named:
		args := t.TypeArgs()
		if args.Len() == 0 {
			return t
		}
		changed := false
		next := make([]types.Type, args.Len())
		for i := range next {
			next[i] = sub(args.At(i))
			changed = changed || next[i] != args.At(i)
		}
		if changed {
			inst, err := types.Instantiate(c.tctx, t.Origin(), next, false)
			if err != nil {
				panic(err)
			}
			return inst
		}
	case *types.Interface:
		changed := false
		var methods []*types.Func
		for i := range t.NumExplicitMethods() {
			m := t.ExplicitMethod(i)
			sig := sub(m.Type()).(*types.Signature)
			changed = changed || sig != m.Type()
			methods = append(methods, types.NewFunc(m.Pos(), m.Pkg(), m.Name(), sig))
		}
		var embedded []types.Type
		for i := range t.NumEmbeddeds() {
			e := sub(t.EmbeddedType(i))
			changed = changed || e != t.EmbeddedType(i)
			embedded = append(embedded, e)
		}
		if changed {
			return types.NewInterfaceType(methods, embedded).Complete()
		}
	case *types.Union:
		changed := false
		terms := make([]*types.Term, t.Len())
		for i := range terms {
			term := t.Term(i)
			tt := sub(term.Type())
			changed = changed || tt != term.Type()
			terms[i] = types.NewTerm(term.Tilde(), tt)
		}
		if changed {
			return types.NewUnion(terms)
		}
	}
	return t
}

func (c *compiler) substTuple(t *types.Tuple, tmap map[*types.TypeParam]types.Type) *types.Tuple {
	if t == nil {
		return nil
	}
	changed := false
	vars := make([]*types.Var, t.Len())
	for i := range vars {
		v := t.At(i)
		vt := c.substitute(v.Type(), tmap)
		changed = changed || vt != v.Type()
		vars[i] = types.NewParam(v.Pos(), v.Pkg(), v.Name(), vt)
	}
	if !changed {
		return t
	}
	return types.NewTuple(vars...)
}

func (c *compiler) instance(origin *types.Func, args []types.Type) *funcCode {
	key := instKey{origin, argsKey(args)}
	p := c.prog
	p.mu.Lock()
	code, ok := p.instances[key]
	p.mu.Unlock()
	if ok {
		return code
	}
	decl := c.generic[origin]
	sig := origin.Signature()
	code = &funcCode{name: origin.Name() + "[" + key.args + "]"}
	if recv := sig.Recv(); recv != nil {
		_, code.ptrRecv = recv.Type().(*types.Pointer)
	}
	p.mu.Lock()
	p.instances[key] = code
	p.mu.Unlock()
	params := sig.TypeParams()
	if params.Len() == 0 {
		params = sig.RecvTypeParams()
	}
	tmap := make(map[*types.TypeParam]types.Type, params.Len())
	for i := range params.Len() {
		tmap[params.At(i)] = args[i]
	}
	s := c.newScope(code, c.capturedIn(decl.Body, map[types.Object]bool{}))
	s.tmap = tmap
	s.compileFunc(sig, decl.Body)
	return code
}

func (s *scope) genericCallee(fn *types.Func, id *ast.Ident) (*funcCode, bool) {
	if _, ok := s.c.generic[fn]; !ok {
		return nil, false
	}
	inst, ok := s.c.info.Instances[id]
	if !ok {
		s.c.fail(id.Pos(), "internal: no instantiation recorded for %s", fn.Name())
	}
	args := typeListOf(inst.TypeArgs)
	for i, a := range args {
		args[i] = s.subst(a)
	}
	return s.c.instance(fn, args), true
}

func (s *scope) target(fn *types.Func, recvType types.Type) (methodTarget, bool) {
	origin := fn.Origin()
	if _, ok := s.c.generic[origin]; ok {
		named, _ := derefType(recvType).(*types.Named)
		if named == nil {
			return methodTarget{}, false
		}
		code := s.c.instance(origin, typeListOf(named.TypeArgs()))
		return methodTarget{name: fn.Name(), code: code, ptrRecv: code.ptrRecv}, true
	}
	return s.c.prog.targetOf(fn, recvType)
}

func (s *scope) ensureMethods(t types.Type) {
	if len(s.c.generic) == 0 {
		return
	}
	seen := map[types.Type]bool{}
	var walk func(t types.Type)
	walk = func(t types.Type) {
		if seen[t] {
			return
		}
		seen[t] = true
		if named, ok := t.(*types.Named); ok && named.TypeArgs().Len() > 0 {
			for m := range named.Origin().Methods() {
				if _, ok := s.c.generic[m]; ok {
					s.c.instance(m, typeListOf(named.TypeArgs()))
				}
			}
		}
		switch u := t.Underlying().(type) {
		case *types.Pointer:
			walk(u.Elem())
		case *types.Slice:
			walk(u.Elem())
		case *types.Array:
			walk(u.Elem())
		case *types.Map:
			walk(u.Key())
			walk(u.Elem())
		case *types.Chan:
			walk(u.Elem())
		case *types.Struct:
			for i := range u.NumFields() {
				walk(u.Field(i).Type())
			}
		}
	}
	walk(t)
}
