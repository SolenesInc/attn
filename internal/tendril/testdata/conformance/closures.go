package main

import (
	"fmt"
	"strings"
)

func counter() (func() int, func()) {
	n := 0
	return func() int { n++; return n }, func() { n = 100 }
}

func apply(fs []func(int) int, v int) int {
	for _, f := range fs {
		v = f(v)
	}
	return v
}

func compose(f, g func(int) int) func(int) int {
	return func(x int) int { return g(f(x)) }
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	next, reset := counter()
	next()
	next()
	add("counter: %d", next())
	reset()
	add("after reset: %d", next())

	var fs []func() int
	for i := 0; i < 3; i++ {
		fs = append(fs, func() int { return i * 10 })
	}
	for _, v := range []int{7, 8} {
		fs = append(fs, func() int { return v })
	}
	var got []string
	for _, f := range fs {
		got = append(got, fmt.Sprint(f()))
	}
	add("per-iteration vars: %s", strings.Join(got, ","))

	adders := []func(int) int{}
	for _, d := range []int{1, 2, 3} {
		adders = append(adders, func(x int) int { return x + d })
	}
	add("apply: %d", apply(adders, 10))
	add("compose: %d", compose(func(x int) int { return x * 2 }, func(x int) int { return x + 1 })(5))

	var fib func(int) int
	fib = func(n int) int {
		if n < 2 {
			return n
		}
		return fib(n-1) + fib(n-2)
	}
	add("closure recursion: %d", fib(15))

	total := 0
	outer := func() func() {
		return func() {
			inner := func() { total += 7 }
			inner()
			inner()
		}
	}
	outer()()
	add("nested capture: %d", total)

	shadow := 1
	{
		shadow := 2
		_ = shadow
		f := func() int { return shadow }
		add("shadow inner: %d", f())
	}
	add("shadow outer: %d", shadow)

	makeAcc := func(start int) (func(int), func() int) {
		sum := start
		return func(d int) { sum += d }, func() int { return sum }
	}
	inc, get := makeAcc(5)
	inc(3)
	inc(4)
	add("acc: %d", get())
	return strings.Join(out, "\n")
}
