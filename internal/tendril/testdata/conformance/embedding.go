package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Base struct {
	ID   int
	Name string
}

func (b Base) Describe() string { return fmt.Sprintf("#%d %s", b.ID, b.Name) }

func (b *Base) Rename(n string) { b.Name = n }

type Timestamps struct{ Created, Updated int }

func (t *Timestamps) Touch(at int) { t.Updated = at }

type User struct {
	Base
	*Timestamps
	Email string
}

type Admin struct {
	User
	Level int
}

func (a Admin) Describe() string { return "admin " + a.User.Describe() }

type Describer interface{ Describe() string }

type Counter struct {
	sync.Mutex
	hits map[string]int
}

func (c *Counter) Hit(k string) {
	c.Lock()
	defer c.Unlock()
	c.hits[k]++
}

type Celsius float64

func (c Celsius) String() string { return fmt.Sprintf("%.1f°C", float64(c)) }

func (c *Celsius) Warm(by Celsius) { *c += by }

type wrapped struct {
	error
	code int
}

type Logger struct{ lines []string }

func (l *Logger) Log(s string) { l.lines = append(l.lines, s) }

func apply(fs []func(string), v string) {
	for _, f := range fs {
		f(v)
	}
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	u := User{Base: Base{ID: 1, Name: "ana"}, Timestamps: &Timestamps{Created: 10}, Email: "a@x"}
	add("promoted field: %d %s %d", u.ID, u.Name, u.Created)
	u.Rename("bea")
	u.Touch(20)
	add("promoted methods: %s %d", u.Describe(), u.Updated)

	a := Admin{User: u, Level: 2}
	a.Rename("cy")
	add("shadowed and deep: %s | %s | %s", a.Describe(), a.User.Describe(), a.Base.Describe())
	add("copy kept: %s", u.Name)

	var ds []Describer = []Describer{u, a, &u, Base{9, "z"}}
	var described []string
	for _, d := range ds {
		described = append(described, d.Describe())
	}
	add("interface through embedding: %s", strings.Join(described, "; "))

	c := Counter{hits: map[string]int{}}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() { c.Hit([]string{"x", "y"}[i%3%2]) })
	}
	wg.Wait()
	add("embedded mutex: %v", c.hits)

	temp := Celsius(20)
	temp.Warm(1.5)
	add("named float methods: %v %s", temp, temp)

	describe := u.Describe
	u.Rename("changed later")
	add("method value binds a copy: %s", describe())
	rename := u.Rename
	rename("via value")
	add("pointer method value: %s", u.Name)
	var l Logger
	apply([]func(string){l.Log, l.Log}, "twice")
	add("method values as funcs: %v", l.lines)

	describeExpr := Base.Describe
	renameExpr := (*Base).Rename
	b := Base{ID: 3, Name: "b"}
	renameExpr(&b, "bee")
	add("method expressions: %s", describeExpr(b))

	var d Describer = a
	f := d.Describe
	add("interface method value: %s", f())

	w := wrapped{errors.New("disk full"), 28}
	var err error = w
	add("embedded interface: %v %d", err, w.code)

	names := []string{"c", "a", "b"}
	sort.Strings(names)
	add("sort: %v", names)
	people := []Base{{3, "x"}, {1, "y"}, {2, "z"}}
	sort.Slice(people, func(i, j int) bool { return people[i].ID < people[j].ID })
	add("sort.Slice: %v", people)
	return strings.Join(out, "\n")
}
