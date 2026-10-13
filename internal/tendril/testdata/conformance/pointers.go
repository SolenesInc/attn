package main

import (
	"fmt"
	"strings"
)

type Counter int

func (c *Counter) Inc() { *c++ }

func (c Counter) Double() Counter { return c * 2 }

type Config struct {
	Name    string
	Retries int
	Tags    []string
	Inner   struct{ Level int }
}

func bump(n *int) { *n += 10 }

func rename(c *Config, name string) { c.Name = name }

func swap(a, b *string) { *a, *b = *b, *a }

func newInt(v int) *int { return &v }

type Grid [3][3]int

func (g *Grid) Set(r, c, v int) { g[r][c] = v }

func (g Grid) Sum() int {
	total := 0
	for _, row := range g {
		for _, v := range row {
			total += v
		}
	}
	return total
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	x := 1
	p := &x
	*p = 5
	bump(&x)
	add("int pointer: %d %d %v", x, *p, p == &x)

	var c Counter
	c.Inc()
	c.Inc()
	cp := &c
	cp.Inc()
	add("pointer receiver on named int: %d %d", c, c.Double())

	cfg := Config{Name: "a", Retries: 3}
	alias := &cfg
	rename(&cfg, "b")
	cfg = Config{Name: "c", Retries: 9}
	add("assignment keeps address: %s %d %v", alias.Name, alias.Retries, alias == &cfg)
	inner := &cfg.Inner
	inner.Level = 7
	retries := &cfg.Retries
	*retries++
	add("field pointers: %d %d", cfg.Inner.Level, cfg.Retries)

	s1, s2 := "left", "right"
	swap(&s1, &s2)
	add("swap: %s %s", s1, s2)

	nums := []int{1, 2, 3}
	ep := &nums[1]
	*ep = 20
	add("element pointer: %v", nums)

	q1, q2 := newInt(3), newInt(3)
	add("escaped locals: %d %d %v", *q1, *q2, q1 == q2)

	var np *int
	add("nil pointer: %v", np == nil)
	pp := &np
	*pp = newInt(42)
	add("pointer to pointer: %d", *np)

	arr := [5]int{1, 2, 3}
	brr := arr
	brr[0] = 100
	add("array copy: %v %v %d", arr, brr, len(arr))
	sl := arr[1:4]
	sl[0] = -2
	add("slice of array aliases: %v %v", arr, sl)
	keyed := [...]string{2: "c", 0: "a"}
	add("keyed array: %q %d", keyed, len(keyed))
	sparse := []int{3: 1, 1: 2}
	add("keyed slice: %v", sparse)
	add("array equality: %v %v", [2]int{1, 2} == [2]int{1, 2}, [2]int{1, 2} == [2]int{2, 1})

	var g Grid
	g.Set(1, 1, 5)
	g.Set(2, 0, 4)
	h := g
	h.Set(0, 0, 100)
	add("grid: %v %d %d", g, g.Sum(), h.Sum())
	ap := &arr
	ap[4] = 9
	add("pointer to array: %v %d", arr, len(ap))
	for i, v := range ap {
		if v == 9 {
			add("found at %d", i)
		}
	}

	type pair struct{ a, b int }
	pairs := [2]pair{{1, 2}, {3, 4}}
	first := &pairs[0]
	first.b = 50
	add("array of structs: %v", pairs)

	conv := [3]int(nums)
	conv[0] = 0
	view := (*[2]int)(nums)
	view[0] = 77
	add("slice to array: %v %v %v", conv, nums, *view)

	counts := new(int)
	*counts += 4
	cfgp := new(Config)
	cfgp.Tags = append(cfgp.Tags, "x")
	add("new: %d %v", *counts, cfgp.Tags)

	funcs := []func(){}
	total := 0
	for i := range 3 {
		ip := &i
		funcs = append(funcs, func() { total += *ip })
	}
	for _, f := range funcs {
		f()
	}
	add("per-iteration address: %d", total)
	return strings.Join(out, "\n")
}
