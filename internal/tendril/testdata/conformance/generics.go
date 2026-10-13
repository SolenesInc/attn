package main

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Number interface {
	~int | ~int64 | ~float64
}

func Sum[T Number](xs []T) T {
	var total T
	for _, x := range xs {
		total += x
	}
	return total
}

func Map[T, U any](xs []T, f func(T) U) []U {
	out := make([]U, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

func Filter[T any](xs []T, keep func(T) bool) []T {
	var out []T
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

func MaxOf[T cmp.Ordered](xs ...T) (T, bool) {
	var best T
	if len(xs) == 0 {
		return best, false
	}
	best = xs[0]
	for _, x := range xs[1:] {
		if cmp.Less(best, x) {
			best = x
		}
	}
	return best, true
}

type Stack[T any] struct {
	items []T
}

func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }

func (s *Stack[T]) Pop() (T, error) {
	var zero T
	if len(s.items) == 0 {
		return zero, errors.New("empty stack")
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, nil
}

func (s Stack[T]) Len() int { return len(s.items) }

type Pair[K comparable, V any] struct {
	Key K
	Val V
}

func (p Pair[K, V]) String() string { return fmt.Sprintf("%v=%v", p.Key, p.Val) }

func Entries[K cmp.Ordered, V any](m map[K]V) []Pair[K, V] {
	var out []Pair[K, V]
	for k, v := range m {
		out = append(out, Pair[K, V]{k, v})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Key < out[j-1].Key; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

type Lener interface{ Len() int }

type Celsius float64

type List[T any] struct {
	head *node[T]
	size int
}

type node[T any] struct {
	val  T
	next *node[T]
}

func (l *List[T]) Prepend(v T) {
	l.head = &node[T]{v, l.head}
	l.size++
}

func (l *List[T]) Each(f func(T)) {
	for n := l.head; n != nil; n = n.next {
		f(n.val)
	}
}

func Reduce[T, A any](xs []T, init A, f func(A, T) A) A {
	acc := init
	for _, x := range xs {
		acc = f(acc, x)
	}
	return acc
}

func Keys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	add("sum ints: %d", Sum([]int{1, 2, 3}))
	add("sum floats: %.2f", Sum([]float64{1.5, 2.25}))
	add("sum named: %.1f", Sum([]Celsius{20, 1.5}))
	add("map: %v", Map([]int{1, 2, 3}, strconv.Itoa))
	add("map closure: %v", Map([]string{"a", "bb"}, func(s string) int { return len(s) }))
	add("filter: %v", Filter([]int{1, 2, 3, 4, 5, 6}, func(n int) bool { return n%2 == 0 }))
	m, ok := MaxOf(3, 9, 2)
	ms, _ := MaxOf("pear", "apple", "zebra")
	_, none := MaxOf[int]()
	add("max: %d %v %s %v", m, ok, ms, none)

	var s Stack[string]
	s.Push("a")
	s.Push("b")
	top, _ := s.Pop()
	_, err := (&Stack[int]{}).Pop()
	add("stack: %s %d %v", top, s.Len(), err)

	var l Lener = s
	var li Lener = Stack[float64]{items: []float64{1, 2}}
	add("generic types through interfaces: %d %d", l.Len(), li.Len())

	add("entries: %v", Entries(map[string]int{"b": 2, "a": 1, "c": 3}))
	p := Pair[string, []int]{"xs", []int{1, 2}}
	add("pair: %s", p)

	var list List[string]
	for _, w := range []string{"c", "b", "a"} {
		list.Prepend(w)
	}
	var walked []string
	list.Each(func(v string) { walked = append(walked, strings.ToUpper(v)) })
	add("list: %v %d", walked, list.size)

	total := Reduce([]string{"a", "bb", "ccc"}, 0, func(acc int, s string) int { return acc + len(s) })
	joined := Reduce([]int{1, 2, 3}, "", func(acc string, n int) string { return acc + strconv.Itoa(n) })
	add("reduce: %d %q", total, joined)
	add("explicit instantiation: %v", Map[int, string]([]int{7}, func(i int) string { return fmt.Sprint(i * 2) }))
	sq := Map[int, int]
	add("instantiated value: %v", sq([]int{1, 2}, func(i int) int { return i * i }))
	add("keys: %d", len(Keys(map[int]bool{1: true, 2: false})))
	add("cmp: %d %d %d", cmp.Compare(1, 2), cmp.Compare("b", "a"), cmp.Compare(2.0, 2.0))
	return strings.Join(out, "\n")
}
