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
	flowBreakLabel
	flowContinueLabel
	flowFallthrough
)

func loopFlow(m *machine, fl flow, label *types.Label) (exit bool, result flow) {
	switch fl {
	case flowNext, flowContinue:
		return false, flowNext
	case flowBreak:
		return true, flowNext
	case flowBreakLabel:
		if label != nil && m.label == label {
			return true, flowNext
		}
	case flowContinueLabel:
		if label != nil && m.label == label {
			return false, flowNext
		}
	}
	return true, fl
}

func switchFlow(m *machine, fl flow, label *types.Label) flow {
	if fl == flowBreak || (fl == flowBreakLabel && label != nil && m.label == label) {
		return flowNext
	}
	return fl
}

type frame struct {
	m         *machine
	slots     []Value
	free      []*cell
	ret       Value
	defers    []deferred
	deferArgs []Value
	store     [8]Value
}

type deferred struct {
	call func(*frame) Value
	args []Value
}

type panicState struct {
	raw       any
	value     Value
	pos       token.Pos
	recovered bool
	depth     int
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
	hasDefer    bool
	zeroRet     func() Value
	body        stmtFn
}

type Program struct {
	env        *Env
	fset       *token.FileSet
	pkg        *types.Package
	funcs      map[string]*funcCode
	sigs       map[string]*types.Signature
	methods    map[*types.Func]*funcCode
	instances  map[instKey]*funcCode
	dispatched map[dispatchKey]*dispatchEntry
	nglobals   int
	zeros      []func() Value
	init       []stmtFn
	initCode   *funcCode
	hostInits  []hostInit

	mu      sync.Mutex
	structs map[types.Type]*structInfo
}

type compiler struct {
	prog    *Program
	info    *types.Info
	errs    []error
	funcs   map[*types.Func]*funcCode
	globals map[*types.Var]int
	generic map[*types.Func]*ast.FuncDecl
	tctx    *types.Context
}

type scope struct {
	c         *compiler
	code      *funcCode
	locals    map[types.Object]int
	free      map[types.Object]int
	captured  map[types.Object]bool
	sig       *types.Signature
	label     *types.Label
	overrides map[ast.Expr]expr
	tmap      map[*types.TypeParam]types.Type
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
		if isGeneric(obj) {
			c.generic[obj] = fd
			continue
		}
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
			p.methods[obj] = code
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
		c.capturedIn(ini.Rhs, initScope.captured)
		c.guard(func() { p.init = append(p.init, initScope.globalInit(ini)) })
	}
	for _, fd := range decls {
		c.guard(func() {
			obj := c.info.Defs[fd.Name].(*types.Func)
			code := c.funcs[obj]
			s := c.newScope(code, c.capturedIn(fd.Body, map[types.Object]bool{}))
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

func (s *scope) child(code *funcCode) *scope {
	c := s.c.newScope(code, s.captured)
	c.tmap = s.tmap
	return c
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

func (c *compiler) capturedIn(body ast.Node, captured map[types.Object]bool) map[types.Object]bool {
	addressed := func(e ast.Expr) {
		if id, ok := ast.Unparen(e).(*ast.Ident); ok {
			if v, ok := c.info.Uses[id].(*types.Var); ok && !isAggregate(v.Type()) {
				captured[v] = true
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			for _, v := range c.freeVars(n) {
				captured[v] = true
			}
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				addressed(n.X)
			}
		case *ast.SelectorExpr:
			if sel, ok := c.info.Selections[n]; ok && sel.Kind() != types.FieldVal {
				if recv := sel.Obj().(*types.Func).Signature().Recv(); recv != nil {
					if _, ptr := recv.Type().(*types.Pointer); ptr {
						if _, xPtr := sel.Recv().Underlying().(*types.Pointer); !xPtr {
							addressed(n.X)
						}
					}
				}
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
			code.resultZeros = append(code.resultZeros, s.c.prog.zero(s.subst(v.Type())))
		}
	}
	code.hasDefer = containsDefer(body)
	zeros := make([]func() Value, sig.Results().Len())
	for i := range zeros {
		zeros[i] = s.c.prog.zero(s.subst(sig.Results().At(i).Type()))
	}
	code.zeroRet = func() Value {
		switch len(zeros) {
		case 0:
			return nil
		case 1:
			return zeros[0]()
		}
		t := make(tuple, len(zeros))
		for i, z := range zeros {
			t[i] = z()
		}
		return t
	}
	code.body = s.block(body.List)
}

func containsDefer(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.DeferStmt:
			found = true
		case *ast.FuncLit:
			return false
		}
		return !found
	})
	return found
}

func (s *scope) takeLabel() *types.Label {
	l := s.label
	s.label = nil
	return l
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
	fr.ret, fr.free, fr.defers, fr.deferArgs = nil, nil, fr.defers[:0], nil
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
	if fn.hasDefer {
		m.execDeferring(fn, fr)
	} else {
		fn.body(fr)
	}
	ret := fr.ret
	m.release(fr)
	m.depth--
	m.pos = pos
	return ret
}

func (m *machine) callMethod(recv iface, name string, args []Value) Value {
	if gerr, ok := recv.v.(error); ok && recv.t == m.prog.env.hostErr && name == "Error" {
		return gerr.Error()
	}
	entry, err := m.prog.dispatch(recv.t, name)
	if err != nil {
		panic(err)
	}
	t := entry.target
	if len(entry.fields) == 0 {
		if !t.ptrRecv {
			if _, isPtr := recv.t.Underlying().(*types.Pointer); isPtr && isNilPointer(recv.v) {
				throw("invalid memory address or nil pointer dereference")
			}
		}
		return t.invoke(m, adjustRecv(m, recv.v, recv.t, t.ptrRecv), args)
	}
	v, vt := embeddedField(m, recv.v, recv.t, entry.fields, t.ptrRecv)
	return t.invoke(m, adjustRecv(m, v, vt, t.ptrRecv), args)
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
	info := &structInfo{typ: t}
	p.structs[t] = info
	switch u := t.Underlying().(type) {
	case *types.Array:
		info.array, info.n = true, int(u.Len())
		info.hasAgg = isAggregate(u.Elem())
		info.elemZero = p.zeroLocked(u.Elem())
	case *types.Struct:
		info.n = u.NumFields()
		for i := range u.NumFields() {
			f := u.Field(i)
			info.names = append(info.names, f.Name())
			agg := isAggregate(f.Type())
			info.agg = append(info.agg, agg)
			info.hasAgg = info.hasAgg || agg
			info.zeros = append(info.zeros, p.zeroLocked(f.Type()))
		}
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
	case *types.Struct, *types.Array:
		info := p.structInfoLocked(t)
		return func() Value { return info.newZero() }
	case *types.Slice:
		return func() Value { return []Value(nil) }
	case *types.Map:
		return func() Value { return (*mapVal)(nil) }
	case *types.Chan:
		return func() Value { return (*chanVal)(nil) }
	case *types.Pointer:
		if isAggregate(u.Elem()) {
			return func() Value { return (*structVal)(nil) }
		}
		return func() Value { return (*Value)(nil) }
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

func sizeOf(t types.Type) int64 {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		n := int64(headerBytes)
		for i := range u.NumFields() {
			n += sizeOf(u.Field(i).Type())
		}
		return n
	case *types.Array:
		return headerBytes + u.Len()*sizeOf(u.Elem())
	}
	return slotBytes
}

func (m *machine) execDeferring(fn *funcCode, fr *frame) {
	depth := m.depth
	var ps *panicState
	if r := m.protect(func() { fn.body(fr) }); r != nil {
		ps = m.newPanic(r)
	}
	for len(fr.defers) > 0 {
		d := fr.defers[len(fr.defers)-1]
		fr.defers = fr.defers[:len(fr.defers)-1]
		m.depth = depth
		saved, outer := fr.deferArgs, m.panicking
		fr.deferArgs, m.panicking = d.args, ps
		if ps != nil {
			ps.depth = depth + 1
		}
		r := m.protect(func() { d.call(fr) })
		fr.deferArgs, m.panicking = saved, outer
		if r != nil {
			ps = m.newPanic(r)
		}
	}
	m.depth = depth
	if ps != nil && !ps.recovered {
		m.pos = ps.pos
		panic(ps.raw)
	}
	if len(fn.results) > 0 {
		fr.ret = fr.resultValues(fn)
	} else if ps != nil {
		fr.ret = fr.zeroResults(fn)
	}
}

func (m *machine) protect(f func()) (r any) {
	defer func() {
		if r = recover(); r != nil {
			if h, isHalt := r.(halt); isHalt {
				panic(h)
			}
		}
	}()
	f()
	return nil
}

func (m *machine) newPanic(r any) *panicState {
	ps := &panicState{raw: r, pos: m.pos}
	switch v := r.(type) {
	case scriptPanic:
		ps.value = v.v
	case runtimeFault:
		ps.value = iface{t: m.prog.env.hostErr, v: runtimeError(v)}
	case error:
		ps.value = iface{t: m.prog.env.hostErr, v: v}
	default:
		ps.value = iface{t: m.prog.env.hostErr, v: fmt.Errorf("%v", v)}
	}
	return ps
}

type runtimeError string

func (e runtimeError) Error() string { return "runtime error: " + string(e) }
func (e runtimeError) RuntimeError() {}

func (m *machine) recoverPanic() Value {
	ps := m.panicking
	if ps == nil || ps.recovered || ps.depth != m.depth {
		return nil
	}
	ps.recovered = true
	return ps.value
}

func (fr *frame) resultValues(fn *funcCode) Value {
	vals := make(tuple, len(fn.results))
	for i, slot := range fn.results {
		v := fr.slots[slot]
		if fn.resultCapt[i] {
			v = v.(*cell).v
		}
		vals[i] = v
	}
	if len(vals) == 1 {
		return vals[0]
	}
	return vals
}

func (fr *frame) zeroResults(fn *funcCode) Value {
	return fn.zeroRet()
}

type hostInit struct {
	slot int
	init func(*machine) Value
}

func (c *compiler) global(v *types.Var) (int, bool) {
	if g, ok := c.globals[v]; ok {
		return g, true
	}
	init, ok := c.prog.env.vars[v]
	if !ok {
		return 0, false
	}
	p := c.prog
	g := p.nglobals
	p.nglobals++
	p.zeros = append(p.zeros, func() Value { return nil })
	p.hostInits = append(p.hostInits, hostInit{g, init})
	c.globals[v] = g
	return g, true
}
