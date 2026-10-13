package tendril

import (
	"go/types"
	"slices"
)

type nativeFn struct {
	fn func(m *machine, args []Value) Value
}

func seqOf(each func(m *machine, yield func(...Value) bool)) *nativeFn {
	return &nativeFn{fn: func(m *machine, args []Value) Value {
		yield := args[0]
		each(m, func(vals ...Value) bool { return m.callValue(yield, vals).(bool) })
		return nil
	}}
}

func collect(m *machine, seq Value, elem types.Type) []Value {
	var out []Value
	zero := m.prog.zero(elem)
	m.callValue(seq, []Value{&nativeFn{fn: func(m *machine, args []Value) Value {
		out = appendValues(m, out, zero, args[0])
		return true
	}}})
	return out
}

const iterSource = `package iter

// Seq is an iterator over sequences of individual values.
type Seq[V any] func(yield func(V) bool)

// Seq2 is an iterator over sequences of pairs of values.
type Seq2[K, V any] func(yield func(K, V) bool)
`

const slicesSource = `package slices

import (
	"cmp"
	"iter"
)

func Sort[S ~[]E, E cmp.Ordered](x S)
func SortFunc[S ~[]E, E any](x S, cmp func(a, b E) int)
func SortStableFunc[S ~[]E, E any](x S, cmp func(a, b E) int)
func Contains[S ~[]E, E comparable](s S, v E) bool
func ContainsFunc[S ~[]E, E any](s S, f func(E) bool) bool
func Index[S ~[]E, E comparable](s S, v E) int
func IndexFunc[S ~[]E, E any](s S, f func(E) bool) int
func Reverse[S ~[]E, E any](s S)
func Max[S ~[]E, E cmp.Ordered](x S) E
func Min[S ~[]E, E cmp.Ordered](x S) E
func Equal[S ~[]E, E comparable](s1, s2 S) bool
func Clone[S ~[]E, E any](s S) S
func Compact[S ~[]E, E comparable](s S) S
func All[Slice ~[]E, E any](s Slice) iter.Seq2[int, E]
func Values[Slice ~[]E, E any](s Slice) iter.Seq[E]
func Backward[Slice ~[]E, E any](s Slice) iter.Seq2[int, E]
func Collect[E any](seq iter.Seq[E]) []E
func Sorted[E cmp.Ordered](seq iter.Seq[E]) []E
`

const mapsSource = `package maps

import "iter"

// Keys, Values and All visit entries in sorted key order in scripts.
func Keys[Map ~map[K]V, K comparable, V any](m Map) iter.Seq[K]
func Values[Map ~map[K]V, K comparable, V any](m Map) iter.Seq[V]
func All[Map ~map[K]V, K comparable, V any](m Map) iter.Seq2[K, V]
func Clone[M ~map[K]V, K comparable, V any](m M) M
func Copy[M1 ~map[K]V, M2 ~map[K]V, K comparable, V any](dst M1, src M2)
func DeleteFunc[M ~map[K]V, K comparable, V any](m M, del func(K, V) bool)
func Collect[K comparable, V any](seq iter.Seq2[K, V]) map[K]V
`

func stdIter() *Package { return NewPackage("iter").Source(iterSource) }

func elemAt(t []types.Type, i int) types.Type { return t[i] }

func stdSlices() *Package {
	sortBy := func(stable bool) nativeImpl {
		return func(m *machine, _ []types.Type, a []Value) Value {
			s := a[0].([]Value)
			m.burn(int64(len(s)) * int64(1+bitsLen(len(s))) / 4)
			cmpFn := func(x, y Value) int { return m.callValue(a[1], []Value{x, y}).(int) }
			if stable {
				slices.SortStableFunc(s, cmpFn)
			} else {
				slices.SortFunc(s, cmpFn)
			}
			return nil
		}
	}
	index := func(m *machine, t []types.Type, a []Value) int {
		eq := equalFn(elemAt(t, 1))
		s := a[0].([]Value)
		m.burn(int64(len(s) / 8))
		for i, v := range s {
			if eq(v, a[1]) {
				return i
			}
		}
		return -1
	}
	indexFunc := func(m *machine, a []Value) int {
		for i, v := range a[0].([]Value) {
			if m.callValue(a[1], []Value{v}).(bool) {
				return i
			}
		}
		return -1
	}
	extreme := func(name string, better int) nativeImpl {
		return func(m *machine, _ []types.Type, a []Value) Value {
			s := a[0].([]Value)
			if len(s) == 0 {
				throw("slices." + name + ": empty list")
			}
			best := s[0]
			for _, v := range s[1:] {
				if compareKeys(v, best) == better {
					best = v
				}
			}
			return best
		}
	}
	return NewPackage("slices").Source(slicesSource).
		Native("Sort", sortValues).
		Native("SortFunc", sortBy(false)).
		Native("SortStableFunc", sortBy(true)).
		Native("Contains", func(m *machine, t []types.Type, a []Value) Value { return index(m, t, a) >= 0 }).
		Native("ContainsFunc", func(m *machine, _ []types.Type, a []Value) Value { return indexFunc(m, a) >= 0 }).
		Native("Index", func(m *machine, t []types.Type, a []Value) Value { return index(m, t, a) }).
		Native("IndexFunc", func(m *machine, _ []types.Type, a []Value) Value { return indexFunc(m, a) }).
		Native("Reverse", func(m *machine, _ []types.Type, a []Value) Value {
			slices.Reverse(a[0].([]Value))
			return nil
		}).
		Native("Max", extreme("Max", 1)).
		Native("Min", extreme("Min", -1)).
		Native("Equal", func(m *machine, t []types.Type, a []Value) Value {
			x, y := a[0].([]Value), a[1].([]Value)
			eq := equalFn(elemAt(t, 1))
			if len(x) != len(y) {
				return false
			}
			for i := range x {
				if !eq(x[i], y[i]) {
					return false
				}
			}
			return true
		}).
		Native("Clone", func(m *machine, t []types.Type, a []Value) Value {
			s := a[0].([]Value)
			if s == nil {
				return s
			}
			m.alloc(headerBytes + slotBytes*int64(len(s)))
			out := slices.Clone(s)
			if isAggregate(elemAt(t, 1)) {
				for i, v := range out {
					out[i] = v.(*structVal).clone(m)
				}
			}
			return out
		}).
		Native("Compact", func(m *machine, t []types.Type, a []Value) Value {
			eq := equalFn(elemAt(t, 1))
			return slices.CompactFunc(a[0].([]Value), eq)
		}).
		Native("All", func(m *machine, _ []types.Type, a []Value) Value {
			s := a[0].([]Value)
			return seqOf(func(m *machine, yield func(...Value) bool) {
				for i, v := range s {
					if !yield(i, v) {
						return
					}
				}
			})
		}).
		Native("Values", func(m *machine, _ []types.Type, a []Value) Value {
			s := a[0].([]Value)
			return seqOf(func(m *machine, yield func(...Value) bool) {
				for _, v := range s {
					if !yield(v) {
						return
					}
				}
			})
		}).
		Native("Backward", func(m *machine, _ []types.Type, a []Value) Value {
			s := a[0].([]Value)
			return seqOf(func(m *machine, yield func(...Value) bool) {
				for i := len(s) - 1; i >= 0; i-- {
					if !yield(i, s[i]) {
						return
					}
				}
			})
		}).
		Native("Collect", func(m *machine, t []types.Type, a []Value) Value { return collect(m, a[0], t[0]) }).
		Native("Sorted", func(m *machine, t []types.Type, a []Value) Value {
			out := collect(m, a[0], t[0])
			slices.SortStableFunc(out, compareKeys)
			return out
		})
}

func stdMaps() *Package {
	each := func(a []Value, visit func(yield func(...Value) bool, k, v Value) bool) *nativeFn {
		mv := a[0].(*mapVal)
		return seqOf(func(m *machine, yield func(...Value) bool) {
			for _, kv := range mv.entries() {
				if _, ok := mv.get(kv[0]); ok && !visit(yield, kv[0], kv[1]) {
					return
				}
			}
		})
	}
	copyInto := func(m *machine, dst, src *mapVal) {
		for _, kv := range src.entries() {
			dst.set(m, kv[0], kv[1])
		}
	}
	return NewPackage("maps").Source(mapsSource).
		Native("Keys", func(m *machine, _ []types.Type, a []Value) Value {
			return each(a, func(yield func(...Value) bool, k, _ Value) bool { return yield(k) })
		}).
		Native("Values", func(m *machine, _ []types.Type, a []Value) Value {
			return each(a, func(yield func(...Value) bool, _, v Value) bool { return yield(v) })
		}).
		Native("All", func(m *machine, _ []types.Type, a []Value) Value {
			return each(a, func(yield func(...Value) bool, k, v Value) bool { return yield(k, v) })
		}).
		Native("Clone", func(m *machine, t []types.Type, a []Value) Value {
			src := a[0].(*mapVal)
			if src == nil {
				return src
			}
			m.alloc(headerBytes)
			dst := newMap(elemAt(t, 1), src.len())
			copyInto(m, dst, src)
			return dst
		}).
		Native("Copy", func(m *machine, _ []types.Type, a []Value) Value {
			copyInto(m, a[0].(*mapVal), a[1].(*mapVal))
			return nil
		}).
		Native("DeleteFunc", func(m *machine, _ []types.Type, a []Value) Value {
			mv := a[0].(*mapVal)
			for _, kv := range mv.entries() {
				if m.callValue(a[1], []Value{kv[0], kv[1]}).(bool) {
					mv.del(kv[0])
				}
			}
			return nil
		}).
		Native("Collect", func(m *machine, t []types.Type, a []Value) Value {
			m.alloc(headerBytes)
			out := newMap(elemAt(t, 0), 0)
			m.callValue(a[0], []Value{&nativeFn{fn: func(m *machine, args []Value) Value {
				out.set(m, args[0], args[1])
				return true
			}}})
			return out
		})
}
