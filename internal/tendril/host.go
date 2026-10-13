package tendril

import (
	"context"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"sync"
)

type Package struct {
	path       string
	source     string
	bound      map[string]nativeImpl
	journaled  bool
	concurrent bool
	types      []namedDecl
	funcs      []namedDecl
	consts     []namedDecl
	native     []nativeDecl
}

type namedDecl struct {
	name  string
	value any
}

type nativeDecl struct {
	name string
	sig  func(*types.Package) *types.Signature
	impl nativeImpl
}

type nativeImpl func(m *machine, typeArgs []types.Type, args []Value) Value

func NewPackage(path string) *Package { return &Package{path: path} }

func (p *Package) Concurrent() *Package {
	p.concurrent = true
	return p
}

func (p *Package) Func(name string, fn any) *Package {
	p.funcs = append(p.funcs, namedDecl{name, fn})
	return p
}

func (p *Package) Type(name string, sample any) *Package {
	p.types = append(p.types, namedDecl{name, sample})
	return p
}

func (p *Package) Const(name string, v any) *Package {
	p.consts = append(p.consts, namedDecl{name, v})
	return p
}

func (p *Package) nativeFunc(name string, sig func(*types.Package) *types.Signature, impl nativeImpl) *Package {
	p.native = append(p.native, nativeDecl{name, sig, impl})
	return p
}

type hostFunc struct {
	name       string
	fn         reflect.Value
	sig        *types.Signature
	ctx        bool
	journaled  bool
	concurrent bool
}

type nativeFunc struct {
	name string
	impl nativeImpl
}

type Env struct {
	mu        sync.Mutex
	pending   map[string]*Package
	sources   map[string]*sourceFile
	pkgs      map[string]*types.Package
	funcs     map[*types.Func]*hostFunc
	natives   map[*types.Func]*nativeFunc
	toTypes   map[reflect.Type]types.Type
	toReflect map[types.Type]reflect.Type
	fieldIdx  map[*types.Struct][]int
	hostErr   types.Type
}

var (
	errorRType   = reflect.TypeFor[error]()
	contextRType = reflect.TypeFor[context.Context]()
	errorType    = types.Universe.Lookup("error").Type()
	anyType      = types.Universe.Lookup("any").Type()
	anyRType     = reflect.TypeFor[any]()
)

func NewEnv(pkgs ...*Package) (*Env, error) {
	e := &Env{
		pkgs:      map[string]*types.Package{},
		funcs:     map[*types.Func]*hostFunc{},
		natives:   map[*types.Func]*nativeFunc{},
		toTypes:   map[reflect.Type]types.Type{},
		toReflect: map[types.Type]reflect.Type{},
		fieldIdx:  map[*types.Struct][]int{},
	}
	hostErrName := types.NewTypeName(token.NoPos, nil, "hostError", nil)
	hostErr := types.NewNamed(hostErrName, types.NewStruct(nil, nil), nil)
	errorMethod := types.NewSignatureType(types.NewVar(token.NoPos, nil, "", hostErr), nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.String])), false)
	hostErr.AddMethod(types.NewFunc(token.NoPos, nil, "Error", errorMethod))
	e.hostErr = hostErr

	e.pending = map[string]*Package{}
	var reflected []*Package
	for _, p := range pkgs {
		if _, dup := e.pending[p.path]; dup {
			return nil, fmt.Errorf("package %q declared twice", p.path)
		}
		e.pending[p.path] = p
		if p.source == "" {
			reflected = append(reflected, p)
		}
	}
	pkgs = reflected
	tpkgs := make([]*types.Package, len(pkgs))
	for i, p := range pkgs {
		delete(e.pending, p.path)
		tpkgs[i] = types.NewPackage(p.path, lastElem(p.path))
		e.pkgs[p.path] = tpkgs[i]
		for _, d := range p.types {
			rt := reflect.TypeOf(d.value)
			tn := types.NewTypeName(token.NoPos, tpkgs[i], d.name, nil)
			named := types.NewNamed(tn, nil, nil)
			tpkgs[i].Scope().Insert(tn)
			e.toTypes[rt] = named
			e.toReflect[named] = rt
		}
	}
	for i, p := range pkgs {
		for _, d := range p.types {
			rt := reflect.TypeOf(d.value)
			named := e.toTypes[rt].(*types.Named)
			u, err := e.underlyingOf(tpkgs[i], rt)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", p.path, d.name, err)
			}
			named.SetUnderlying(u)
		}
		for _, d := range p.funcs {
			rv := reflect.ValueOf(d.value)
			if rv.Kind() != reflect.Func {
				return nil, fmt.Errorf("%s.%s: not a function", p.path, d.name)
			}
			sig, wantsCtx, err := e.signature(rv.Type())
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", p.path, d.name, err)
			}
			fn := types.NewFunc(token.NoPos, tpkgs[i], d.name, sig)
			tpkgs[i].Scope().Insert(fn)
			e.funcs[fn] = &hostFunc{name: p.path + "." + d.name, fn: rv, sig: sig, ctx: wantsCtx, journaled: p.journaled, concurrent: p.concurrent}
		}
		for _, d := range p.native {
			fn := types.NewFunc(token.NoPos, tpkgs[i], d.name, d.sig(tpkgs[i]))
			tpkgs[i].Scope().Insert(fn)
			e.natives[fn] = &nativeFunc{name: p.path + "." + d.name, impl: d.impl}
		}
		for _, d := range p.consts {
			t, err := e.typeOf(reflect.TypeOf(d.value))
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", p.path, d.name, err)
			}
			tpkgs[i].Scope().Insert(types.NewConst(token.NoPos, tpkgs[i], d.name, t, constant.Make(reflect.ValueOf(d.value).Convert(basicRType(t)).Interface())))
		}
		tpkgs[i].MarkComplete()
	}
	for len(e.pending) > 0 {
		for path := range e.pending {
			if _, err := e.Import(path); err != nil {
				return nil, err
			}
			break
		}
	}
	return e, nil
}

func lastElem(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

func (e *Env) Import(path string) (*types.Package, error) {
	if p, ok := e.pkgs[path]; ok {
		return p, nil
	}
	if p, ok := e.pending[path]; ok {
		delete(e.pending, path)
		return e.checkSource(p)
	}
	return nil, fmt.Errorf("package %q is not available to scripts", path)
}

func (e *Env) underlyingOf(pkg *types.Package, rt reflect.Type) (types.Type, error) {
	if rt.Kind() != reflect.Struct {
		t, err := e.typeOf(unnamed(rt))
		if err != nil {
			return nil, err
		}
		return t.Underlying(), nil
	}
	var fields []*types.Var
	var idx []int
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() || f.Anonymous {
			continue
		}
		ft, err := e.typeOf(f.Type)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		fields = append(fields, types.NewField(token.NoPos, pkg, f.Name, ft, false))
		idx = append(idx, i)
	}
	st := types.NewStruct(fields, nil)
	e.fieldIdx[st] = idx
	return st, nil
}

func unnamed(rt reflect.Type) reflect.Type {
	switch rt.Kind() {
	case reflect.Slice:
		return reflect.SliceOf(rt.Elem())
	case reflect.Map:
		return reflect.MapOf(rt.Key(), rt.Elem())
	case reflect.Pointer:
		return reflect.PointerTo(rt.Elem())
	}
	if k := rt.Kind(); k <= reflect.Complex128 || k == reflect.String {
		return basicRTypes[k]
	}
	return rt
}

var basicKinds = map[reflect.Kind]types.BasicKind{
	reflect.Bool: types.Bool, reflect.Int: types.Int, reflect.Int8: types.Int8, reflect.Int16: types.Int16,
	reflect.Int32: types.Int32, reflect.Int64: types.Int64, reflect.Uint: types.Uint, reflect.Uint8: types.Uint8,
	reflect.Uint16: types.Uint16, reflect.Uint32: types.Uint32, reflect.Uint64: types.Uint64,
	reflect.Float32: types.Float32, reflect.Float64: types.Float64, reflect.String: types.String,
}

var basicRTypes = map[reflect.Kind]reflect.Type{
	reflect.Bool: reflect.TypeFor[bool](), reflect.Int: reflect.TypeFor[int](), reflect.Int8: reflect.TypeFor[int8](),
	reflect.Int16: reflect.TypeFor[int16](), reflect.Int32: reflect.TypeFor[int32](), reflect.Int64: reflect.TypeFor[int64](),
	reflect.Uint: reflect.TypeFor[uint](), reflect.Uint8: reflect.TypeFor[uint8](), reflect.Uint16: reflect.TypeFor[uint16](),
	reflect.Uint32: reflect.TypeFor[uint32](), reflect.Uint64: reflect.TypeFor[uint64](), reflect.Float32: reflect.TypeFor[float32](),
	reflect.Float64: reflect.TypeFor[float64](), reflect.String: reflect.TypeFor[string](),
}

func basicRType(t types.Type) reflect.Type {
	b := t.Underlying().(*types.Basic)
	for k, bk := range basicKinds {
		if bk == b.Kind() {
			return basicRTypes[k]
		}
	}
	panic("no reflect type for " + t.String())
}

func (e *Env) runtimeTypeOf(rt reflect.Type) (types.Type, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.typeOf(rt)
}

func (e *Env) typeOf(rt reflect.Type) (types.Type, error) {
	if t, ok := e.toTypes[rt]; ok {
		return t, nil
	}
	if rt == errorRType {
		return errorType, nil
	}
	if rt.Name() != "" && rt.PkgPath() != "" {
		return nil, fmt.Errorf("host type %s is not declared in any script package", rt)
	}
	var t types.Type
	switch rt.Kind() {
	case reflect.Slice:
		elem, err := e.typeOf(rt.Elem())
		if err != nil {
			return nil, err
		}
		t = types.NewSlice(elem)
	case reflect.Map:
		k, err := e.typeOf(rt.Key())
		if err != nil {
			return nil, err
		}
		v, err := e.typeOf(rt.Elem())
		if err != nil {
			return nil, err
		}
		t = types.NewMap(k, v)
	case reflect.Pointer:
		elem, err := e.typeOf(rt.Elem())
		if err != nil {
			return nil, err
		}
		if _, ok := elem.Underlying().(*types.Struct); !ok {
			return nil, fmt.Errorf("pointer %s: only pointers to structs cross into scripts", rt)
		}
		t = types.NewPointer(elem)
	case reflect.Interface:
		if rt.NumMethod() != 0 {
			return nil, fmt.Errorf("interface %s: only any and error cross into scripts", rt)
		}
		t = anyType
	default:
		bk, ok := basicKinds[rt.Kind()]
		if !ok {
			return nil, fmt.Errorf("type %s cannot cross into scripts", rt)
		}
		t = types.Typ[bk]
	}
	e.toTypes[rt] = t
	return t, nil
}

func (e *Env) signature(rt reflect.Type) (*types.Signature, bool, error) {
	start := 0
	wantsCtx := rt.NumIn() > 0 && rt.In(0) == contextRType
	if wantsCtx {
		start = 1
	}
	var params, results []*types.Var
	for i := start; i < rt.NumIn(); i++ {
		t, err := e.typeOf(rt.In(i))
		if err != nil {
			return nil, false, err
		}
		params = append(params, types.NewParam(token.NoPos, nil, "", t))
	}
	for i := range rt.NumOut() {
		t, err := e.typeOf(rt.Out(i))
		if err != nil {
			return nil, false, err
		}
		results = append(results, types.NewParam(token.NoPos, nil, "", t))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(results...), rt.IsVariadic()), wantsCtx, nil
}

func (e *Env) reflectFor(t types.Type) (reflect.Type, bool) {
	if rt, ok := e.toReflect[t]; ok {
		return rt, true
	}
	switch u := t.(type) {
	case *types.Basic:
		return basicRType(u), true
	case *types.Slice:
		elem, ok := e.reflectFor(u.Elem())
		if !ok {
			return nil, false
		}
		return reflect.SliceOf(elem), true
	case *types.Map:
		k, ok1 := e.reflectFor(u.Key())
		v, ok2 := e.reflectFor(u.Elem())
		if !ok1 || !ok2 {
			return nil, false
		}
		return reflect.MapOf(k, v), true
	case *types.Pointer:
		elem, ok := e.reflectFor(u.Elem())
		if !ok {
			return nil, false
		}
		return reflect.PointerTo(elem), true
	case *types.Alias:
		return e.reflectFor(types.Unalias(u))
	}
	return nil, false
}

func (m *machine) fromGo(rv reflect.Value, t types.Type) Value {
	if rv.Kind() == reflect.Interface && !isInterface(t) {
		rv = rv.Elem()
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		return basicFromReflect(m, u.Kind(), rv)
	case *types.Slice:
		if rv.IsNil() {
			return []Value(nil)
		}
		n := rv.Len()
		m.alloc(headerBytes + slotBytes*int64(n))
		out := make([]Value, n)
		for i := range n {
			out[i] = m.fromGo(rv.Index(i), u.Elem())
		}
		return out
	case *types.Map:
		if rv.IsNil() {
			return (*mapVal)(nil)
		}
		m.alloc(headerBytes + entryBytes*int64(rv.Len()))
		mv := &mapVal{m: make(map[Value]Value, rv.Len())}
		it := rv.MapRange()
		for it.Next() {
			mv.m[m.fromGo(it.Key(), u.Key())] = m.fromGo(it.Value(), u.Elem())
		}
		return mv
	case *types.Struct:
		idx := m.prog.env.fieldIdx[u]
		info := m.prog.structInfo(t)
		m.alloc(headerBytes + slotBytes*int64(len(idx)))
		f := make([]Value, len(idx))
		for i, ri := range idx {
			f[i] = m.fromGo(rv.Field(ri), u.Field(i).Type())
		}
		return &structVal{info: info, f: f}
	case *types.Pointer:
		if rv.IsNil() {
			return (*structVal)(nil)
		}
		return m.fromGo(rv.Elem(), u.Elem())
	case *types.Interface:
		if !rv.IsValid() || (rv.Kind() == reflect.Interface && rv.IsNil()) {
			return nil
		}
		if rv.Kind() == reflect.Interface {
			rv = rv.Elem()
		}
		if err, ok := rv.Interface().(error); ok && types.Identical(t, errorType) {
			if se, ok := err.(scriptError); ok {
				return se.v
			}
			return iface{t: m.prog.env.hostErr, v: err}
		}
		dt, err := m.prog.env.runtimeTypeOf(rv.Type())
		if err != nil {
			panic(err)
		}
		return iface{t: dt, v: m.fromGo(rv, dt)}
	}
	panic(fmt.Errorf("cannot convert %s into a script value", rv.Type()))
}

func basicFromReflect(m *machine, k types.BasicKind, rv reflect.Value) Value {
	switch k {
	case types.Bool:
		return rv.Bool()
	case types.Int:
		return int(rv.Int())
	case types.Int8:
		return int8(rv.Int())
	case types.Int16:
		return int16(rv.Int())
	case types.Int32:
		return int32(rv.Int())
	case types.Int64:
		return rv.Int()
	case types.Uint:
		return uint(rv.Uint())
	case types.Uint8:
		return uint8(rv.Uint())
	case types.Uint16:
		return uint16(rv.Uint())
	case types.Uint32:
		return uint32(rv.Uint())
	case types.Uint64:
		return rv.Uint()
	case types.Float32:
		return float32(rv.Float())
	case types.Float64:
		return rv.Float()
	case types.String:
		s := rv.String()
		m.alloc(int64(len(s)))
		return s
	}
	panic(fmt.Errorf("unsupported basic kind %v", k))
}

type scriptError struct {
	m *machine
	v iface
}

func (s scriptError) Error() (msg string) {
	s.m.reenter(func() { msg = s.m.callMethod(s.v, "Error", nil).(string) })
	return msg
}

func (m *machine) toGo(v Value, t types.Type, rt reflect.Type) reflect.Value {
	switch rt.Kind() {
	case reflect.Interface:
		if v == nil {
			return reflect.Zero(rt)
		}
		iv := v.(iface)
		if gerr, ok := iv.v.(error); ok && iv.t == m.prog.env.hostErr {
			return reflect.ValueOf(gerr)
		}
		if rt == errorRType {
			return reflect.ValueOf(scriptError{m: m, v: iv})
		}
		if drt, ok := m.prog.env.reflectFor(iv.t); ok {
			return m.toGo(iv.v, iv.t, drt)
		}
		return reflect.ValueOf(view{m: m, t: iv.t, v: iv.v})
	case reflect.Slice:
		s := v.([]Value)
		if s == nil {
			return reflect.Zero(rt)
		}
		elemT := t.Underlying().(*types.Slice).Elem()
		out := reflect.MakeSlice(rt, len(s), len(s))
		for i, e := range s {
			out.Index(i).Set(m.toGo(e, elemT, rt.Elem()))
		}
		return out
	case reflect.Map:
		mv := v.(*mapVal)
		if mv == nil {
			return reflect.Zero(rt)
		}
		mt := t.Underlying().(*types.Map)
		out := reflect.MakeMapWithSize(rt, len(mv.m))
		for k, e := range mv.m {
			out.SetMapIndex(m.toGo(k, mt.Key(), rt.Key()), m.toGo(e, mt.Elem(), rt.Elem()))
		}
		return out
	case reflect.Struct:
		st := t.Underlying().(*types.Struct)
		sv := v.(*structVal)
		out := reflect.New(rt).Elem()
		for i, ri := range m.prog.env.fieldIdx[st] {
			out.Field(ri).Set(m.toGo(sv.f[i], st.Field(i).Type(), rt.Field(ri).Type))
		}
		return out
	case reflect.Pointer:
		sv := v.(*structVal)
		if sv == nil {
			return reflect.Zero(rt)
		}
		p := reflect.New(rt.Elem())
		p.Elem().Set(m.toGo(sv, t.Underlying().(*types.Pointer).Elem(), rt.Elem()))
		return p
	default:
		return reflect.ValueOf(v).Convert(rt)
	}
}

func isInterface(t types.Type) bool {
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

func (h *hostFunc) call(m *machine, args []Value) (result Value) {
	rt := h.fn.Type()
	in := make([]reflect.Value, 0, rt.NumIn())
	if h.ctx {
		in = append(in, reflect.ValueOf(m.hctx))
	}
	params := h.sig.Params()
	var work int64
	for i, a := range args {
		gv := m.toGo(a, params.At(i).Type(), rt.In(len(in)))
		switch gv.Kind() {
		case reflect.String, reflect.Slice, reflect.Map:
			work += int64(gv.Len())
		}
		in = append(in, gv)
	}
	m.burn(1 + work/32)
	var out []reflect.Value
	live := func() []reflect.Value {
		m.invokeHost(h, in, &out)
		return out
	}
	if h.journaled && m.journal != nil {
		out = m.journaledCall(h, in, live)
	} else {
		live()
	}
	if m.halted != nil {
		panic(halt{m.halted})
	}
	results := h.sig.Results()
	switch len(out) {
	case 0:
		return nil
	case 1:
		return m.fromGo(out[0], results.At(0).Type())
	}
	tup := make(tuple, len(out))
	for i, o := range out {
		tup[i] = m.fromGo(o, results.At(i).Type())
	}
	return tup
}

func (m *machine) invokeHost(h *hostFunc, in []reflect.Value, out *[]reflect.Value) {
	if h.concurrent && m.sched != nil {
		self := m.g
		if h.ctx {
			in[0] = reflect.ValueOf(m.ctx)
		}
		m.releaseForHost()
		failure := callGo(h, in, out)
		m.reacquire(self)
		if m.halted != nil {
			panic(halt{m.halted})
		}
		if failure != nil {
			panic(failure)
		}
		return
	}
	if failure := callGo(h, in, out); failure != nil {
		panic(failure)
	}
}

func callGo(h *hostFunc, in []reflect.Value, out *[]reflect.Value) (failure any) {
	defer func() {
		if r := recover(); r != nil {
			if hl, ok := r.(halt); ok {
				failure = hl
				return
			}
			failure = hostPanic{fn: h.name, v: r}
		}
	}()
	if h.sig.Variadic() {
		*out = h.fn.CallSlice(in)
	} else {
		*out = h.fn.Call(in)
	}
	return nil
}
