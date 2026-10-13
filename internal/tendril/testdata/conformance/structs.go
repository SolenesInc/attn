package main

import (
	"fmt"
	"strings"
)

type Point struct {
	X, Y int
}

func (p Point) Add(q Point) Point { return Point{p.X + q.X, p.Y + q.Y} }

func (p *Point) Scale(k int) {
	p.X *= k
	p.Y *= k
}

func (p Point) String() string { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }

type Rect struct {
	Min, Max Point
	Tags     []string
}

func (r Rect) Area() int { return (r.Max.X - r.Min.X) * (r.Max.Y - r.Min.Y) }

type Node struct {
	Val  int
	Next *Node
}

type Stack struct {
	items []int
}

func (s *Stack) Push(v int) { s.items = append(s.items, v) }

func (s *Stack) Pop() (int, bool) {
	if len(s.items) == 0 {
		return 0, false
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, true
}

func mutate(p Point) Point {
	p.X = 999
	return p
}

func mutatePtr(p *Point) { p.X = 999 }

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	a := Point{1, 2}
	b := a
	b.X = 10
	add("copy semantics: %v %v", a, b)
	add("method value recv: %v", a.Add(b))
	a.Scale(3)
	add("pointer method on var: %v", a)
	m := mutate(a)
	add("pass by value: %v %v", a, m)
	p := &Point{5, 6}
	mutatePtr(p)
	q := p
	q.Y = 0
	add("pointer aliasing: %v %v", *p, *q)
	add("equality: %v %v", Point{1, 2} == Point{1, 2}, Point{1, 2} == Point{2, 1})

	r := Rect{Min: Point{0, 0}, Max: Point{4, 5}, Tags: []string{"a"}}
	r2 := r
	r2.Max.X = 10
	r2.Tags[0] = "shared"
	add("nested copy: %d %d %v", r.Area(), r2.Area(), r.Tags)
	add("%+v", r)

	pts := []Point{{1, 1}, {2, 2}}
	for _, pt := range pts {
		pt.X = 100
	}
	add("range copies: %v", pts)
	for i := range pts {
		pts[i].Scale(2)
	}
	add("index mutation: %v", pts)

	byName := map[string]Point{"o": {0, 0}}
	pt := byName["o"]
	pt.X = 7
	add("map value copy: %v %v", byName["o"], pt)

	var list *Node
	for i := 1; i <= 4; i++ {
		list = &Node{Val: i, Next: list}
	}
	var vals []string
	for n := list; n != nil; n = n.Next {
		vals = append(vals, fmt.Sprint(n.Val))
	}
	add("linked list: %s", strings.Join(vals, "->"))

	var s Stack
	for i := range 4 {
		s.Push(i * i)
	}
	v1, _ := s.Pop()
	v2, _ := s.Pop()
	add("stack: %d %d %d", v1, v2, len(s.items))

	anon := struct {
		Name string
		Age  int
	}{"x", 3}
	add("anon: %+v", anon)
	np := new(Point)
	np.Y = 4
	add("new: %v", *np)
	return strings.Join(out, "\n")
}
