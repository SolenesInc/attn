package tendril

import (
	"go/ast"
	"go/token"
	"go/types"
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
		switch st.Tok {
		case token.BREAK:
			return func(*frame) flow { return flowBreak }
		case token.CONTINUE:
			return func(*frame) flow { return flowContinue }
		}
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
	switch e := ast.Unparen(e).(type) {
	case *ast.Ident:
		if e.Name == "_" {
			return lvalue{store: func(*frame, Value, Value, Value) {}}
		}
		v := s.object(e).(*types.Var)
		if define && s.c.info.Defs[e] != nil {
			return s.declLvalue(v)
		}
		return s.varLvalue(v, e.Pos())
	case *ast.IndexExpr:
		x := s.expr(e.X)
		switch xt := s.typeOf(e.X).Underlying().(type) {
		case *types.Slice:
			idx := s.intExpr(e.Index)
			return lvalue{
				prep:  func(fr *frame) (Value, Value) { return x(fr), idx(fr) },
				load:  func(_ *frame, obj, key Value) Value { return obj.([]Value)[key.(int)] },
				store: func(_ *frame, obj, key, v Value) { obj.([]Value)[key.(int)] = v },
			}
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
			}
		}
	case *ast.SelectorExpr:
		base, i := s.fieldBase(e)
		return lvalue{
			prep:  func(fr *frame) (Value, Value) { return base(fr), nil },
			load:  func(_ *frame, obj, _ Value) Value { return obj.(*structVal).f[i] },
			store: func(_ *frame, obj, _, v Value) { obj.(*structVal).f[i] = v },
		}
	case *ast.StarExpr:
		p := s.expr(e.X)
		return lvalue{
			prep: func(fr *frame) (Value, Value) { return p(fr), nil },
			load: func(_ *frame, obj, _ Value) Value { return obj },
			store: func(_ *frame, obj, _, v Value) {
				dst := obj.(*structVal)
				copy(dst.f, v.(*structVal).f)
			},
		}
	}
	s.c.fail(e.Pos(), "cannot assign to %T", e)
	return lvalue{}
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
			lv := s.declLvalue(v)
			parts = append(parts, func(fr *frame) flow {
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
			switch body(fr) {
			case flowBreak:
				return flowNext
			case flowReturn:
				return flowReturn
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
	define := st.Tok == token.DEFINE
	x := s.expr(st.X)
	key := s.rangeVar(st.Key, define)
	val := s.rangeVar(st.Value, define)
	body := s.block(st.Body.List)
	iterate := func(fr *frame) (stop bool, fl flow) {
		switch fl := body(fr); fl {
		case flowBreak:
			return true, flowNext
		case flowReturn:
			return true, flowReturn
		}
		fr.m.burn(1)
		return false, flowNext
	}
	switch xt := s.typeOf(st.X).Underlying().(type) {
	case *types.Slice:
		_, structElem := xt.Elem().Underlying().(*types.Struct)
		return func(fr *frame) flow {
			sl := x(fr).([]Value)
			for i := range len(sl) {
				key.set(fr, i)
				if val != nil {
					v := sl[i]
					if structElem {
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
		if fl := clauses[chosen].body(fr); fl != flowBreak {
			return fl
		}
		return flowNext
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
				_, clauses[i].clone = obj.Type().Underlying().(*types.Struct)
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
		if fl := cl.body(fr); fl != flowBreak {
			return fl
		}
		return flowNext
	}
}
