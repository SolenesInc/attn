package main

import (
	"errors"
	"fmt"
	"strings"
)

var log []string

func trace(name string) func() {
	log = append(log, "enter "+name)
	return func() { log = append(log, "exit "+name) }
}

func ordered() {
	defer trace("ordered")()
	for i := range 3 {
		defer func() { log = append(log, fmt.Sprint("loop ", i)) }()
	}
	x := 1
	defer fmt.Sprint(x)
	defer func(v int) { log = append(log, fmt.Sprint("arg evaluated at defer: ", v)) }(x)
	x = 2
}

func safeDiv(a, b int) (q int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered: %v", r)
		}
	}()
	q = a / b
	return q, nil
}

func double(n int) (result int) {
	defer func() { result *= 2 }()
	return n + 1
}

type stack struct{ items []string }

func (s *stack) push(v string) { s.items = append(s.items, v) }

func lookup(m map[string]int, k string) (v int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("lookup panicked")
		}
	}()
	if k == "" {
		panic("empty key")
	}
	return m[k], nil
}

func nested() (out string) {
	defer func() {
		r := recover()
		out = fmt.Sprintf("outer recovered %v", r)
	}()
	defer func() {
		panic("second")
	}()
	panic("first")
}

func notDirect() (got any) {
	defer func() {
		helper := func() any { return recover() }
		got = helper()
		recover()
	}()
	panic("x")
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	ordered()
	add("defer order: %s", strings.Join(log, "; "))
	q, err := safeDiv(7, 2)
	add("safeDiv ok: %d %v", q, err)
	q, err = safeDiv(1, 0)
	add("safeDiv zero: %d %v", q, err)
	add("named result modified by defer: %d", double(4))
	v, err := lookup(map[string]int{"a": 1}, "a")
	add("lookup: %d %v", v, err)
	v, err = lookup(nil, "")
	add("lookup panic: %d %v", v, err)
	add("nested: %s", nested())
	add("indirect recover: %v", notDirect())

	var s stack
	func() {
		defer s.push("deferred")
		s.push("body")
	}()
	add("deferred method: %v", s.items)

	found := ""
outer:
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			if j == 3 {
				continue outer
			}
			if i*j == 6 {
				found = fmt.Sprintf("%d*%d", i, j)
				break outer
			}
		}
	}
	add("labels: %s", found)

	count := 0
rows:
	for _, row := range [][]int{{1, 2}, {3, -1, 4}, {5}} {
		for _, v := range row {
			switch {
			case v < 0:
				continue rows
			case v > 4:
				break rows
			}
			count += v
		}
	}
	add("labeled range: %d", count)

	var trail []string
	for _, n := range []int{1, 2, 3} {
		switch n {
		case 1:
			trail = append(trail, "one")
			fallthrough
		case 2:
			trail = append(trail, "one-or-two")
		case 3:
			trail = append(trail, "three")
			fallthrough
		default:
			trail = append(trail, "default")
		}
	}
	add("fallthrough: %v", trail)

	hits := 0
loop:
	for i := range 10 {
		switch {
		case i == 4:
			break loop
		case i%2 == 0:
			hits++
		}
	}
	add("break label from switch: %d", hits)
	return strings.Join(out, "\n")
}
