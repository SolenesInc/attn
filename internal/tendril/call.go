package tendril

import (
	"go/ast"
	"go/token"
	"go/types"
)

type argList struct {
	args   []expr
	spread func(*frame) []Value
}

func (a argList) eval(fr *frame) []Value {
	if a.spread != nil {
		return a.spread(fr)
	}
	vals := make([]Value, len(a.args))
	for i, x := range a.args {
		vals[i] = x(fr)
	}
	return vals
}

func (s *scope) args(sig *types.Signature, e *ast.CallExpr) argList {
	params := sig.Params()
	if len(e.Args) == 1 {
		if tup, ok := s.typeOf(e.Args[0]).(*types.Tuple); ok {
			src := s.expr(e.Args[0])
			convs := make([]func(*machine, Value) Value, tup.Len())
			for i := range convs {
				convs[i] = s.conv(params.At(min(i, params.Len()-1)).Type(), tup.At(i).Type())
			}
			if sig.Variadic() {
				s.c.fail(e.Pos(), "passing multiple results to a variadic function is not supported")
			}
			return argList{spread: func(fr *frame) []Value {
				t := src(fr).(tuple)
				out := make([]Value, len(t))
				for i, v := range t {
					if convs[i] != nil {
						v = convs[i](fr.m, v)
					}
					out[i] = v
				}
				return out
			}}
		}
	}
	n := params.Len()
	var args []expr
	if sig.Variadic() && !e.Ellipsis.IsValid() {
		for i := range n - 1 {
			args = append(args, s.convertTo(params.At(i).Type(), e.Args[i]))
		}
		elemT := params.At(n - 1).Type().(*types.Slice).Elem()
		var rest []expr
		for _, a := range e.Args[n-1:] {
			rest = append(rest, s.convertTo(elemT, a))
		}
		args = append(args, func(fr *frame) Value {
			if len(rest) == 0 {
				return []Value(nil)
			}
			fr.m.alloc(headerBytes + slotBytes*int64(len(rest)))
			out := make([]Value, len(rest))
			for i, r := range rest {
				out[i] = r(fr)
			}
			return out
		})
		return argList{args: args}
	}
	for i, a := range e.Args {
		args = append(args, s.convertTo(params.At(i).Type(), a))
	}
	return argList{args: args}
}

func (s *scope) call(e *ast.CallExpr) expr {
	ftv := s.c.info.Types[e.Fun]
	if ftv.IsType() {
		return s.conversion(ftv.Type, e.Args[0])
	}
	if ftv.IsBuiltin() {
		return s.builtin(e)
	}
	sig := s.typeOf(e.Fun).Underlying().(*types.Signature)
	var callee types.Object
	var calleeIdent *ast.Ident
	switch f := ast.Unparen(e.Fun).(type) {
	case *ast.Ident:
		callee, calleeIdent = s.c.info.Uses[f], f
	case *ast.SelectorExpr:
		if sel, ok := s.c.info.Selections[f]; ok {
			if sel.Kind() == types.MethodVal {
				return s.methodCall(e, f, sel, sig)
			}
		} else {
			callee, calleeIdent = s.c.info.Uses[f.Sel], f.Sel
		}
	}
	if fn, ok := callee.(*types.Func); ok {
		if code, ok := s.c.funcs[fn]; ok {
			return s.staticCall(code, s.args(sig, e))
		}
		if hf, ok := s.c.prog.env.funcs[fn]; ok {
			args := s.args(sig, e)
			return func(fr *frame) Value { return hf.call(fr.m, args.eval(fr)) }
		}
		if nf, ok := s.c.prog.env.natives[fn]; ok {
			targs := s.c.info.Instances[calleeIdent].TypeArgs
			typeArgs := make([]types.Type, targs.Len())
			for i := range typeArgs {
				typeArgs[i] = targs.At(i)
			}
			args := s.args(sig, e)
			return func(fr *frame) Value {
				fr.m.burn(1)
				return nf.impl(fr.m, typeArgs, args.eval(fr))
			}
		}
	}
	f := s.expr(e.Fun)
	args := s.args(sig, e)
	return func(fr *frame) Value { return fr.m.callValue(f(fr), args.eval(fr)) }
}

func (s *scope) staticCall(code *funcCode, args argList) expr {
	if args.spread != nil {
		return func(fr *frame) Value {
			vals := args.spread(fr)
			nf := fr.m.newFrame(code, nil)
			for i, v := range vals {
				nf.param(code, i, v)
			}
			return fr.m.exec(code, nf)
		}
	}
	list := args.args
	return func(fr *frame) Value {
		nf := fr.m.newFrame(code, nil)
		for i, a := range list {
			nf.param(code, i, a(fr))
		}
		return fr.m.exec(code, nf)
	}
}

func (m *machine) callValue(fv Value, args []Value) Value {
	switch fn := fv.(type) {
	case *closure:
		if fn != nil {
			nf := m.newFrame(fn.fn, fn.free)
			for i, a := range args {
				nf.param(fn.fn, i, a)
			}
			return m.exec(fn.fn, nf)
		}
	case *hostFunc:
		if fn != nil {
			return fn.call(m, args)
		}
	}
	throw("invalid memory address or nil pointer dereference")
	return nil
}

func (s *scope) methodCall(e *ast.CallExpr, f *ast.SelectorExpr, sel *types.Selection, sig *types.Signature) expr {
	fn := sel.Obj().(*types.Func)
	args := s.args(sig, e)
	if isInterface(sel.Recv()) {
		x := s.expr(f.X)
		name := fn.Name()
		return func(fr *frame) Value {
			v := x(fr)
			if v == nil {
				throw("invalid memory address or nil pointer dereference")
			}
			return fr.m.callMethod(v.(iface), name, args.eval(fr))
		}
	}
	code, ok := s.c.funcs[fn]
	if !ok {
		s.c.fail(f.Pos(), "methods of host types are not supported")
	}
	if len(sel.Index()) > 1 {
		s.c.fail(f.Pos(), "promoted methods are not supported")
	}
	_, xIsPtr := sel.Recv().Underlying().(*types.Pointer)
	var recv expr
	switch {
	case code.ptrRecv:
		recv = s.expr(f.X)
	case xIsPtr:
		x := s.expr(f.X)
		recv = func(fr *frame) Value {
			sv := x(fr).(*structVal)
			if sv == nil {
				throw("invalid memory address or nil pointer dereference")
			}
			return sv.clone(fr.m)
		}
	default:
		recv = s.value(f.X)
	}
	list := args.args
	if args.spread != nil {
		return func(fr *frame) Value {
			nf := fr.m.newFrame(code, nil)
			nf.param(code, 0, recv(fr))
			for i, v := range args.spread(fr) {
				nf.param(code, i+1, v)
			}
			return fr.m.exec(code, nf)
		}
	}
	return func(fr *frame) Value {
		nf := fr.m.newFrame(code, nil)
		nf.param(code, 0, recv(fr))
		for i, a := range list {
			nf.param(code, i+1, a(fr))
		}
		return fr.m.exec(code, nf)
	}
}

func (s *scope) builtin(e *ast.CallExpr) expr {
	id := ast.Unparen(e.Fun).(*ast.Ident)
	switch id.Name {
	case "len":
		x := s.expr(e.Args[0])
		switch u := s.typeOf(e.Args[0]).Underlying().(type) {
		case *types.Basic:
			return func(fr *frame) Value { return len(x(fr).(string)) }
		case *types.Slice:
			return func(fr *frame) Value { return len(x(fr).([]Value)) }
		case *types.Map:
			return func(fr *frame) Value { return x(fr).(*mapVal).len() }
		default:
			s.c.fail(e.Pos(), "len of %s is not supported", u)
		}
	case "cap":
		x := s.expr(e.Args[0])
		return func(fr *frame) Value { return cap(x(fr).([]Value)) }
	case "append":
		return s.appendCall(e)
	case "make":
		return s.makeCall(e)
	case "new":
		t := s.typeOf(e.Args[0])
		if _, ok := t.Underlying().(*types.Struct); !ok {
			s.c.fail(e.Pos(), "new is only supported for struct types")
		}
		zero := s.c.prog.zero(t)
		size := int64(headerBytes + slotBytes*t.Underlying().(*types.Struct).NumFields())
		return func(fr *frame) Value {
			fr.m.alloc(size)
			return zero()
		}
	case "delete":
		mt := s.typeOf(e.Args[0]).Underlying().(*types.Map)
		x, k := s.expr(e.Args[0]), s.convertTo(mt.Key(), e.Args[1])
		return func(fr *frame) Value {
			if mv := x(fr).(*mapVal); mv != nil {
				delete(mv.m, k(fr))
			}
			return nil
		}
	case "copy":
		dst, src := s.expr(e.Args[0]), s.expr(e.Args[1])
		if b, ok := s.typeOf(e.Args[1]).Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
			return func(fr *frame) Value {
				d, str := dst(fr).([]Value), src(fr).(string)
				n := min(len(d), len(str))
				fr.m.burn(int64(n / 8))
				for i := range n {
					d[i] = str[i]
				}
				return n
			}
		}
		return func(fr *frame) Value {
			n := copy(dst(fr).([]Value), src(fr).([]Value))
			fr.m.burn(int64(n / 8))
			return n
		}
	case "clear":
		x := s.expr(e.Args[0])
		if mt, ok := s.typeOf(e.Args[0]).Underlying().(*types.Slice); ok {
			zero := s.c.prog.zero(mt.Elem())
			return func(fr *frame) Value {
				sl := x(fr).([]Value)
				fr.m.burn(int64(len(sl) / 8))
				for i := range sl {
					sl[i] = zero()
				}
				return nil
			}
		}
		return func(fr *frame) Value {
			if mv := x(fr).(*mapVal); mv != nil {
				clear(mv.m)
			}
			return nil
		}
	case "panic":
		v := s.convertTo(anyType, e.Args[0])
		return func(fr *frame) Value { panic(scriptPanic{v(fr)}) }
	case "min", "max":
		t := defaultType(s.typeOf(e))
		tok := token.LSS
		if id.Name == "max" {
			tok = token.GTR
		}
		better := compareOp(tok, t.Underlying().(*types.Basic).Kind())
		args := make([]expr, len(e.Args))
		for i, a := range e.Args {
			args[i] = s.convertTo(t, a)
		}
		return func(fr *frame) Value {
			best := args[0](fr)
			for _, a := range args[1:] {
				if v := a(fr); better(fr.m, v, best).(bool) {
					best = v
				}
			}
			return best
		}
	}
	s.c.fail(e.Pos(), "builtin %s is not available in scripts", id.Name)
	return nil
}

func (s *scope) appendCall(e *ast.CallExpr) expr {
	st := s.typeOf(e).Underlying().(*types.Slice)
	base := s.expr(e.Args[0])
	zero := s.c.prog.zero(st.Elem())
	if e.Ellipsis.IsValid() {
		other := s.expr(e.Args[1])
		if b, ok := s.typeOf(e.Args[1]).Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
			return func(fr *frame) Value {
				str := other(fr).(string)
				vals := make([]Value, len(str))
				for i := range len(str) {
					vals[i] = str[i]
				}
				return appendValues(fr.m, base(fr).([]Value), zero, vals...)
			}
		}
		return func(fr *frame) Value {
			return appendValues(fr.m, base(fr).([]Value), zero, other(fr).([]Value)...)
		}
	}
	elems := make([]expr, len(e.Args)-1)
	for i, a := range e.Args[1:] {
		elems[i] = s.convertTo(st.Elem(), a)
	}
	if len(elems) == 1 {
		el := elems[0]
		return func(fr *frame) Value {
			sl := base(fr).([]Value)
			return appendValues(fr.m, sl, zero, el(fr))
		}
	}
	return func(fr *frame) Value {
		sl := base(fr).([]Value)
		vals := make([]Value, len(elems))
		for i, el := range elems {
			vals[i] = el(fr)
		}
		return appendValues(fr.m, sl, zero, vals...)
	}
}

func appendValues(m *machine, s []Value, zero func() Value, vals ...Value) []Value {
	need := len(s) + len(vals)
	if need > cap(s) {
		newcap := growCap(cap(s), need)
		m.alloc(headerBytes + slotBytes*int64(newcap))
		m.burn(int64(newcap / 8))
		ns := make([]Value, len(s), newcap)
		copy(ns, s)
		for i := need; i < newcap; i++ {
			ns[:newcap][i] = zero()
		}
		s = ns
	}
	return append(s, vals...)
}

func growCap(old, need int) int {
	double := 2 * old
	if need > double {
		return need
	}
	if old < 256 {
		return double
	}
	n := old
	for n < need {
		n += (n + 768) / 4
	}
	return n
}

func (s *scope) makeCall(e *ast.CallExpr) expr {
	t := s.typeOf(e.Args[0])
	switch u := t.Underlying().(type) {
	case *types.Map:
		return func(fr *frame) Value {
			fr.m.alloc(headerBytes)
			return &mapVal{m: map[Value]Value{}}
		}
	case *types.Slice:
		zero := s.c.prog.zero(u.Elem())
		n := s.intExpr(e.Args[1])
		c := n
		if len(e.Args) > 2 {
			c = s.intExpr(e.Args[2])
		}
		return func(fr *frame) Value {
			ln, cp := n(fr).(int), c(fr).(int)
			if ln < 0 || ln > cp {
				throw("makeslice: len out of range")
			}
			fr.m.alloc(headerBytes + slotBytes*int64(cp))
			fr.m.burn(int64(cp / 8))
			out := make([]Value, ln, cp)
			full := out[:cp]
			for i := range full {
				full[i] = zero()
			}
			return out
		}
	}
	s.c.fail(e.Pos(), "make of %s is not supported", t)
	return nil
}
