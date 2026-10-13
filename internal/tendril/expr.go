package tendril

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"unicode/utf8"
)

type runtimeFault string

func throw(msg string) { panic(runtimeFault(msg)) }

func (s *scope) typeOf(e ast.Expr) types.Type { return s.c.info.TypeOf(e) }

func defaultType(t types.Type) types.Type {
	b, ok := t.(*types.Basic)
	if !ok || b.Info()&types.IsUntyped == 0 {
		return t
	}
	switch b.Kind() {
	case types.UntypedBool:
		return types.Typ[types.Bool]
	case types.UntypedInt:
		return types.Typ[types.Int]
	case types.UntypedRune:
		return types.Typ[types.Int32]
	case types.UntypedFloat:
		return types.Typ[types.Float64]
	case types.UntypedString:
		return types.Typ[types.String]
	}
	return t
}

func (s *scope) expr(e ast.Expr) expr {
	if ov, ok := s.overrides[e]; ok {
		return ov
	}
	if tv := s.c.info.Types[e]; tv.Value != nil {
		v := constValue(tv.Value, defaultType(tv.Type))
		return func(*frame) Value { return v }
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return s.expr(e.X)
	case *ast.Ident:
		return s.ident(e)
	case *ast.BinaryExpr:
		return s.binary(e)
	case *ast.UnaryExpr:
		return s.unary(e)
	case *ast.CallExpr:
		return s.call(e)
	case *ast.SelectorExpr:
		return s.selector(e)
	case *ast.IndexExpr:
		return s.index(e)
	case *ast.SliceExpr:
		return s.sliceExpr(e)
	case *ast.CompositeLit:
		return s.composite(e)
	case *ast.FuncLit:
		return s.funcLit(e)
	case *ast.TypeAssertExpr:
		return s.typeAssert(e, false)
	case *ast.StarExpr:
		p := s.expr(e.X)
		if isAggregate(s.typeOf(e)) {
			return func(fr *frame) Value { return derefBox(p(fr)) }
		}
		return func(fr *frame) Value { return *derefSlot(p(fr)) }
	}
	s.c.fail(e.Pos(), "unsupported expression %T", e)
	return nil
}

func constValue(v constant.Value, t types.Type) Value {
	k := t.Underlying().(*types.Basic).Kind()
	switch k {
	case types.Bool:
		return constant.BoolVal(v)
	case types.String:
		return constant.StringVal(v)
	case types.Float32, types.Float64:
		f, _ := constant.Float64Val(constant.ToFloat(v))
		return convertFloat(f, k)
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
		u, _ := constant.Uint64Val(constant.ToInt(v))
		return convertUnsigned(u, k)
	}
	i, _ := constant.Int64Val(constant.ToInt(v))
	return convertBasic(i, k)
}

func (s *scope) ident(e *ast.Ident) expr {
	switch obj := s.object(e).(type) {
	case *types.Var:
		return s.varLoad(obj, e.Pos())
	case *types.Nil:
		return func(*frame) Value { return nil }
	case *types.Func:
		return s.funcValue(obj, e.Pos())
	}
	s.c.fail(e.Pos(), "unsupported identifier %s", e.Name)
	return nil
}

func (s *scope) varLoad(v *types.Var, pos token.Pos) expr {
	if g, ok := s.c.globals[v]; ok {
		return func(fr *frame) Value { return fr.m.globals[g] }
	}
	if i, ok := s.free[v]; ok {
		return func(fr *frame) Value { return fr.free[i].v }
	}
	slot, ok := s.locals[v]
	if !ok {
		s.c.fail(pos, "internal: unresolved variable %s", v.Name())
	}
	if s.captured[v] {
		return func(fr *frame) Value { return fr.slots[slot].(*cell).v }
	}
	return func(fr *frame) Value { return fr.slots[slot] }
}

func (s *scope) funcValue(fn *types.Func, pos token.Pos) expr {
	if code, ok := s.c.funcs[fn]; ok {
		cl := &closure{fn: code}
		return func(*frame) Value { return cl }
	}
	if hf, ok := s.c.prog.env.funcs[fn]; ok {
		return func(*frame) Value { return hf }
	}
	s.c.fail(pos, "%s cannot be used as a value", fn.FullName())
	return nil
}

func (s *scope) selector(e *ast.SelectorExpr) expr {
	sel, ok := s.c.info.Selections[e]
	if !ok {
		return s.ident(e.Sel)
	}
	if sel.Kind() != types.FieldVal {
		return s.methodValue(e, sel)
	}
	base, i := s.fieldBase(e)
	return func(fr *frame) Value { return base(fr).(*structVal).f[i] }
}

func (s *scope) fieldBase(e *ast.SelectorExpr) (expr, int) {
	path := s.c.info.Selections[e].Index()
	x := s.expr(e.X)
	for _, i := range path[:len(path)-1] {
		prev := x
		x = func(fr *frame) Value { return prev(fr).(*structVal).f[i] }
	}
	return x, path[len(path)-1]
}

func (s *scope) intExpr(e ast.Expr) expr {
	x := s.expr(e)
	if b, ok := defaultType(s.typeOf(e)).Underlying().(*types.Basic); ok && b.Kind() == types.Int {
		return x
	}
	return func(fr *frame) Value { return int(toInt64(x(fr))) }
}

func (s *scope) index(e *ast.IndexExpr) expr {
	x := s.expr(e.X)
	switch xt := s.typeOf(e.X).Underlying().(type) {
	case *types.Slice:
		idx := s.intExpr(e.Index)
		return func(fr *frame) Value { return x(fr).([]Value)[idx(fr).(int)] }
	case *types.Basic:
		idx := s.intExpr(e.Index)
		return func(fr *frame) Value { return x(fr).(string)[idx(fr).(int)] }
	case *types.Map:
		return s.mapIndex(e, false)
	case *types.Array, *types.Pointer:
		idx := s.intExpr(e.Index)
		return func(fr *frame) Value { return derefBox(x(fr)).f[idx(fr).(int)] }
	default:
		s.c.fail(e.Pos(), "cannot index %s", xt)
	}
	return nil
}

func (s *scope) mapIndex(e *ast.IndexExpr, commaOK bool) expr {
	mt := s.typeOf(e.X).Underlying().(*types.Map)
	x := s.expr(e.X)
	k := s.convertTo(mt.Key(), e.Index)
	zero := s.c.prog.zero(mt.Elem())
	structElem := isAggregate(mt.Elem())
	if !commaOK {
		return func(fr *frame) Value {
			if v, ok := x(fr).(*mapVal).get(k(fr)); ok {
				return v
			}
			return zero()
		}
	}
	return func(fr *frame) Value {
		v, ok := x(fr).(*mapVal).get(k(fr))
		switch {
		case !ok:
			v = zero()
		case structElem:
			v = v.(*structVal).clone(fr.m)
		}
		return tuple{v, ok}
	}
}

func (mv *mapVal) get(k Value) (Value, bool) {
	if mv == nil {
		return nil, false
	}
	v, ok := mv.m[k]
	return v, ok
}

func (mv *mapVal) set(m *machine, k, v Value) {
	if mv == nil {
		throw("assignment to entry in nil map")
	}
	if _, exists := mv.m[k]; !exists {
		size := int64(entryBytes)
		if str, ok := k.(string); ok {
			size += int64(len(str))
		}
		m.alloc(size)
	}
	mv.m[k] = v
}

func (s *scope) sliceExpr(e *ast.SliceExpr) expr {
	x := s.expr(e.X)
	opt := func(e ast.Expr) expr {
		if e == nil {
			return nil
		}
		return s.intExpr(e)
	}
	lo, hi, max := opt(e.Low), opt(e.High), opt(e.Max)
	bound := func(fr *frame, b expr, def int) int {
		if b == nil {
			return def
		}
		return b(fr).(int)
	}
	if b, ok := s.typeOf(e.X).Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
		return func(fr *frame) Value {
			str := x(fr).(string)
			return str[bound(fr, lo, 0):bound(fr, hi, len(str))]
		}
	}
	asSlice := func(v Value) []Value { return v.([]Value) }
	switch s.typeOf(e.X).Underlying().(type) {
	case *types.Array, *types.Pointer:
		asSlice = func(v Value) []Value { return derefBox(v).f }
	}
	return func(fr *frame) Value {
		sl := asSlice(x(fr))
		l, h := bound(fr, lo, 0), bound(fr, hi, len(sl))
		if max != nil {
			return sl[l:h:max(fr).(int)]
		}
		return sl[l:h]
	}
}

func (s *scope) composite(e *ast.CompositeLit) expr {
	t := s.typeOf(e)
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		t = ptr.Elem()
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		zero := s.c.prog.zero(t)
		type fieldInit struct {
			i int
			v expr
		}
		var inits []fieldInit
		for i, el := range e.Elts {
			fi := i
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				name := kv.Key.(*ast.Ident).Name
				for j := range u.NumFields() {
					if u.Field(j).Name() == name {
						fi = j
					}
				}
				el = kv.Value
			}
			inits = append(inits, fieldInit{fi, s.convertTo(u.Field(fi).Type(), el)})
		}
		size := int64(headerBytes + slotBytes*u.NumFields())
		return func(fr *frame) Value {
			fr.m.alloc(size)
			sv := zero().(*structVal)
			for _, in := range inits {
				sv.f[in.i] = in.v(fr)
			}
			return sv
		}
	case *types.Array:
		idx, elems, _ := s.indexedElems(e, u.Elem())
		zero := s.c.prog.zero(t)
		size := int64(headerBytes + slotBytes*u.Len())
		return func(fr *frame) Value {
			fr.m.alloc(size)
			box := zero().(*structVal)
			for i, el := range elems {
				box.f[idx[i]] = el(fr)
			}
			return box
		}
	case *types.Slice:
		idx, elems, n := s.indexedElems(e, u.Elem())
		elemZero := s.c.prog.zero(u.Elem())
		size := int64(headerBytes + slotBytes*n)
		dense := n == len(elems)
		return func(fr *frame) Value {
			fr.m.alloc(size)
			out := make([]Value, n)
			if !dense {
				for i := range out {
					out[i] = elemZero()
				}
			}
			for i, el := range elems {
				out[idx[i]] = el(fr)
			}
			return out
		}
	case *types.Map:
		keys := make([]expr, len(e.Elts))
		vals := make([]expr, len(e.Elts))
		for i, el := range e.Elts {
			kv := el.(*ast.KeyValueExpr)
			keys[i] = s.convertTo(u.Key(), kv.Key)
			vals[i] = s.convertTo(u.Elem(), kv.Value)
		}
		return func(fr *frame) Value {
			fr.m.alloc(headerBytes)
			mv := &mapVal{m: make(map[Value]Value, len(keys))}
			for i, k := range keys {
				mv.set(fr.m, k(fr), vals[i](fr))
			}
			return mv
		}
	}
	s.c.fail(e.Pos(), "unsupported composite literal of type %s", t)
	return nil
}

func (s *scope) indexedElems(e *ast.CompositeLit, elemT types.Type) ([]int, []expr, int) {
	idx := make([]int, len(e.Elts))
	elems := make([]expr, len(e.Elts))
	next, n := 0, 0
	for i, el := range e.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			k, _ := constant.Int64Val(s.c.info.Types[kv.Key].Value)
			next = int(k)
			el = kv.Value
		}
		idx[i] = next
		elems[i] = s.convertTo(elemT, el)
		next++
		n = max(n, next)
	}
	return idx, elems, n
}

func (s *scope) funcLit(e *ast.FuncLit) expr {
	sig := s.typeOf(e).(*types.Signature)
	free := s.c.freeVars(e)
	child := s.c.newScope(&funcCode{name: "func literal"}, s.captured)
	for i, v := range free {
		child.free[v] = i
	}
	child.compileFunc(sig, e.Body)
	code := child.code
	cells := make([]func(*frame) *cell, len(free))
	for i, v := range free {
		if j, ok := s.free[v]; ok {
			cells[i] = func(fr *frame) *cell { return fr.free[j] }
			continue
		}
		slot := s.locals[v]
		cells[i] = func(fr *frame) *cell { return fr.slots[slot].(*cell) }
	}
	size := int64(headerBytes + 8*len(free))
	return func(fr *frame) Value {
		fr.m.alloc(size)
		cl := &closure{fn: code, free: make([]*cell, len(cells))}
		for i, c := range cells {
			cl.free[i] = c(fr)
		}
		return cl
	}
}

func (s *scope) unary(e *ast.UnaryExpr) expr {
	if e.Op == token.AND {
		return s.addressOf(e.X)
	}
	x := s.expr(e.X)
	switch e.Op {
	case token.ADD:
		return x
	case token.NOT:
		return func(fr *frame) Value { return !x(fr).(bool) }
	case token.ARROW:
		zero := s.c.prog.zero(s.typeOf(e))
		return func(fr *frame) Value {
			v, _ := fr.m.recv(x(fr).(*chanVal), zero)
			return v
		}
	case token.SUB, token.XOR:
		op := unaryOp(e.Op, s.typeOf(e).Underlying().(*types.Basic).Kind())
		return func(fr *frame) Value { return op(x(fr)) }
	}
	s.c.fail(e.Pos(), "unsupported operator %s", e.Op)
	return nil
}

func (s *scope) binary(e *ast.BinaryExpr) expr {
	switch e.Op {
	case token.LAND:
		l, r := s.expr(e.X), s.expr(e.Y)
		return func(fr *frame) Value { return l(fr).(bool) && r(fr).(bool) }
	case token.LOR:
		l, r := s.expr(e.X), s.expr(e.Y)
		return func(fr *frame) Value { return l(fr).(bool) || r(fr).(bool) }
	case token.EQL, token.NEQ:
		return s.equality(e)
	}
	t := defaultType(s.typeOf(e.X))
	if b, ok := t.(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
		t = defaultType(s.typeOf(e.Y))
	}
	op := s.binop(e.Op, t, e.Pos())
	l, r := s.expr(e.X), s.expr(e.Y)
	return func(fr *frame) Value { return op(fr.m, l(fr), r(fr)) }
}

func (s *scope) binop(op token.Token, t types.Type, pos token.Pos) binop {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		s.c.fail(pos, "operator %s not supported on %s", op, t)
	}
	switch op {
	case token.SHL, token.SHR:
		return shiftOp(op, b.Kind())
	case token.LSS, token.LEQ, token.GTR, token.GEQ:
		return compareOp(op, b.Kind())
	}
	f := arithOp(op, b.Kind())
	if f == nil {
		s.c.fail(pos, "operator %s not supported on %s", op, t)
	}
	return f
}

func (s *scope) equality(e *ast.BinaryExpr) expr {
	xNil, yNil := s.c.info.Types[e.X].IsNil(), s.c.info.Types[e.Y].IsNil()
	neq := e.Op == token.NEQ
	if xNil || yNil {
		other := e.X
		if xNil {
			other = e.Y
		}
		x := s.expr(other)
		isNil := nilCheck(s.typeOf(other))
		return func(fr *frame) Value { return isNil(x(fr)) != neq }
	}
	xt, yt := s.typeOf(e.X), s.typeOf(e.Y)
	t := defaultType(xt)
	var l, r expr
	switch {
	case isInterface(xt) && !isInterface(yt):
		l, r = s.expr(e.X), s.convertTo(xt, e.Y)
	case isInterface(yt) && !isInterface(xt):
		t = yt
		l, r = s.convertTo(yt, e.X), s.expr(e.Y)
	default:
		if b, ok := t.(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
			t = defaultType(yt)
		}
		l, r = s.expr(e.X), s.expr(e.Y)
	}
	eq := equalFn(t)
	return func(fr *frame) Value { return eq(l(fr), r(fr)) != neq }
}

func nilCheck(t types.Type) func(Value) bool {
	switch t.Underlying().(type) {
	case *types.Slice:
		return func(v Value) bool { return v.([]Value) == nil }
	case *types.Map:
		return func(v Value) bool { return v.(*mapVal) == nil }
	case *types.Pointer:
		return isNilPointer
	case *types.Chan:
		return func(v Value) bool { return v.(*chanVal) == nil }
	case *types.Signature:
		return func(v Value) bool {
			switch f := v.(type) {
			case *closure:
				return f == nil
			case *hostFunc:
				return f == nil
			case *boundMethod:
				return f == nil
			}
			return v == nil
		}
	}
	return func(v Value) bool { return v == nil }
}

func equalFn(t types.Type) func(a, b Value) bool {
	if _, ok := t.Underlying().(*types.Basic); ok {
		return func(a, b Value) bool { return a == b }
	}
	return func(a, b Value) bool { return equalAt(t, a, b) }
}

func (s *scope) typeAssert(e *ast.TypeAssertExpr, commaOK bool) expr {
	x := s.expr(e.X)
	t := s.typeOf(e.Type)
	ti, toIface := t.Underlying().(*types.Interface)
	structT := isAggregate(t)
	zero := s.c.prog.zero(t)
	srcT := s.typeOf(e.X)
	return func(fr *frame) Value {
		v := x(fr)
		var out Value
		ok := false
		if v != nil {
			iv := v.(iface)
			if toIface {
				ok, out = types.Implements(iv.t, ti), iv
			} else if ok = types.Identical(iv.t, t); ok {
				out = iv.v
			}
		}
		if commaOK {
			switch {
			case !ok:
				out = zero()
			case structT:
				out = out.(*structVal).clone(fr.m)
			}
			return tuple{out, ok}
		}
		if !ok {
			if v == nil {
				throw("interface conversion: interface is nil, not " + t.String())
			}
			throw("interface conversion: " + srcT.String() + " is " + v.(iface).t.String() + ", not " + t.String())
		}
		return out
	}
}

func isFresh(e ast.Expr) bool {
	switch ast.Unparen(e).(type) {
	case *ast.CompositeLit, *ast.CallExpr, *ast.FuncLit:
		return true
	}
	return false
}

func (s *scope) value(e ast.Expr) expr {
	x := s.expr(e)
	if isAggregate(s.typeOf(e)) && !isFresh(e) {
		return func(fr *frame) Value { return x(fr).(*structVal).clone(fr.m) }
	}
	return x
}

func (s *scope) convertTo(target types.Type, e ast.Expr) expr {
	if s.c.info.Types[e].IsNil() {
		zero := s.c.prog.zero(target)
		return func(*frame) Value { return zero() }
	}
	x := s.value(e)
	if conv := s.conv(target, s.typeOf(e)); conv != nil {
		return func(fr *frame) Value { return conv(fr.m, x(fr)) }
	}
	return x
}

func (s *scope) conv(target, src types.Type) func(*machine, Value) Value {
	if isInterface(target) && !isInterface(src) {
		dyn := defaultType(src)
		return func(_ *machine, v Value) Value { return iface{t: dyn, v: v} }
	}
	return nil
}

func (s *scope) conversion(to types.Type, arg ast.Expr) expr {
	if isInterface(to) {
		return s.convertTo(to, arg)
	}
	x := s.value(arg)
	if _, fromSlice := s.typeOf(arg).Underlying().(*types.Slice); fromSlice {
		target := to.Underlying()
		viaPointer := false
		if p, ok := target.(*types.Pointer); ok {
			target, viaPointer = p.Elem().Underlying(), true
		}
		if at, ok := target.(*types.Array); ok {
			n := int(at.Len())
			info := s.c.prog.structInfo(at)
			return func(fr *frame) Value {
				sl := x(fr).([]Value)
				if len(sl) < n {
					throw(fmt.Sprintf("cannot convert slice with length %d to array or pointer to array with length %d", len(sl), n))
				}
				if viaPointer {
					return &structVal{info: info, f: sl[:n:n]}
				}
				return (&structVal{info: info, f: sl[:n:n]}).clone(fr.m)
			}
		}
	}
	fb, fok := s.typeOf(arg).Underlying().(*types.Basic)
	tb, tok := to.Underlying().(*types.Basic)
	switch {
	case fok && tok && tb.Info()&types.IsString != 0 && fb.Info()&types.IsInteger != 0:
		return func(fr *frame) Value {
			r := rune(toInt64(x(fr)))
			if !utf8.ValidRune(r) {
				r = utf8.RuneError
			}
			return string(r)
		}
	case fok && tok && tb.Info()&types.IsNumeric != 0:
		conv := numericConv(fb.Kind(), tb.Kind())
		return func(fr *frame) Value { return conv(x(fr)) }
	case tok && tb.Info()&types.IsString != 0 && !fok:
		elem := s.typeOf(arg).Underlying().(*types.Slice).Elem().Underlying().(*types.Basic).Kind()
		return func(fr *frame) Value {
			sl := x(fr).([]Value)
			fr.m.alloc(int64(len(sl)))
			if elem == types.Int32 {
				rs := make([]rune, len(sl))
				for i, r := range sl {
					rs[i] = r.(rune)
				}
				return string(rs)
			}
			bs := make([]byte, len(sl))
			for i, b := range sl {
				bs[i] = b.(byte)
			}
			return string(bs)
		}
	case fok && fb.Info()&types.IsString != 0 && !tok:
		elem := to.Underlying().(*types.Slice).Elem().Underlying().(*types.Basic).Kind()
		return func(fr *frame) Value {
			str := x(fr).(string)
			fr.m.alloc(headerBytes + slotBytes*int64(len(str)))
			if elem == types.Int32 {
				out := make([]Value, 0, utf8.RuneCountInString(str))
				for _, r := range str {
					out = append(out, r)
				}
				return out
			}
			out := make([]Value, len(str))
			for i := range len(str) {
				out[i] = str[i]
			}
			return out
		}
	}
	return x
}

func (s *scope) addressOf(e ast.Expr) expr {
	if ov, ok := s.overrides[e]; ok {
		return ov
	}
	t := s.typeOf(e)
	agg := isAggregate(t)
	switch x := ast.Unparen(e).(type) {
	case *ast.CompositeLit:
		v := s.composite(x)
		if agg {
			return v
		}
		return func(fr *frame) Value {
			fr.m.alloc(slotBytes)
			c := &cell{v(fr)}
			return &c.v
		}
	case *ast.Ident:
		v := s.object(x).(*types.Var)
		if agg {
			return s.varLoad(v, x.Pos())
		}
		return s.varAddr(v, x.Pos())
	case *ast.SelectorExpr:
		if sel, ok := s.c.info.Selections[x]; ok && sel.Kind() == types.FieldVal {
			base, i := s.fieldBase(x)
			if agg {
				return func(fr *frame) Value { return base(fr).(*structVal).f[i] }
			}
			return func(fr *frame) Value { return &base(fr).(*structVal).f[i] }
		}
	case *ast.IndexExpr:
		col := s.expr(x.X)
		idx := s.intExpr(x.Index)
		elems := func(v Value) []Value { return derefBox(v).f }
		if _, ok := s.typeOf(x.X).Underlying().(*types.Slice); ok {
			elems = func(v Value) []Value { return v.([]Value) }
		}
		if agg {
			return func(fr *frame) Value { return elems(col(fr))[idx(fr).(int)] }
		}
		return func(fr *frame) Value { return &elems(col(fr))[idx(fr).(int)] }
	case *ast.StarExpr:
		return s.expr(x.X)
	}
	s.c.fail(e.Pos(), "cannot take the address of %T", e)
	return nil
}

func (s *scope) varAddr(v *types.Var, pos token.Pos) expr {
	if g, ok := s.c.globals[v]; ok {
		return func(fr *frame) Value { return &fr.m.globals[g] }
	}
	if i, ok := s.free[v]; ok {
		return func(fr *frame) Value { return &fr.free[i].v }
	}
	slot, ok := s.locals[v]
	if !ok || !s.captured[v] {
		s.c.fail(pos, "internal: address of %s was not escape-analyzed", v.Name())
	}
	return func(fr *frame) Value { return &fr.slots[slot].(*cell).v }
}
