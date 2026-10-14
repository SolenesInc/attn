package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

type Task struct {
	ID       int            `json:"id"`
	Title    string         `json:"title"`
	Tags     []string       `json:"tags,omitempty"`
	Owner    *string        `json:"owner"`
	Meta     map[string]int `json:"meta,omitempty"`
	internal string
	Skip     bool              `json:"-"`
	Extra    map[string]string `json:"extra"`
}

type Envelope struct {
	Task
	Kind string `json:"kind"`
}

type upper struct{ w io.Writer }

func (u upper) Write(p []byte) (int, error) {
	return u.w.Write([]byte(strings.ToUpper(string(p))))
}

func count(n int) func(yield func(int) bool) {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

func pairs(words []string) func(yield func(int, string) bool) {
	return func(yield func(int, string) bool) {
		for i, w := range words {
			if !yield(i*10, w) {
				return
			}
		}
	}
}

func firstOver(limit int) (found int) {
	for v := range count(100) {
		if v*v > limit {
			return v
		}
	}
	return -1
}

func Run() string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	sum := 0
	for v := range count(5) {
		if v == 3 {
			continue
		}
		sum += v
	}
	add("range over func: %d", sum)
	for i, w := range pairs([]string{"a", "b", "c"}) {
		if w == "c" {
			break
		}
		add("pair %d %s", i, w)
	}
	add("return from iterator body: %d", firstOver(50))

	ages := map[string]int{"cy": 30, "ana": 25, "bo": 41}
	add("sorted keys: %v", slices.Sorted(maps.Keys(ages)))
	add("collect values sorted: %v", slices.Sorted(maps.Values(ages)))
	var names []string
	for name, age := range maps.All(ages) {
		if age > 26 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	add("maps.All: %v", names)
	nums := []int{5, 3, 8, 1}
	add("slices: %d %d %v %v", slices.Max(nums), slices.Min(nums), slices.Contains(nums, 8), slices.Index(nums, 8))
	clone := slices.Clone(nums)
	slices.SortFunc(clone, func(a, b int) int { return b - a })
	slices.Reverse(nums)
	add("clone/sortfunc/reverse: %v %v", clone, nums)
	add("compact: %v", slices.Compact([]int{1, 1, 2, 2, 2, 3, 1}))
	add("collect: %v", slices.Collect(maps.Keys(map[int]bool{3: true})))
	for i, v := range slices.Backward([]string{"x", "y"}) {
		add("backward %d %s", i, v)
	}
	copied := maps.Clone(ages)
	maps.DeleteFunc(copied, func(k string, v int) bool { return v < 30 })
	add("maps clone/deletefunc: %d %d", len(copied), len(ages))

	var b strings.Builder
	for i := range 3 {
		fmt.Fprintf(&b, "[%d]", i)
	}
	b.WriteString("!")
	b.WriteByte('?')
	b.WriteRune('é')
	add("builder: %s %d", b.String(), b.Len())
	var lines []string
	for line := range strings.Lines("one\ntwo\nthree") {
		lines = append(lines, strings.TrimSpace(line))
	}
	add("lines: %v", lines)
	var parts []string
	for p := range strings.SplitSeq("a,b,c", ",") {
		parts = append(parts, p)
	}
	add("splitseq: %v %v", parts, strings.Fields(" x  y "))
	after, ok := strings.CutPrefix("prefix-body", "prefix-")
	add("cutprefix: %s %v %d", after, ok, strings.Compare("a", "b"))

	var sb strings.Builder
	fmt.Fprintln(upper{&sb}, "shout", 1)
	io.WriteString(&sb, "done")
	add("io.Writer: %q", sb.String())
	n, err := fmt.Fprint(os.Stderr, "")
	add("fprint to stderr: %d %v", n, err)

	owner := "ana"
	task := Task{ID: 7, Title: "Ship <it>", Owner: &owner, internal: "x", Skip: true}
	data, err := json.Marshal(task)
	add("marshal: %s %v", data, err)
	env := Envelope{Task: Task{ID: 1, Title: "t", Tags: []string{"a"}, Meta: map[string]int{"z": 1, "a": 2}}, Kind: "k"}
	pretty, _ := json.MarshalIndent(env, "", "  ")
	add("indent:\n%s", pretty)
	var back Task
	err = json.Unmarshal([]byte(`{"id": 9, "TITLE": "decoded", "tags": ["x", "y"], "owner": "bo", "meta": {"k": 3}}`), &back)
	add("unmarshal: %v %d %s %v %s %v", err, back.ID, back.Title, back.Tags, *back.Owner, back.Meta)
	var anything any
	json.Unmarshal([]byte(`{"a": [1, "two", true, null], "b": {"c": 1.5}}`), &anything)
	add("unmarshal any: %v", anything)
	var nums2 []int
	err = json.Unmarshal([]byte(`[1, "x"]`), &nums2)
	add("unmarshal error: %v", err != nil)
	raw, _ := json.Marshal(map[string]any{"list": []int{1, 2}, "nil": nil, "f": 1.5, "s": "q\"uote", "bytes": []byte("hi")})
	add("marshal map: %s %v", raw, json.Valid(raw))
	return strings.Join(out, "\n")
}
