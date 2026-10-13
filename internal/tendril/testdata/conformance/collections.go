package main

import (
	"fmt"
	"slices"
	"strings"
)

func sum(nums ...int) int {
	t := 0
	for _, n := range nums {
		t += n
	}
	return t
}

func divmod(a, b int) (q, r int) {
	q = a / b
	r = a % b
	return
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	a := make([]int, 3, 10)
	b := append(a, 4)
	c := append(a, 5)
	add("shared backing: %v %v", b, c)
	d := a[1:2]
	d = append(d, 77)
	add("reslice append: %v %v %d %d", a, d, len(d), cap(d))
	full := a[0:2:2]
	full = append(full, 9)
	full[0] = -1
	add("3-index: %v %v", a, full)
	add("beyond len: %v", a[:5])
	var nilSlice []string
	add("nil slice: %v %d %v", nilSlice == nil, len(nilSlice), append(nilSlice, "x"))
	dst := make([]int, 2)
	n := copy(dst, []int{7, 8, 9})
	add("copy: %d %v", n, dst)

	words := strings.Fields("the quick brown fox jumps over the lazy dog the end")
	freq := map[string]int{}
	for _, w := range words {
		freq[w]++
	}
	var keys []string
	for k := range freq {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, freq[k]))
	}
	add("freq: %s", strings.Join(parts, " "))
	add("map print: %v", map[string]int{"b": 2, "a": 1, "c": 3})
	v, ok := freq["missing"]
	add("missing key: %d %v %d", v, ok, len(freq))
	delete(freq, "the")
	delete(freq, "nope")
	add("after delete: %d", len(freq))

	groups := map[int][]string{}
	for _, w := range words {
		groups[len(w)] = append(groups[len(w)], w)
	}
	add("groups[3]: %v", groups[3])

	nums := []int{5, 2, 9, 1, 5, 6}
	slices.Sort(nums)
	add("sorted: %v %v %d", nums, slices.Contains(nums, 9), slices.Index(nums, 6))
	add("variadic: %d %d %d", sum(), sum(1, 2, 3), sum(nums...))
	q, r := divmod(17, 5)
	add("named results: %d %d", q, r)

	matrix := make([][]int, 3)
	for i := range matrix {
		matrix[i] = make([]int, 3)
		for j := range matrix[i] {
			matrix[i][j] = i * j
		}
	}
	add("matrix: %v", matrix)

	var nilMap map[string]int
	add("nil map read: %d %d", nilMap["x"], len(nilMap))
	set := map[int]bool{}
	for _, n := range nums {
		set[n] = true
	}
	add("set size: %d", len(set))
	clear(set)
	add("cleared: %d", len(set))
	return strings.Join(out, "\n")
}
