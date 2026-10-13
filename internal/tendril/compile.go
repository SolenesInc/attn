package tendril

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sync"
)

type expr func(*frame) Value

type stmtFn func(*frame) flow

type flow uint8

const (
	flowNext flow = iota
	flowBreak
	flowContinue
	flowReturn
)

type frame struct {
	m     *machine
	slots []Value
	free  []*cell
	ret   Value
	store [8]Value
}

type funcCode struct {
	name        string
	nslots      int
	params      []int
	capt        []bool
	results     []int
	resultCapt  []bool
	resultZeros []func() Value
	ptrRecv     bool
	body        stmtFn
}

type methodKey struct {
	recv *types.TypeName
	name string
}

type Program struct {
	env      *Env
	fset     *token.FileSet
	pkg      *types.Package
	funcs    map[string]*funcCode
	sigs     map[string]*types.Signature
	methods  map[methodKey]*funcCode
	nglobals int
	zeros    []func() Value
	init     []stmtFn
	initCode *funcCode

	mu      sync.Mutex
	structs map[types.Type]*structInfo
}

type compiler struct {
	prog    *Program
	info    *types.Info
	errs    []error
	funcs   map[*types.Func]*funcCode
	globals map[*types.Var]int
}

type scope struct {
	c        *compiler
	code     *funcCode
	locals   map[types.Object]int
	free     map[types.Object]int
	captured map[types.Object]bool
	sig      *types.Signature
}

type unsupported struct {
	pos token.Pos
	msg string
}

func (c *compiler) fail(pos token.Pos, format string, args ...any) {
	panic(unsupported{pos, fmt.Sprintf(format, args...)})
}

func (c *compiler) guard(f func()) {
	defer func() {
		if r := recover(); r != nil {
			u, ok := r.(unsupported)
			if !ok {
				panic(r)
			}
			c.errs = append(c.errs, &Error{Pos: c.prog.fset.Position(u.pos), Msg: u.msg})
		}
	}()
	f()
}

type Error struct {
	Pos token.Position
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Pos, e.Msg) }

func (c *compiler) compileFile(f *ast.File) error {
	p := c.prog
	var decls []*ast.FuncDecl
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		obj := c.info.Defs[fd.Name].(*types.Func)
		code := &funcCode{name: fd.Name.Name}
		c.funcs[obj] = code
		decls = append(decls, fd)
		sig := obj.Type().(*types.Signature)
		if recv := sig.Recv(); recv != nil {
			rt := recv.Type()
			if ptr, ok := rt.(*types.Pointer); ok {
				rt = ptr.Elem()
				code.ptrRecv = true
			}
			named := rt.(*types.Named)
			p.methods[methodKey{named.Obj(), obj.Name()}] = code
			code.name = named.Obj().Name() + "." + obj.Name()
		} else if fd.Name.Name != "init" {
			p.funcs[obj.Name()] = code
			p.sigs[obj.Name()] = sig
		}
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			for _, name := range spec.(*ast.ValueSpec).Names {
				v, ok := c.info.Defs[name].(*types.Var)
				if !ok {
					continue
				}
				c.globals[v] = p.nglobals
				p.nglobals++
				p.zeros = append(p.zeros, p.zero(v.Type()))
			}
		}
	}
	initScope := c.newScope(&funcCode{name: "init"}, map[types.Object]bool{})
	for _, ini := range c.info.InitOrder {
		c.guard(func() { p.init = append(p.init, initScope.globalInit(ini)) })
	}
	for _, fd := range decls {
		c.guard(func() {
			obj := c.info.Defs[fd.Name].(*types.Func)
			code := c.funcs[obj]
			s := c.newScope(code, c.capturedIn(fd.Body))
			s.compileFunc(obj.Type().(*types.Signature), fd.Body)
			if fd.Name.Name == "init" && fd.Recv == nil {
				p.init = append(p.init, func(fr *frame) flow {
					fr.m.exec(code, fr.m.newFrame(code, nil))
					return flowNext
				})
			}
		})
	}
	p.initCode = initScope.code
	return errors.Join(c.errs...)
}

func (c *compiler) newScope(code *funcCode, captured map[types.Object]bool) *scope {
	return &scope{c: c, code: code, locals: map[types.Object]int{}, free: map[types.Object]int{}, captured: captured}
}

func (s *scope) globalInit(ini *types.Initializer) stmtFn {
	if len(ini.Lhs) == 1 {
		g := s.c.globals[ini.Lhs[0]]
		val := s.convertTo(ini.Lhs[0].Type(), ini.Rhs)
		return func(fr *frame) flow {
			fr.m.globals[g] = val(fr)
			return flowNext
		}
	}
	call := s.expr(ini.Rhs)
	gs := make([]int, len(ini.Lhs))
	for i, v := range ini.Lhs {
		gs[i] = s.c.globals[v]
	}
	return func(fr *frame) flow {
		t := call(fr).(tuple)
		for i, g := range gs {
			fr.m.globals[g] = t[i]
		}
		return flowNext
	}
}

func (c *compiler) capturedIn(body ast.Node) map[types.Object]bool {
	captured := map[types.Object]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			for _, v := range c.freeVars(lit) {
				captured[v] = true
			}
		}
		return true
	})
	return captured
}

func (c *compiler) freeVars(lit *ast.FuncLit) []types.Object {
	var out []types.Object
	seen := map[types.Object]bool{}
	pkgScope := c.prog.pkg.Scope()
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := c.info.Uses[id].(*types.Var)
		if !ok || v.IsField() || v.Parent() == pkgScope || seen[v] {
			return true
		}
		if v.Pos() >= lit.Pos() && v.Pos() < lit.End() {
			return true
		}
		seen[v] = true
		out = append(out, v)
		return true
	})
	return out
}

func (s *scope) declare(v types.Object) int {
	slot := s.code.nslots
	s.code.nslots++
	s.locals[v] = slot
	return slot
}

func (s *scope) compileFunc(sig *types.Signature, body *ast.BlockStmt) {
	s.sig = sig
	code := s.code
	if recv := sig.Recv(); recv != nil {
		code.params = append(code.params, s.declare(recv))
		code.capt = append(code.capt, s.captured[recv])
	}
	for v := range sig.Params().Variables() {
		code.params = append(code.params, s.declare(v))
		code.capt = append(code.capt, s.captured[v])
	}
	if res := sig.Results(); res.Len() > 0 && res.At(0).Name() != "" {
		for v := range res.Variables() {
			code.results = append(code.results, s.declare(v))
			code.resultCapt = append(code.resultCapt, s.captured[v])
			code.resultZeros = append(code.resultZeros, s.c.prog.zero(v.Type()))
		}
	}
	code.body = s.block(body.List)
}

func (m *machine) newFrame(fn *funcCode, free []*cell) *frame {
	var fr *frame
	if n := len(m.frames); n > 0 {
		fr = m.frames[n-1]
		m.frames = m.frames[:n-1]
	} else {
		fr = &frame{m: m}
	}
	switch n := fn.nslots; {
	case n <= len(fr.store):
		fr.slots = fr.store[:n]
	case cap(fr.slots) >= n:
		fr.slots = fr.slots[:n]
	default:
		fr.slots = make([]Value, n)
	}
	fr.free = free
	return fr
}

func (m *machine) release(fr *frame) {
	clear(fr.slots)
	fr.ret, fr.free = nil, nil
	m.frames = append(m.frames, fr)
}

func (fr *frame) param(fn *funcCode, i int, v Value) {
	if fn.capt[i] {
		fr.slots[fn.params[i]] = &cell{v}
	} else {
		fr.slots[fn.params[i]] = v
	}
}

func (m *machine) exec(fn *funcCode, fr *frame) Value {
	m.enter()
	pos := m.pos
	for i, z := range fn.resultZeros {
		if fn.resultCapt[i] {
			fr.slots[fn.results[i]] = &cell{z()}
		} else {
			fr.slots[fn.results[i]] = z()
		}
	}
	fn.body(fr)
	ret := fr.ret
	m.release(fr)
	m.depth--
	m.pos = pos
	return ret
}

func (m *machine) callMethod(recv iface, name string, args []Value) Value {
	t := recv.t
	isPtr := false
	if p, ok := t.(*types.Pointer); ok {
		t, isPtr = p.Elem(), true
	}
	named, ok := t.(*types.Named)
	if !ok {
		if gerr, ok := recv.v.(error); ok && name == "Error" {
			return gerr.Error()
		}
		panic(fmt.Errorf("no method %s on %s", name, recv.t))
	}
	fn, ok := m.prog.methods[methodKey{named.Obj(), name}]
	if !ok {
		if gerr, ok := recv.v.(error); ok && name == "Error" {
			return gerr.Error()
		}
		panic(fmt.Errorf("no method %s on %s", name, recv.t))
	}
	r := recv.v
	if sv, isStruct := r.(*structVal); isStruct && !fn.ptrRecv {
		if isPtr && sv == nil {
			panic(fmt.Errorf("value method %s called using nil pointer", fn.name))
		}
		r = sv.clone(m)
	}
	fr := m.newFrame(fn, nil)
	fr.param(fn, 0, r)
	for i, a := range args {
		fr.param(fn, i+1, a)
	}
	return m.exec(fn, fr)
}

func (p *Program) structInfo(t types.Type) *structInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.structInfoLocked(t)
}

func (p *Program) structInfoLocked(t types.Type) *structInfo {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	t = types.Unalias(t)
	if info, ok := p.structs[t]; ok {
		return info
	}
	st := t.Underlying().(*types.Struct)
	info := &structInfo{typ: t}
	p.structs[t] = info
	for i := range st.NumFields() {
		f := st.Field(i)
		info.names = append(info.names, f.Name())
		if _, ok := f.Type().Underlying().(*types.Struct); ok {
			info.valueStructs = append(info.valueStructs, i)
		}
		info.zeros = append(info.zeros, p.zeroLocked(f.Type()))
	}
	return info
}

func (p *Program) zero(t types.Type) func() Value {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.zeroLocked(t)
}

func (p *Program) zeroLocked(t types.Type) func() Value {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		z := basicZero(u.Kind())
		return func() Value { return z }
	case *types.Struct:
		info := p.structInfoLocked(t)
		return func() Value {
			f := make([]Value, len(info.zeros))
			for i, z := range info.zeros {
				f[i] = z()
			}
			return &structVal{info: info, f: f}
		}
	case *types.Slice:
		return func() Value { return []Value(nil) }
	case *types.Map:
		return func() Value { return (*mapVal)(nil) }
	case *types.Pointer:
		return func() Value { return (*structVal)(nil) }
	default:
		return func() Value { return nil }
	}
}

func basicZero(k types.BasicKind) Value {
	switch k {
	case types.Bool, types.UntypedBool:
		return false
	case types.Int, types.UntypedInt:
		return 0
	case types.Int8:
		return int8(0)
	case types.Int16:
		return int16(0)
	case types.Int32, types.UntypedRune:
		return int32(0)
	case types.Int64:
		return int64(0)
	case types.Uint:
		return uint(0)
	case types.Uint8:
		return uint8(0)
	case types.Uint16:
		return uint16(0)
	case types.Uint32:
		return uint32(0)
	case types.Uint64:
		return uint64(0)
	case types.Uintptr:
		return uintptr(0)
	case types.Float32:
		return float32(0)
	case types.Float64, types.UntypedFloat:
		return float64(0)
	case types.String, types.UntypedString:
		return ""
	}
	return nil
}
