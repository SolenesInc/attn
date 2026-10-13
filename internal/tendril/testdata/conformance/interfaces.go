package main

import (
	"errors"
	"fmt"
	"strings"
)

type Shape interface {
	Area() float64
	Name() string
}

type Square struct{ Side float64 }

func (s Square) Area() float64 { return s.Side * s.Side }
func (s Square) Name() string  { return "square" }

type Circle struct{ R float64 }

func (c *Circle) Area() float64 { return 3 * c.R * c.R }
func (c *Circle) Name() string  { return "circle" }

type NotFound struct{ Key string }

func (e *NotFound) Error() string { return "not found: " + e.Key }

var ErrEmpty = errors.New("empty key")

func lookup(key string) (int, error) {
	switch key {
	case "":
		return 0, ErrEmpty
	case "a":
		return 1, nil
	}
	return 0, &NotFound{Key: key}
}

func describe(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case int:
		return fmt.Sprintf("int %d", x+1)
	case string:
		return "string " + strings.ToUpper(x)
	case Square:
		return fmt.Sprintf("square side %v", x.Side)
	case Shape:
		return "shape " + x.Name()
	case error:
		return "error " + x.Error()
	case []int:
		return fmt.Sprintf("ints %d", len(x))
	default:
		return fmt.Sprintf("other %v", x)
	}
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	shapes := []Shape{Square{2}, &Circle{1}}
	total := 0.0
	for _, s := range shapes {
		total += s.Area()
		add("%s %.1f", s.Name(), s.Area())
	}
	add("total %.1f", total)

	for _, k := range []string{"a", "", "zz"} {
		v, err := lookup(k)
		var nf *NotFound
		switch {
		case err == nil:
			add("found %d", v)
		case errors.Is(err, ErrEmpty):
			add("empty: %v", err)
		default:
			nf2, ok := err.(*NotFound)
			nf = nf2
			add("missing %v %v %s", ok, err, nf.Key)
		}
	}
	wrapped := fmt.Errorf("lookup failed: %w", ErrEmpty)
	add("wrapped: %v %v", wrapped, errors.Is(wrapped, ErrEmpty))

	for _, v := range []any{nil, 41, "hi", Square{3}, &Circle{2}, errors.New("boom"), []int{1, 2}, 2.5} {
		add("describe: %s", describe(v))
	}

	var a any = 3
	n, ok := a.(int)
	str, ok2 := a.(string)
	add("comma-ok: %d %v %q %v", n, ok, str, ok2)
	var b any = 3
	add("iface eq: %v %v %v", a == b, a == any(4), a == nil)
	var err error
	add("nil error: %v", err == nil)
	var sh Shape = Square{1}
	_, isSquare := sh.(Square)
	add("assert concrete: %v", isSquare)
	return strings.Join(out, "\n")
}
