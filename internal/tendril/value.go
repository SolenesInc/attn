package tendril

import (
	"cmp"
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

type Value = any

type cell struct{ v Value }

type tuple []Value

type structInfo struct {
	typ      types.Type
	names    []string
	agg      []bool
	hasAgg   bool
	zeros    []func() Value
	array    bool
	n        int
	elemZero func() Value
}

func (si *structInfo) aggAt(i int) bool {
	if si.array {
		return si.hasAgg
	}
	return si.agg[i]
}

func (si *structInfo) newZero() *structVal {
	f := make([]Value, si.n)
	for i := range f {
		if si.array {
			f[i] = si.elemZero()
		} else {
			f[i] = si.zeros[i]()
		}
	}
	return &structVal{info: si, f: f}
}

func isAggregate(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Struct, *types.Array:
		return true
	}
	return false
}

func pointsToAggregate(t types.Type) bool {
	p, ok := t.Underlying().(*types.Pointer)
	return ok && isAggregate(p.Elem())
}

type structVal struct {
	info *structInfo
	f    []Value
}

const (
	slotBytes   = 16
	headerBytes = 32
	entryBytes  = 64
)

func (s *structVal) clone(m *machine) *structVal {
	m.alloc(headerBytes + slotBytes*int64(len(s.f)))
	f := make([]Value, len(s.f))
	copy(f, s.f)
	if s.info.hasAgg {
		for i := range f {
			if s.info.aggAt(i) {
				f[i] = f[i].(*structVal).clone(m)
			}
		}
	}
	return &structVal{info: s.info, f: f}
}

func assignAgg(dst, src *structVal) {
	if dst == src {
		return
	}
	if !dst.info.hasAgg {
		copy(dst.f, src.f)
		return
	}
	for i := range dst.f {
		if dst.info.aggAt(i) {
			assignAgg(dst.f[i].(*structVal), src.f[i].(*structVal))
		} else {
			dst.f[i] = src.f[i]
		}
	}
}

type mapVal struct {
	m map[Value]Value
}

func (mv *mapVal) len() int {
	if mv == nil {
		return 0
	}
	return len(mv.m)
}

func (mv *mapVal) sortedKeys() []Value {
	keys := make([]Value, 0, len(mv.m))
	for k := range mv.m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareKeys)
	return keys
}

func compareKeys(a, b Value) int {
	switch a := a.(type) {
	case string:
		return cmp.Compare(a, b.(string))
	case int:
		return cmp.Compare(a, b.(int))
	case int8:
		return cmp.Compare(a, b.(int8))
	case int16:
		return cmp.Compare(a, b.(int16))
	case int32:
		return cmp.Compare(a, b.(int32))
	case int64:
		return cmp.Compare(a, b.(int64))
	case uint:
		return cmp.Compare(a, b.(uint))
	case uint8:
		return cmp.Compare(a, b.(uint8))
	case uint16:
		return cmp.Compare(a, b.(uint16))
	case uint32:
		return cmp.Compare(a, b.(uint32))
	case uint64:
		return cmp.Compare(a, b.(uint64))
	case float32:
		return cmp.Compare(a, b.(float32))
	case float64:
		return cmp.Compare(a, b.(float64))
	case bool:
		bb := b.(bool)
		switch {
		case a == bb:
			return 0
		case !a:
			return -1
		default:
			return 1
		}
	}
	panic(fmt.Sprintf("unsortable map key %T", a))
}

type iface struct {
	t types.Type
	v Value
}

type closure struct {
	fn   *funcCode
	free []*cell
}

const maxFormatDepth = 16

var stringerIface = types.NewInterfaceType([]*types.Func{
	types.NewFunc(token.NoPos, nil, "String", types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.String])), false)),
}, nil).Complete()

var errorIface = errorType.Underlying().(*types.Interface)

type view struct {
	m *machine
	t types.Type
	v Value
}

func (v view) Format(f fmt.State, verb rune) {
	var b strings.Builder
	v.m.reenter(func() { v.m.writeTyped(&b, fmt.FormatString(f, verb), verb, v.t, v.v, 0) })
	_, _ = f.Write([]byte(b.String()))
}

func (m *machine) reenter(f func()) {
	depth, pos := m.depth, m.pos
	defer func() { m.depth, m.pos = depth, pos }()
	f()
}

func (m *machine) writeTyped(b *strings.Builder, directive string, verb rune, t types.Type, v Value, depth int) {
	if depth > maxFormatDepth {
		b.WriteString("...")
		return
	}
	if verb == 'T' {
		b.WriteString(t.String())
		return
	}
	if !isInterface(t) && strings.ContainsRune("vsqxX", verb) {
		if _, isPtr := t.Underlying().(*types.Pointer); isPtr && isNilPointer(v) {
			b.WriteString("<nil>")
			return
		}
		for _, method := range []struct {
			iface *types.Interface
			name  string
		}{{errorIface, "Error"}, {stringerIface, "String"}} {
			if types.Implements(t, method.iface) {
				fmt.Fprintf(b, directive, m.callMethod(iface{t: t, v: v}, method.name, nil))
				return
			}
		}
	}
	plus := strings.Contains(directive, "+")
	switch u := t.Underlying().(type) {
	case *types.Interface:
		if v == nil {
			b.WriteString("<nil>")
			return
		}
		iv := v.(iface)
		if gerr, ok := iv.v.(error); ok && iv.t == m.prog.env.hostErr {
			fmt.Fprintf(b, directive, gerr)
			return
		}
		m.writeTyped(b, directive, verb, iv.t, iv.v, depth)
	case *types.Pointer:
		if isNilPointer(v) {
			b.WriteString("<nil>")
			return
		}
		b.WriteByte('&')
		if p, ok := v.(*Value); ok {
			m.writeTyped(b, directive, verb, u.Elem(), *p, depth+1)
			return
		}
		m.writeTyped(b, directive, verb, u.Elem(), v, depth+1)
	case *types.Array:
		sv := v.(*structVal)
		b.WriteByte('[')
		for i, e := range sv.f {
			if i > 0 {
				b.WriteByte(' ')
			}
			m.writeTyped(b, directive, verb, u.Elem(), e, depth+1)
		}
		b.WriteByte(']')
	case *types.Struct:
		sv := v.(*structVal)
		b.WriteByte('{')
		for i, f := range sv.f {
			if i > 0 {
				b.WriteByte(' ')
			}
			if plus {
				b.WriteString(u.Field(i).Name())
				b.WriteByte(':')
			}
			m.writeTyped(b, directive, verb, u.Field(i).Type(), f, depth+1)
		}
		b.WriteByte('}')
	case *types.Slice:
		sl := v.([]Value)
		if eb, ok := u.Elem().Underlying().(*types.Basic); ok && eb.Kind() == types.Uint8 && strings.ContainsRune("sqxX", verb) {
			bs := make([]byte, len(sl))
			for i, e := range sl {
				bs[i] = e.(byte)
			}
			fmt.Fprintf(b, directive, bs)
			return
		}
		b.WriteByte('[')
		for i, e := range sl {
			if i > 0 {
				b.WriteByte(' ')
			}
			m.writeTyped(b, directive, verb, u.Elem(), e, depth+1)
		}
		b.WriteByte(']')
	case *types.Map:
		mv := v.(*mapVal)
		b.WriteString("map[")
		if mv != nil {
			for i, k := range mv.sortedKeys() {
				if i > 0 {
					b.WriteByte(' ')
				}
				m.writeTyped(b, directive, verb, u.Key(), k, depth+1)
				b.WriteByte(':')
				m.writeTyped(b, directive, verb, u.Elem(), mv.m[k], depth+1)
			}
		}
		b.WriteByte(']')
	case *types.Signature:
		if v == nil {
			b.WriteString("<nil>")
			return
		}
		b.WriteString("func")
	case *types.Chan:
		b.WriteString("chan")
	default:
		fmt.Fprintf(b, directive, v)
	}
}

func (m *machine) formatPanic(v Value) (s string) {
	defer func() {
		if recover() != nil {
			s = "(unprintable value)"
		}
	}()
	if v == nil {
		return "nil"
	}
	var b strings.Builder
	m.depth, m.halted = 0, nil
	m.writeTyped(&b, "%v", 'v', anyType, v, 0)
	return b.String()
}

func equalAt(t types.Type, a, b Value) bool {
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return a == b
	case *types.Array:
		as, bs := a.(*structVal), b.(*structVal)
		for i := range as.f {
			if !equalAt(u.Elem(), as.f[i], bs.f[i]) {
				return false
			}
		}
		return true
	case *types.Struct:
		as, bs := a.(*structVal), b.(*structVal)
		for i := range as.f {
			if !equalAt(u.Field(i).Type(), as.f[i], bs.f[i]) {
				return false
			}
		}
		return true
	case *types.Interface:
		if a == nil || b == nil {
			return a == nil && b == nil
		}
		ai, bi := a.(iface), b.(iface)
		if !types.Identical(ai.t, bi.t) {
			return false
		}
		if !types.Comparable(ai.t) {
			panic(fmt.Errorf("comparing uncomparable type %s", ai.t))
		}
		return equalAt(ai.t, ai.v, bi.v)
	default:
		return a == b
	}
}

func isNilPointer(v Value) bool {
	switch p := v.(type) {
	case *structVal:
		return p == nil
	case *Value:
		return p == nil
	}
	return v == nil
}
