package tendril

import (
	"fmt"
	"go/ast"
	"go/types"
)

type methodTarget struct {
	code    *funcCode
	native  *nativeFunc
	host    *hostFunc
	name    string
	ptrRecv bool
}

func (t methodTarget) invoke(m *machine, recv Value, args []Value) Value {
	switch {
	case t.code != nil:
		fr := m.newFrame(t.code, nil)
		fr.param(t.code, 0, recv)
		for i, a := range args {
			fr.param(t.code, i+1, a)
		}
		return m.exec(t.code, fr)
	case t.native != nil:
		m.burn(1)
		return t.native.impl(m, nil, append([]Value{recv}, args...))
	case t.host != nil:
		return t.host.call(m, append([]Value{recv}, args...))
	}
	if recv == nil {
		throw("invalid memory address or nil pointer dereference")
	}
	return m.callMethod(recv.(iface), t.name, args)
}

func (p *Program) targetOf(fn *types.Func, recvType types.Type) (methodTarget, bool) {
	t := methodTarget{name: fn.Name()}
	if isInterface(recvType) {
		return t, true
	}
	if sig := fn.Signature(); sig.Recv() != nil {
		_, t.ptrRecv = sig.Recv().Type().(*types.Pointer)
	}
	if code, ok := p.methodCode(fn); ok {
		t.code = code
		return t, true
	}
	if nf, ok := p.env.natives[fn]; ok {
		t.native = nf
		return t, true
	}
	if hf, ok := p.env.funcs[fn]; ok {
		t.host = hf
		return t, true
	}
	return t, false
}

func (p *Program) methodCode(fn *types.Func) (*funcCode, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	code, ok := p.methods[fn]
	return code, ok
}

func derefType(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

func embeddedField(m *machine, v Value, t types.Type, fields []int, ptrRecv bool) (Value, types.Type) {
	for k, i := range fields {
		box := derefBox(v)
		ft := derefType(t).Underlying().(*types.Struct).Field(i).Type()
		if k == len(fields)-1 && ptrRecv && !isAggregate(ft) && !isInterface(ft) {
			if _, isPtr := ft.Underlying().(*types.Pointer); !isPtr {
				return &box.f[i], types.NewPointer(ft)
			}
		}
		v, t = box.f[i], ft
	}
	return v, t
}

func adjustRecv(m *machine, v Value, t types.Type, ptrRecv bool) Value {
	if ptrRecv || isInterface(t) {
		return v
	}
	if _, isPtr := t.Underlying().(*types.Pointer); isPtr {
		if pointsToAggregate(t) {
			return derefBox(v).clone(m)
		}
		return *derefSlot(v)
	}
	if isAggregate(t) {
		return v.(*structVal).clone(m)
	}
	return v
}

func (s *scope) methodRecv(f *ast.SelectorExpr, sel *types.Selection, target methodTarget) expr {
	path := sel.Index()
	if len(path) == 1 || sel.Kind() == types.MethodExpr {
		if target.code == nil && target.native == nil && target.host == nil {
			return s.expr(f.X)
		}
		return s.receiver(f.X, sel.Recv(), target.ptrRecv)
	}
	x := s.expr(f.X)
	recvT := sel.Recv()
	fields := path[:len(path)-1]
	return func(fr *frame) Value {
		v, t := embeddedField(fr.m, x(fr), recvT, fields, target.ptrRecv)
		return adjustRecv(fr.m, v, t, target.ptrRecv)
	}
}

func (s *scope) methodCall(e *ast.CallExpr, f *ast.SelectorExpr, sel *types.Selection, sig *types.Signature) expr {
	fn := sel.Obj().(*types.Func)
	args := s.args(sig, e)
	target, ok := s.c.prog.targetOf(fn, s.embeddedRecvType(sel))
	if !ok {
		s.c.fail(f.Pos(), "method %s has no implementation", fn.FullName())
	}
	recv := s.methodRecv(f, sel, target)
	return func(fr *frame) Value {
		r := recv(fr)
		return target.invoke(fr.m, r, args.eval(fr))
	}
}

func (s *scope) embeddedRecvType(sel *types.Selection) types.Type {
	t := sel.Recv()
	path := sel.Index()
	for _, i := range path[:len(path)-1] {
		t = derefType(t).Underlying().(*types.Struct).Field(i).Type()
	}
	return t
}

type boundMethod struct {
	target methodTarget
	recv   Value
	expr   bool
	recvT  types.Type
}

func (b *boundMethod) call(m *machine, args []Value) Value {
	if b.expr {
		return b.target.invoke(m, adjustRecv(m, args[0], b.recvT, b.target.ptrRecv), args[1:])
	}
	return b.target.invoke(m, b.recv, args)
}

func (s *scope) methodValue(f *ast.SelectorExpr, sel *types.Selection) expr {
	fn := sel.Obj().(*types.Func)
	target, ok := s.c.prog.targetOf(fn, s.embeddedRecvType(sel))
	if !ok {
		s.c.fail(f.Pos(), "method %s has no implementation", fn.FullName())
	}
	if sel.Kind() == types.MethodExpr {
		if len(sel.Index()) > 1 {
			s.c.fail(f.Pos(), "method expressions of promoted methods are not supported")
		}
		bm := &boundMethod{target: target, expr: true, recvT: sel.Recv()}
		return func(*frame) Value { return bm }
	}
	recv := s.methodRecv(f, sel, target)
	return func(fr *frame) Value {
		fr.m.alloc(headerBytes)
		return &boundMethod{target: target, recv: recv(fr)}
	}
}

type dispatchKey struct {
	t    types.Type
	name string
}

type dispatchEntry struct {
	target methodTarget
	fields []int
}

func (p *Program) dispatch(t types.Type, name string) (*dispatchEntry, error) {
	key := dispatchKey{t, name}
	p.mu.Lock()
	entry, ok := p.dispatched[key]
	p.mu.Unlock()
	if ok {
		return entry, nil
	}
	ms := types.NewMethodSet(t)
	for i := range ms.Len() {
		sel := ms.At(i)
		if sel.Obj().Name() != name {
			continue
		}
		path := sel.Index()
		fields := path[:len(path)-1]
		owner := t
		for _, f := range fields {
			owner = derefType(owner).Underlying().(*types.Struct).Field(f).Type()
		}
		target, ok := p.targetOf(sel.Obj().(*types.Func), owner)
		if !ok {
			break
		}
		entry = &dispatchEntry{target: target, fields: fields}
		p.mu.Lock()
		p.dispatched[key] = entry
		p.mu.Unlock()
		return entry, nil
	}
	return nil, fmt.Errorf("no method %s on %s", name, t)
}
