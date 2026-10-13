package main

import (
	"fmt"
	"strconv"
	"strings"
)

type Weekday int

const (
	Sunday Weekday = iota
	Monday
	Tuesday
)

const greeting = "hello"

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	var i8 int8 = 127
	i8++
	add("wrap int8: %d", i8)
	var u8 uint8 = 0
	u8--
	add("wrap uint8: %d", u8)
	add("shifts: %d %d %d", 1<<10, -17>>2, uint32(1)<<31)
	add("div/mod: %d %d %d %d", 17/5, 17%5, -17/5, -17%5)
	add("floats: %.3f %v %g", 10.0/3, 1.5e3, 0.1+0.2)
	add("bits: %d %d %d %d", 6&3, 6|3, 6^3, 6&^3)
	add("complement: %d", ^5)
	add("iota: %d %d %d", Sunday, Monday, Tuesday)
	f, g := 3.99, -2.5
	add("conversions: %d %d %v %s", int(f), int64(g), float64(7)/2, string(rune(955)))
	add("strconv: %s %q", strconv.Itoa(-42), strconv.Quote(greeting))
	n, err := strconv.Atoi("12x")
	add("atoi: %d %v", n, err)
	s := "héllo, 世界"
	add("len: %d", len(s))
	for i, r := range s {
		if r > 127 {
			add("rune at %d: %c (%d)", i, r, r)
		}
	}
	add("bytes: %d %d", s[0], s[1])
	add("slice string: %s|%s", s[:1], s[7:])
	bs := []byte("abc")
	bs[0] = 'z'
	add("bytes roundtrip: %s %v", string(bs), bs)
	rs := []rune("añb")
	add("runes: %d %s", len(rs), string(rs[1:]))
	add("strings: %s %v %v", strings.ToUpper(greeting), strings.Split("a,b,c", ","), strings.Fields(" x  y "))
	before, after, found := strings.Cut("key=value", "=")
	add("cut: %s %s %v", before, after, found)
	add("min/max: %d %d %s", min(3, 1, 2), max(3, 1, 2), max("pear", "apple"))
	x := 10
	x += 5
	x *= 2
	x -= 1
	x /= 2
	x %= 8
	x <<= 3
	x |= 1
	add("op-assign: %d", x)
	var f32 float32 = 1.1
	add("float32: %v %v", f32*3, float64(f32))
	add("bool ops: %v %v %v", true && !false, 3 < 4 || false, "a" < "b")
	add("big: %d %d", int64(1)<<62, uint64(1)<<63)
	add("hex: %x %X %o %b", 255, 255, 8, 5)
	add("padding: [%5d] [%-5s] [%05.1f]", 42, "ab", 3.14159)
	return strings.Join(out, "\n")
}
