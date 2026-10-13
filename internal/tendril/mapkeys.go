package tendril

import (
	"fmt"
	"go/types"
	"math"
	"strconv"
	"strings"
)

type mapKey string

func newMap(keyType types.Type, hint int) *mapVal {
	return &mapVal{m: make(map[Value]Value, min(hint, 64)), canon: keyCanon(keyType)}
}

func keyCanon(t types.Type) func(Value) Value {
	switch t.Underlying().(type) {
	case *types.Basic, *types.Pointer, *types.Chan:
		return nil
	}
	return func(v Value) Value {
		var b strings.Builder
		encodeKey(&b, t, v)
		return mapKey(b.String())
	}
}

func encodeKey(b *strings.Builder, t types.Type, v Value) {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		encodeBasic(b, v)
	case *types.Struct:
		sv := v.(*structVal)
		b.WriteByte('{')
		for i, f := range sv.f {
			encodeKey(b, u.Field(i).Type(), f)
		}
		b.WriteByte('}')
	case *types.Array:
		sv := v.(*structVal)
		b.WriteByte('[')
		for _, e := range sv.f {
			encodeKey(b, u.Elem(), e)
		}
		b.WriteByte(']')
	case *types.Pointer, *types.Chan:
		fmt.Fprintf(b, "p%p;", v)
	case *types.Interface:
		if v == nil {
			b.WriteString("n;")
			return
		}
		iv := v.(iface)
		name := types.TypeString(iv.t, nil)
		if !types.Comparable(iv.t) {
			throw("hash of unhashable type " + name)
		}
		fmt.Fprintf(b, "I%d:%s", len(name), name)
		encodeKey(b, iv.t, iv.v)
	default:
		throw("hash of unhashable type " + t.String())
	}
}

func encodeBasic(b *strings.Builder, v Value) {
	switch v := v.(type) {
	case string:
		fmt.Fprintf(b, "s%d:%s", len(v), v)
	case bool:
		if v {
			b.WriteString("t;")
		} else {
			b.WriteString("f;")
		}
	case float32:
		encodeFloat(b, float64(v))
	case float64:
		encodeFloat(b, v)
	case uint, uint8, uint16, uint32, uint64, uintptr:
		b.WriteString("u" + strconv.FormatUint(toUint64(v), 10) + ";")
	default:
		b.WriteString("i" + strconv.FormatInt(toInt64(v), 10) + ";")
	}
}

func encodeFloat(b *strings.Builder, f float64) {
	if f == 0 {
		f = 0
	}
	b.WriteString("d" + strconv.FormatUint(math.Float64bits(f), 16) + ";")
}

func (mv *mapVal) key(k Value) Value {
	if mv.canon == nil {
		return k
	}
	return mv.canon(k)
}

func (mv *mapVal) get(k Value) (Value, bool) {
	if mv == nil {
		return nil, false
	}
	v, ok := mv.m[mv.key(k)]
	return v, ok
}

func (mv *mapVal) set(m *machine, k, v Value) {
	if mv == nil {
		throw("assignment to entry in nil map")
	}
	ck := mv.key(k)
	if _, exists := mv.m[ck]; !exists {
		size := int64(entryBytes)
		switch key := ck.(type) {
		case string:
			size += int64(len(key))
		case mapKey:
			size += int64(len(key))
		}
		m.alloc(size)
		if mv.canon != nil {
			if mv.orig == nil {
				mv.orig = map[Value]Value{}
			}
			mv.orig[ck] = k
		}
	}
	mv.m[ck] = v
}

func (mv *mapVal) del(k Value) {
	if mv == nil {
		return
	}
	ck := mv.key(k)
	delete(mv.m, ck)
	delete(mv.orig, ck)
}

func (mv *mapVal) clear() {
	if mv == nil {
		return
	}
	clear(mv.m)
	clear(mv.orig)
}

func (mv *mapVal) entries() [][2]Value {
	if mv == nil {
		return nil
	}
	keys := make([]Value, 0, len(mv.m))
	for k := range mv.m {
		keys = append(keys, k)
	}
	sortKeys(keys)
	out := make([][2]Value, len(keys))
	for i, k := range keys {
		orig := k
		if mv.canon != nil {
			orig = mv.orig[k]
		}
		out[i] = [2]Value{orig, mv.m[k]}
	}
	return out
}
