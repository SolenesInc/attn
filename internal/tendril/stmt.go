package tendril

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
)

func (s *scope) block(list []ast.Stmt) stmtFn {
	fns := make([]stmtFn, 0, len(list))
	poss := make([]token.Pos, 0, len(list))
	for _, st := range list {
		if _, ok := st.(*ast.EmptyStmt); ok {
			continue
		}
		fns = append(fns, s.stmt(st))
		poss = append(poss, st.Pos())
	}
	return func(fr *frame) flow {
		m := fr.m
		for i, f := range fns {
			m.pos = poss[i]
			m.fuel--
			if m.fuel < m.checkpoint {
				m.checkpointReached()
			}
			if fl := f(fr); fl != flowNext {
				return fl
			}
		}
		return flowNext
	}
}

func (s *scope) stmt(st ast.Stmt) stmtFn {
	switch st := st.(type) {
	case *ast.ExprStmt:
		x := s.expr(st.X)
		return func(fr *frame) flow {
			x(fr)
			return flowNext
		}
	case *ast.AssignStmt:
		return s.assign(st)
	case *ast.IncDecStmt:
		return s.incDec(st)
	case *ast.DeclStmt:
		return s.declStmt(st)
	case *ast.ReturnStmt:
		return s.returnStmt(st)
	case *ast.BlockStmt:
		return s.block(st.List)
	case *ast.IfStmt:
		return s.ifStmt(st)
	case *ast.ForStmt:
		return s.forStmt(st)
	case *ast.RangeStmt:
		return s.rangeStmt(st)
	case *ast.SwitchStmt:
		return s.switchStmt(st)
	case *ast.TypeSwitchStmt:
		return s.typeSwitchStmt(st)
	case *ast.BranchStmt:
		var label *types.Label
		if st.Label != nil {
			label = s.c.info.Uses[st.Label].(*types.Label)
		}
		switch {
		case st.Tok == token.FALLTHROUGH:
			return func(*frame) flow { return flowFallthrough }
		case st.Tok == token.BREAK && label == nil:
			return func(*frame) flow { return flowBreak }
		case st.Tok == token.CONTINUE && label == nil:
			return func(*frame) flow { return flowContinue }
		case st.Tok == token.BREAK:
			return func(fr *frame) flow {
				fr.m.label = label
				return flowBreakLabel
			}
		case st.Tok == token.CONTINUE:
			return func(fr *frame) flow {
				fr.m.label = label
				return flowContinueLabel
			}
		}
	case *ast.LabeledStmt:
		s.label = s.c.info.Defs[st.Label].(*types.Label)
		inner := s.stmt(st.Stmt)
		s.label = nil
		return inner
	case *ast.DeferStmt:
		return s.deferStmt(st)
	case *ast.GoStmt:
		return s.goStmt(st)
	case *ast.SendStmt:
		ch := s.expr(st.Chan)
		v := s.convertTo(s.typeOf(st.Chan).Underlying().(*types.Chan).Elem(), st.Value)
		return func(fr *frame) flow {
			c := ch(fr).(*chanVal)
			fr.m.send(c, v(fr))
			return flowNext
		}
	case *ast.SelectStmt:
		return s.selectStmt(st)
	case *ast.EmptyStmt:
		return func(*frame) flow { return flowNext }
	}
	s.c.fail(st.Pos(), "unsupported statement %T", st)
	return nil
}

type lvalue struct {
	prep  func(fr *frame) (Value, Value)
	load  func(fr *frame, obj, key Value) Value
	store func(fr *frame, obj, key, v Value)
}

func (lv lvalue) ref(fr *frame) (Value, Value) {
	if lv.prep == nil {
		return nil, nil
	}
	return lv.prep(fr)
}

func (s *scope) object(id *ast.Ident) types.Object {
	if obj := s.c.info.Defs[id]; obj != nil {
		return obj
	}
	return s.c.info.Uses[id]
}

func (s *scope) lvalue(e ast.Expr, define bool) lvalue {
	lv, inPlace := s.lvalueOf(e, define)
	if inPlace && isAggregate(s.typeOf(e)) {
		load := lv.load
		lv.store = func(fr *frame, obj, key, v Value) {
			assignAgg(load(fr, obj, key).(*structVal), v.(*structVal))
		}
	}
	return lv
}

func (s *scope) lvalueOf(e ast.Expr, define bool) (lvalue, bool) {
	switch e := ast.Unparen(e).(type) {
	case *ast.Ident:
		if e.Name == "_" {
			return lvalue{store: func(*frame, Value, Value, Value) {}}, false
		}
		v := s.object(e).(*types.Var)
		if define && s.c.info.Defs[e] != nil {
			return s.declLvalue(v), false
		}
		return s.varLvalue(v, e.Pos()), true
	case *ast.IndexExpr:
		x := s.expr(e.X)
		switch xt := s.typeOf(e.X).Underlying().(type) {
		case *types.Slice:
			idx := s.intExpr(e.Index)
			return lvalue{
				prep:  func(fr *frame) (Value, Value) { return x(fr), idx(fr) },
				load:  func(_ *frame, obj, key Value) Value { return obj.([]Value)[key.(int)] },
				store: func(_ *frame, obj, key, v Value) { obj.([]Value)[key.(int)] = v },
			}, true
		case *types.Array, *types.Pointer:
			idx := s.intExpr(e.Index)
			return lvalue{
				prep:  func(fr *frame) (Value, Value) { return derefBox(x(fr)), idx(fr) },
				load:  func(_ *frame, obj, key Value) Value { return obj.(*structVal).f[key.(int)] },
				store: func(_ *frame, obj, key, v Value) { obj.(*structVal).f[key.(int)] = v },
			}, true
		case *types.Map:
			k := s.convertTo(xt.Key(), e.Index)
			zero := s.c.prog.zero(xt.Elem())
			return lvalue{
				prep: func(fr *frame) (Value, Value) { return x(fr), k(fr) },
				load: func(_ *frame, obj, key Value) Value {
					if v, ok := obj.(*mapVal).get(key); ok {
						return v
					}
					return zero()
				},
				store: func(fr *frame, obj, key, v Value) { obj.(*mapVal).set(fr.m, key, v) },
			}, false
		}
	case *ast.SelectorExpr:
		base, i := s.fieldBase(e)
		return lvalue{
			prep:  func(fr *frame) (Value, Value) { return base(fr), nil },
			load:  func(_ *frame, obj, _ Value) Value { return obj.(*structVal).f[i] },
			store: func(_ *frame, obj, _, v Value) { obj.(*structVal).f[i] = v },
		}, true
	case *ast.StarExpr:
		p := s.expr(e.X)
		if isAggregate(s.typeOf(e)) {
			return lvalue{
				prep: func(fr *frame) (Value, Value) { return derefBox(p(fr)), nil },
				load: func(_ *frame, obj, _ Value) Value { return obj },
			}, true
		}
		return lvalue{
			prep:  func(fr *frame) (Value, Value) { return derefSlot(p(fr)), nil },
			load:  func(_ *frame, obj, _ Value) Value { return *obj.(*Value) },
			store: func(_ *frame, obj, _, v Value) { *obj.(*Value) = v },
		}, false
	}
	s.c.fail(e.Pos(), "cannot assign to %T", e)
	return lvalue{}, false
}

func derefBox(v Value) *structVal {
	sv := v.(*structVal)
	if sv == nil {
		throw("invalid memory address or nil pointer dereference")
	}
	return sv
}

func derefSlot(v Value) *Value {
	p := v.(*Value)
	if p == nil {
		throw("invalid memory address or nil pointer dereference")
	}
	return p
}

func (s *scope) declLvalue(v types.Object) lvalue {
	slot := s.declare(v)
	if s.captured[v] {
		return lvalue{store: func(fr *frame, _, _, val Value) { fr.slots[slot] = &cell{val} }}
	}
	return lvalue{store: func(fr *frame, _, _, val Value) { fr.slots[slot] = val }}
}

func (s *scope) varLvalue(v *types.Var, pos token.Pos) lvalue {
	if g, ok := s.c.globals[v]; ok {
		return lvalue{
			load:  func(fr *frame, _, _ Value) Value { return fr.m.globals[g] },
			store: func(fr *frame, _, _, val Value) { fr.m.globals[g] = val },
		}
	}
	if i, ok := s.free[v]; ok {
		return lvalue{
			load:  func(fr *frame, _, _ Value) Value { return fr.free[i].v },
			store: func(fr *frame, _, _, val Value) { fr.free[i].v = val },
		}
	}
	slot, ok := s.locals[v]
	if !ok {
		s.c.fail(pos, "internal: unresolved variable %s", v.Name())
	}
	if s.captured[v] {
		return lvalue{
			load:  func(fr *frame, _, _ Value) Value { return fr.slots[slot].(*cell).v },
			store: func(fr *frame, _, _, val Value) { fr.slots[slot].(*cell).v = val },
		}
	}
	return lvalue{
		load:  func(fr *frame, _, _ Value) Value { return fr.slots[slot] },
		store: func(fr *frame, _, _, val Value) { fr.slots[slot] = val },
	}
}

func (s *scope) lhsType(lhs, rhs ast.Expr) types.Type {
	if id, ok := ast.Unparen(lhs).(*ast.Ident); ok && id.Name == "_" {
		return defaultType(s.typeOf(rhs))
	}
	return s.typeOf(lhs)
}

func (s *scope) assign(st *ast.AssignStmt) stmtFn {
	define := st.Tok == token.DEFINE
	if st.Tok != token.DEFINE && st.Tok != token.ASSIGN {
		return s.opAssign(st)
	}
	if len(st.Lhs) == len(st.Rhs) {
		vals := make([]expr, len(st.Rhs))
		for i, r := range st.Rhs {
			vals[i] = s.convertTo(s.lhsType(st.Lhs[i], r), r)
		}
		lvs := make([]lvalue, len(st.Lhs))
		for i, l := range st.Lhs {
			lvs[i] = s.lvalue(l, define)
		}
		if len(lvs) == 1 {
			lv, val := lvs[0], vals[0]
			if lv.prep == nil {
				return func(fr *frame) flow {
					lv.store(fr, nil, nil, val(fr))
					return flowNext
				}
			}
			return func(fr *frame) flow {
				obj, key := lv.prep(fr)
				lv.store(fr, obj, key, val(fr))
				return flowNext
			}
		}
		return func(fr *frame) flow {
			objs := make([]Value, 2*len(lvs))
			for i, lv := range lvs {
				objs[2*i], objs[2*i+1] = lv.ref(fr)
			}
			vs := make([]Value, len(vals))
			for i, v := range vals {
				vs[i] = v(fr)
			}
			for i, lv := range lvs {
				lv.store(fr, objs[2*i], objs[2*i+1], vs[i])
			}
			return flowNext
		}
	}
	src, elemTypes := s.multiValue(st.Rhs[0])
	convs := make([]func(*machine, Value) Value, len(st.Lhs))
	for i, l := range st.Lhs {
		if id, ok := ast.Unparen(l).(*ast.Ident); ok && id.Name == "_" {
			continue
		}
		convs[i] = s.conv(s.typeOf(l), elemTypes[i])
	}
	lvs := make([]lvalue, len(st.Lhs))
	for i, l := range st.Lhs {
		lvs[i] = s.lvalue(l, define)
	}
	return func(fr *frame) flow {
		t := src(fr).(tuple)
		for i, lv := range lvs {
			v := t[i]
			if convs[i] != nil {
				v = convs[i](fr.m, v)
			}
			obj, key := lv.ref(fr)
			lv.store(fr, obj, key, v)
		}
		return flowNext
	}
}

func (s *scope) multiValue(rhs ast.Expr) (expr, []types.Type) {
	switch r := ast.Unparen(rhs).(type) {
	case *ast.IndexExpr:
		if mt, ok := s.typeOf(r.X).Underlying().(*types.Map); ok {
			return s.mapIndex(r, true), []types.Type{mt.Elem(), types.Typ[types.Bool]}
		}
	case *ast.TypeAssertExpr:
		return s.typeAssert(r, true), []types.Type{s.typeOf(r.Type), types.Typ[types.Bool]}
	case *ast.UnaryExpr:
		if r.Op == token.ARROW {
			elem := s.typeOf(r.X).Underlying().(*types.Chan).Elem()
			ch, zero := s.expr(r.X), s.c.prog.zero(elem)
			return func(fr *frame) Value {
				v, ok := fr.m.recv(ch(fr).(*chanVal), zero)
				return tuple{v, ok}
			}, []types.Type{elem, types.Typ[types.Bool]}
		}
	}
	tup := s.typeOf(rhs).(*types.Tuple)
	ts := make([]types.Type, tup.Len())
	for i := range ts {
		ts[i] = tup.At(i).Type()
	}
	return s.expr(rhs), ts
}

var assignOps = map[token.Token]token.Token{
	token.ADD_ASSIGN: token.ADD, token.SUB_ASSIGN: token.SUB, token.MUL_ASSIGN: token.MUL,
	token.QUO_ASSIGN: token.QUO, token.REM_ASSIGN: token.REM, token.AND_ASSIGN: token.AND,
	token.OR_ASSIGN: token.OR, token.XOR_ASSIGN: token.XOR, token.SHL_ASSIGN: token.SHL,
	token.SHR_ASSIGN: token.SHR, token.AND_NOT_ASSIGN: token.AND_NOT,
}

func (s *scope) opAssign(st *ast.AssignStmt) stmtFn {
	lv := s.lvalue(st.Lhs[0], false)
	op := s.binop(assignOps[st.Tok], s.typeOf(st.Lhs[0]), st.Pos())
	rhs := s.expr(st.Rhs[0])
	return func(fr *frame) flow {
		obj, key := lv.ref(fr)
		lv.store(fr, obj, key, op(fr.m, lv.load(fr, obj, key), rhs(fr)))
		return flowNext
	}
}

func (s *scope) incDec(st *ast.IncDecStmt) stmtFn {
	t := s.typeOf(st.X)
	lv := s.lvalue(st.X, false)
	tok := token.ADD
	if st.Tok == token.DEC {
		tok = token.SUB
	}
	op := s.binop(tok, t, st.Pos())
	one := convertBasic(1, t.Underlying().(*types.Basic).Kind())
	return func(fr *frame) flow {
		obj, key := lv.ref(fr)
		lv.store(fr, obj, key, op(fr.m, lv.load(fr, obj, key), one))
		return flowNext
	}
}

func (s *scope) declStmt(st *ast.DeclStmt) stmtFn {
	gd := st.Decl.(*ast.GenDecl)
	if gd.Tok != token.VAR {
		return func(*frame) flow { return flowNext }
	}
	var parts []stmtFn
	for _, spec := range gd.Specs {
		vs := spec.(*ast.ValueSpec)
		lhs := make([]ast.Expr, len(vs.Names))
		for i, n := range vs.Names {
			lhs[i] = n
		}
		if len(vs.Values) > 0 {
			parts = append(parts, s.assign(&ast.AssignStmt{Lhs: lhs, Tok: token.DEFINE, TokPos: vs.Pos(), Rhs: vs.Values}))
			continue
		}
		for _, n := range vs.Names {
			if n.Name == "_" {
				continue
			}
			v := s.c.info.Defs[n]
			zero := s.c.prog.zero(v.Type())
			size := int64(0)
			if isAggregate(v.Type()) {
				size = sizeOf(v.Type())
			}
			lv := s.declLvalue(v)
			parts = append(parts, func(fr *frame) flow {
				fr.m.alloc(size)
				lv.store(fr, nil, nil, zero())
				return flowNext
			})
		}
	}
	return func(fr *frame) flow {
		for _, p := range parts {
			p(fr)
		}
		return flowNext
	}
}

func (s *scope) returnStmt(st *ast.ReturnStmt) stmtFn {
	res := s.sig.Results()
	if len(st.Results) > 0 && len(s.code.results) > 0 && containsDeferIn(s) {
		lhs := make([]ast.Expr, res.Len())
		for i := range lhs {
			lhs[i] = &ast.Ident{Name: res.At(i).Name(), NamePos: st.Pos()}
			s.c.info.Uses[lhs[i].(*ast.Ident)] = res.At(i)
		}
		assign := s.assign(&ast.AssignStmt{Lhs: lhs, Tok: token.ASSIGN, TokPos: st.Pos(), Rhs: st.Results})
		return func(fr *frame) flow {
			assign(fr)
			return flowReturn
		}
	}
	switch {
	case len(st.Results) == 0 && len(s.code.results) > 0:
		loads := make([]expr, len(s.code.results))
		for i := range loads {
			loads[i] = s.varLoad(res.At(i), st.Pos())
		}
		return func(fr *frame) flow {
			if len(loads) == 1 {
				fr.ret = loads[0](fr)
				return flowReturn
			}
			t := make(tuple, len(loads))
			for i, l := range loads {
				t[i] = l(fr)
			}
			fr.ret = t
			return flowReturn
		}
	case len(st.Results) == 0:
		return func(fr *frame) flow { return flowReturn }
	case len(st.Results) == 1 && res.Len() == 1:
		v := s.convertTo(res.At(0).Type(), st.Results[0])
		return func(fr *frame) flow {
			fr.ret = v(fr)
			return flowReturn
		}
	case len(st.Results) == 1:
		src, elemTypes := s.multiValue(st.Results[0])
		convs := make([]func(*machine, Value) Value, res.Len())
		for i := range convs {
			convs[i] = s.conv(res.At(i).Type(), elemTypes[i])
		}
		return func(fr *frame) flow {
			t := src(fr).(tuple)
			out := make(tuple, len(t))
			for i, v := range t {
				if convs[i] != nil {
					v = convs[i](fr.m, v)
				}
				out[i] = v
			}
			fr.ret = out
			return flowReturn
		}
	}
	vals := make([]expr, len(st.Results))
	for i, r := range st.Results {
		vals[i] = s.convertTo(res.At(i).Type(), r)
	}
	return func(fr *frame) flow {
		t := make(tuple, len(vals))
		for i, v := range vals {
			t[i] = v(fr)
		}
		fr.ret = t
		return flowReturn
	}
}

func (s *scope) optStmt(st ast.Stmt) stmtFn {
	if st == nil {
		return nil
	}
	return s.stmt(st)
}

func (s *scope) ifStmt(st *ast.IfStmt) stmtFn {
	init := s.optStmt(st.Init)
	cond := s.expr(st.Cond)
	then := s.block(st.Body.List)
	els := s.optStmt(st.Else)
	return func(fr *frame) flow {
		if init != nil {
			init(fr)
		}
		if cond(fr).(bool) {
			return then(fr)
		}
		if els != nil {
			return els(fr)
		}
		return flowNext
	}
}

func (s *scope) forStmt(st *ast.ForStmt) stmtFn {
	label := s.takeLabel()
	init := s.optStmt(st.Init)
	var perIteration []int
	if a, ok := st.Init.(*ast.AssignStmt); ok && a.Tok == token.DEFINE {
		for _, l := range a.Lhs {
			if v := s.c.info.Defs[l.(*ast.Ident)]; v != nil && s.captured[v] {
				perIteration = append(perIteration, s.locals[v])
			}
		}
	}
	var cond expr
	if st.Cond != nil {
		cond = s.expr(st.Cond)
	}
	post := s.optStmt(st.Post)
	body := s.block(st.Body.List)
	return func(fr *frame) flow {
		if init != nil {
			init(fr)
		}
		m := fr.m
		for {
			if cond != nil && !cond(fr).(bool) {
				return flowNext
			}
			if exit, fl := loopFlow(m, body(fr), label); exit {
				return fl
			}
			m.burn(1)
			for _, slot := range perIteration {
				fr.slots[slot] = &cell{fr.slots[slot].(*cell).v}
			}
			if post != nil {
				post(fr)
			}
		}
	}
}

func (s *scope) rangeVar(e ast.Expr, define bool) *lvalue {
	if e == nil {
		return nil
	}
	if id, ok := e.(*ast.Ident); ok && id.Name == "_" {
		return nil
	}
	lv := s.lvalue(e, define)
	return &lv
}

func (lv *lvalue) set(fr *frame, v Value) {
	if lv == nil {
		return
	}
	obj, key := lv.ref(fr)
	lv.store(fr, obj, key, v)
}

func (s *scope) rangeStmt(st *ast.RangeStmt) stmtFn {
	label := s.takeLabel()
	define := st.Tok == token.DEFINE
	x := s.expr(st.X)
	key := s.rangeVar(st.Key, define)
	val := s.rangeVar(st.Value, define)
	body := s.block(st.Body.List)
	iterate := func(fr *frame) (stop bool, fl flow) {
		if exit, fl := loopFlow(fr.m, body(fr), label); exit {
			return true, fl
		}
		fr.m.burn(1)
		return false, flowNext
	}
	rangeT := s.typeOf(st.X).Underlying()
	if p, ok := rangeT.(*types.Pointer); ok {
		rangeT = p.Elem().Underlying()
	}
	switch xt := rangeT.(type) {
	case *types.Chan:
		zero := s.c.prog.zero(xt.Elem())
		return func(fr *frame) flow {
			c := x(fr).(*chanVal)
			for {
				v, ok := fr.m.recv(c, zero)
				if !ok {
					return flowNext
				}
				key.set(fr, v)
				if stop, fl := iterate(fr); stop {
					return fl
				}
			}
		}
	case *types.Slice, *types.Array:
		var elemT types.Type
		elems := func(v Value) []Value { return v.([]Value) }
		if at, ok := xt.(*types.Array); ok {
			elemT = at.Elem()
			_, viaPointer := s.typeOf(st.X).Underlying().(*types.Pointer)
			elems = func(v Value) []Value { return derefBox(v).f }
			if val != nil && !viaPointer {
				elems = func(v Value) []Value { return slices.Clone(v.(*structVal).f) }
			}
		} else {
			elemT = xt.(*types.Slice).Elem()
		}
		aggElem := isAggregate(elemT)
		return func(fr *frame) flow {
			sl := elems(x(fr))
			for i := range len(sl) {
				key.set(fr, i)
				if val != nil {
					v := sl[i]
					if aggElem {
						v = v.(*structVal).clone(fr.m)
					}
					val.set(fr, v)
				}
				if stop, fl := iterate(fr); stop {
					return fl
				}
			}
			return flowNext
		}
	case *types.Map:
		return func(fr *frame) flow {
			mv := x(fr).(*mapVal)
			if mv == nil {
				return flowNext
			}
			keys := mv.sortedKeys()
			fr.m.burn(int64(len(keys)))
			for _, k := range keys {
				v, ok := mv.m[k]
				if !ok {
					continue
				}
				key.set(fr, k)
				val.set(fr, v)
				if stop, fl := iterate(fr); stop {
					return fl
				}
			}
			return flowNext
		}
	case *types.Basic:
		if xt.Info()&types.IsString != 0 {
			return func(fr *frame) flow {
				str := x(fr).(string)
				for i, r := range str {
					key.set(fr, i)
					val.set(fr, r)
					if stop, fl := iterate(fr); stop {
						return fl
					}
				}
				return flowNext
			}
		}
		kind := defaultType(xt).(*types.Basic).Kind()
		return func(fr *frame) flow {
			n := toInt64(x(fr))
			for i := range n {
				key.set(fr, convertBasic(i, kind))
				if stop, fl := iterate(fr); stop {
					return fl
				}
			}
			return flowNext
		}
	}
	s.c.fail(st.Pos(), "cannot range over %s", s.typeOf(st.X))
	return nil
}

type switchClause struct {
	conds []expr
	body  stmtFn
}

func (s *scope) switchStmt(st *ast.SwitchStmt) stmtFn {
	label := s.takeLabel()
	init := s.optStmt(st.Init)
	var tag expr
	var eq func(a, b Value) bool
	var tagT types.Type
	if st.Tag != nil {
		tagT = defaultType(s.typeOf(st.Tag))
		tag = s.expr(st.Tag)
		eq = equalFn(tagT)
	}
	clauses := make([]switchClause, len(st.Body.List))
	def := -1
	for i, c := range st.Body.List {
		cc := c.(*ast.CaseClause)
		if cc.List == nil {
			def = i
		}
		for _, e := range cc.List {
			if tag != nil {
				clauses[i].conds = append(clauses[i].conds, s.convertTo(tagT, e))
			} else {
				clauses[i].conds = append(clauses[i].conds, s.expr(e))
			}
		}
		clauses[i].body = s.block(cc.Body)
	}
	return func(fr *frame) flow {
		if init != nil {
			init(fr)
		}
		var tv Value
		if tag != nil {
			tv = tag(fr)
		}
		chosen := def
	search:
		for i, cl := range clauses {
			for _, c := range cl.conds {
				v := c(fr)
				if (tag != nil && eq(tv, v)) || (tag == nil && v.(bool)) {
					chosen = i
					break search
				}
			}
		}
		if chosen < 0 {
			return flowNext
		}
		fl := clauses[chosen].body(fr)
		for fl == flowFallthrough && chosen+1 < len(clauses) {
			chosen++
			fl = clauses[chosen].body(fr)
		}
		return switchFlow(fr.m, fl, label)
	}
}

type typeCase struct {
	nilCase bool
	t       types.Type
	iface   *types.Interface
}

func (tc typeCase) matches(v Value) bool {
	if tc.nilCase {
		return v == nil
	}
	if v == nil {
		return false
	}
	dyn := v.(iface).t
	if tc.iface != nil {
		return types.Implements(dyn, tc.iface)
	}
	return types.Identical(dyn, tc.t)
}

func (s *scope) typeCaseOf(e ast.Expr) typeCase {
	if s.c.info.Types[e].IsNil() {
		return typeCase{nilCase: true}
	}
	t := s.typeOf(e)
	tc := typeCase{t: t}
	if it, ok := t.Underlying().(*types.Interface); ok {
		tc.iface = it
	}
	return tc
}

func (s *scope) typeSwitchStmt(st *ast.TypeSwitchStmt) stmtFn {
	label := s.takeLabel()
	init := s.optStmt(st.Init)
	var subject ast.Expr
	bound := false
	switch a := st.Assign.(type) {
	case *ast.AssignStmt:
		subject, bound = a.Rhs[0].(*ast.TypeAssertExpr).X, true
	case *ast.ExprStmt:
		subject = a.X.(*ast.TypeAssertExpr).X
	}
	x := s.expr(subject)
	type clause struct {
		cases  []typeCase
		bind   *lvalue
		unwrap bool
		clone  bool
		body   stmtFn
	}
	clauses := make([]clause, len(st.Body.List))
	def := -1
	for i, c := range st.Body.List {
		cc := c.(*ast.CaseClause)
		if cc.List == nil {
			def = i
		}
		for _, e := range cc.List {
			clauses[i].cases = append(clauses[i].cases, s.typeCaseOf(e))
		}
		if obj := s.c.info.Implicits[cc]; bound && obj != nil {
			lv := s.declLvalue(obj)
			clauses[i].bind = &lv
			if len(cc.List) == 1 && !clauses[i].cases[0].nilCase && clauses[i].cases[0].iface == nil {
				clauses[i].unwrap = true
				clauses[i].clone = isAggregate(obj.Type())
			}
		}
		clauses[i].body = s.block(cc.Body)
	}
	return func(fr *frame) flow {
		if init != nil {
			init(fr)
		}
		v := x(fr)
		chosen := def
	search:
		for i, cl := range clauses {
			for _, tc := range cl.cases {
				if tc.matches(v) {
					chosen = i
					break search
				}
			}
		}
		if chosen < 0 {
			return flowNext
		}
		cl := clauses[chosen]
		if cl.bind != nil {
			bv := v
			if cl.unwrap {
				bv = v.(iface).v
				if cl.clone {
					bv = bv.(*structVal).clone(fr.m)
				}
			}
			cl.bind.set(fr, bv)
		}
		return switchFlow(fr.m, cl.body(fr), label)
	}
}

func containsDeferIn(s *scope) bool { return s.code.hasDefer }

func (s *scope) prepareCall(call *ast.CallExpr) (func(*frame) []Value, expr) {
	var captured []ast.Expr
	var evals []expr
	capture := func(e ast.Expr, eval expr) {
		captured = append(captured, e)
		evals = append(evals, eval)
	}
	ftv := s.c.info.Types[call.Fun]
	if !ftv.IsType() && !ftv.IsBuiltin() {
		switch f := ast.Unparen(call.Fun).(type) {
		case *ast.SelectorExpr:
			if sel, ok := s.c.info.Selections[f]; ok {
				switch {
				case sel.Kind() == types.FieldVal:
					capture(call.Fun, s.value(call.Fun))
				case methodTakesAddress(sel):
					capture(f.X, s.addressOf(f.X))
				default:
					capture(f.X, s.value(f.X))
				}
			}
		case *ast.Ident:
			if _, isVar := s.c.info.Uses[f].(*types.Var); isVar {
				capture(call.Fun, s.value(call.Fun))
			}
		default:
			capture(call.Fun, s.value(call.Fun))
		}
	}
	for _, a := range call.Args {
		capture(a, s.value(a))
	}
	if s.overrides == nil {
		s.overrides = map[ast.Expr]expr{}
	}
	for i, e := range captured {
		s.overrides[e] = func(fr *frame) Value { return fr.deferArgs[i] }
	}
	run := s.expr(call)
	for _, e := range captured {
		delete(s.overrides, e)
	}
	return func(fr *frame) []Value {
		args := make([]Value, len(evals))
		for i, ev := range evals {
			args[i] = ev(fr)
		}
		return args
	}, run
}

func (s *scope) deferStmt(st *ast.DeferStmt) stmtFn {
	args, run := s.prepareCall(st.Call)
	return func(fr *frame) flow {
		fr.defers = append(fr.defers, deferred{call: run, args: args(fr)})
		return flowNext
	}
}

func (s *scope) goStmt(st *ast.GoStmt) stmtFn {
	args, run := s.prepareCall(st.Call)
	return func(fr *frame) flow {
		captured := args(fr)
		fr.m.spawn(func(gfr *frame) {
			gfr.deferArgs = captured
			run(gfr)
		})
		return flowNext
	}
}

func (s *scope) selectStmt(st *ast.SelectStmt) stmtFn {
	label := s.takeLabel()
	type clause struct {
		ch     expr
		send   bool
		val    expr
		zero   func() Value
		assign []*lvalue
		body   stmtFn
	}
	var clauses []clause
	var def stmtFn
	for _, c := range st.Body.List {
		cc := c.(*ast.CommClause)
		var cl clause
		var recv *ast.UnaryExpr
		switch comm := cc.Comm.(type) {
		case nil:
			def = s.block(cc.Body)
			continue
		case *ast.SendStmt:
			cl.ch, cl.send = s.expr(comm.Chan), true
			cl.val = s.convertTo(s.typeOf(comm.Chan).Underlying().(*types.Chan).Elem(), comm.Value)
		case *ast.ExprStmt:
			recv = ast.Unparen(comm.X).(*ast.UnaryExpr)
		case *ast.AssignStmt:
			recv = ast.Unparen(comm.Rhs[0]).(*ast.UnaryExpr)
			for _, l := range comm.Lhs {
				cl.assign = append(cl.assign, s.rangeVar(l, comm.Tok == token.DEFINE))
			}
		}
		if recv != nil {
			cl.ch = s.expr(recv.X)
			cl.zero = s.c.prog.zero(s.typeOf(recv.X).Underlying().(*types.Chan).Elem())
		}
		cl.body = s.block(cc.Body)
		clauses = append(clauses, cl)
	}
	return func(fr *frame) flow {
		cases := make([]selectCase, len(clauses))
		for i, cl := range clauses {
			cases[i] = selectCase{ch: cl.ch(fr).(*chanVal), send: cl.send, zero: cl.zero}
			if cl.send {
				cases[i].val = cl.val(fr)
			}
		}
		i, v, ok := fr.m.selectOp(cases, def != nil)
		if i < 0 {
			return switchFlow(fr.m, def(fr), label)
		}
		cl := clauses[i]
		if len(cl.assign) > 0 {
			cl.assign[0].set(fr, v)
		}
		if len(cl.assign) > 1 {
			cl.assign[1].set(fr, ok)
		}
		return switchFlow(fr.m, cl.body(fr), label)
	}
}

func methodTakesAddress(sel *types.Selection) bool {
	recv := sel.Obj().(*types.Func).Signature().Recv()
	if recv == nil {
		return false
	}
	_, ptrRecv := recv.Type().(*types.Pointer)
	_, xPtr := sel.Recv().Underlying().(*types.Pointer)
	return ptrRecv && !xPtr
}
