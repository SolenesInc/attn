package tendril

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustEnv(t testing.TB, extra ...*Package) *Env {
	t.Helper()
	env, err := NewEnv(append(Std(), extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func mustCompile(t testing.TB, env *Env, src string) *Program {
	t.Helper()
	p, err := env.Compile("script.go", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return p
}

func TestLimitsStopHostileScripts(t *testing.T) {
	env := mustEnv(t)
	lim := Limits{Fuel: 5_000_000, Memory: 32 << 20, Depth: 200}
	cases := []struct {
		name string
		src  string
		want error
	}{
		{"empty infinite loop", `func Run() { for {} }`, ErrFuel},
		{"busy loop", `func Run() int { n := 0; for { n++ } }`, ErrFuel},
		{"runaway recursion", "func f(n int) int { return f(n+1) + 1 }\nfunc Run() int { return f(0) }", ErrDepth},
		{"mutual recursion", "func a(n int) int { return b(n) }\nfunc b(n int) int { return a(n) }\nfunc Run() int { return a(0) }", ErrDepth},
		{"string doubling", `func Run() int { s := "x"; for { s += s } }`, ErrMemory},
		{"huge make", `func Run() int { return len(make([]int, 1<<40)) }`, ErrMemory},
		{"append forever", `func Run() int { var s []string; for { s = append(s, "x") } }`, ErrMemory},
		{"map forever", `func Run() int { m := map[int]bool{}; for i := 0; ; i++ { m[i] = true } }`, ErrMemory},
		{"strings.Repeat bomb", "import \"strings\"\nfunc Run() int { return len(strings.Repeat(\"x\", 1<<40)) }", ErrMemory},
		{"ReplaceAll amplification", "import \"strings\"\nfunc Run() int { s := strings.Repeat(\"a\", 1<<12); return len(strings.ReplaceAll(s, \"a\", s)) }", ErrMemory},
		{"Sprintf padding", "import \"fmt\"\nfunc Run() { for { _ = fmt.Sprintf(\"%1000000d\", 1) } }", ErrMemory},
		{"closure allocation", `func Run() { var fs []func() int; for i := 0; ; i++ { fs = append(fs, func() int { return i }) } }`, ErrMemory},
		{"halt inside a method fmt calls", "import \"fmt\"\ntype E struct{}\nfunc (E) Error() string { for {} }\nfunc Run() string { return fmt.Sprintf(\"%v\", E{}) }", ErrFuel},
		{"halt inside a String method", "import \"fmt\"\ntype S struct{}\nfunc (S) String() string { return string(make([]byte, 1<<40)) }\nfunc Run() string { return fmt.Sprint(S{}) }", ErrMemory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustCompile(t, env, tc.src)
			start := time.Now()
			_, stats, err := p.Call(context.Background(), lim, "Run")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			t.Logf("%v after %v (fuel %d, memory %d)", err, time.Since(start).Round(time.Millisecond), stats.Fuel, stats.Memory)
		})
	}
}

func TestDeadlineStopsAScriptWithUnlimitedFuel(t *testing.T) {
	p := mustCompile(t, mustEnv(t), `func Run() { for {} }`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := p.Call(ctx, Limits{Fuel: 1 << 62, Memory: 1 << 20, Depth: 10}, "Run")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v to notice the deadline", elapsed)
	}
}

func TestCompileRejectsEscapes(t *testing.T) {
	env := mustEnv(t)
	cases := map[string]string{
		"import \"os\"\nfunc Run() { os.Exit(1) }":             `package "os" is not available`,
		"import \"unsafe\"\nvar _ = unsafe.Sizeof(1)":          `package "unsafe" is not available`,
		"import \"net/http\"\nvar _ = http.Get":                `package "net/http" is not available`,
		`func Run() { go Run() }`:                              "go statements are not available",
		`func Run() { c := make(chan int); c <- 1 }`:           "channels are not available",
		`func Run() { print("hi") }`:                           "print writes to the host's stderr",
		`func Run() { defer Run() }`:                           "defer is not supported",
		`func Run() { _ = recover() }`:                         "recover is not available",
		`func Run() { var a [4]int; _ = a }`:                   "arrays are not supported",
		`func Run() { x := 1; p := &x; _ = p }`:                "& is only supported on composite literals",
		`func Run() int { return "x" }`:                        `cannot use "x"`,
		`type K struct{ A int }; var m map[K]int`:              "map keys must be strings, numbers or booleans",
		`func Run() { for i := range 3 { fmt.Println(i) } }`:   "undefined: fmt",
		"import \"strings\"\nvar b strings.Builder":            "undefined: strings.Builder",
		"import \"strings\"\nfunc Run() { strings.NewReader }": "undefined: strings.NewReader",
	}
	for src, want := range cases {
		_, err := env.Compile("script.go", src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q\n  got  %v\n  want %q", src, err, want)
		}
	}
}

func TestRuntimeErrorsCarryScriptPositions(t *testing.T) {
	env := mustEnv(t)
	cases := []struct{ src, want string }{
		{"func Run() int {\n\ts := []int{1, 2}\n\ti := 5\n\treturn s[i]\n}", "script.go:4:2: runtime error: index out of range [5] with length 2"},
		{"func Run() {\n\tvar m map[string]int\n\tm[\"x\"] = 1\n}", "script.go:3:2: runtime error: assignment to entry in nil map"},
		{"func Run() int {\n\tz := 0\n\treturn 1 / z\n}", "script.go:3:2: runtime error: integer divide by zero"},
		{"type T struct{ X int }\nfunc Run() int {\n\tvar p *T\n\treturn p.X\n}", "script.go:4:2: runtime error: invalid memory address or nil pointer dereference"},
		{"func Run() int {\n\tvar x any = \"s\"\n\treturn x.(int)\n}", "script.go:3:2: runtime error: interface conversion: any is string, not int"},
		{"func Run() {\n\tpanic(\"boom\")\n}", "script.go:2:2: panic: boom"},
		{"import \"errors\"\nfunc Run() {\n\tpanic(errors.New(\"wrapped boom\"))\n}", "script.go:3:2: panic: wrapped boom"},
		{"func helper(s []int) int {\n\treturn s[9]\n}\nfunc Run() int {\n\treturn helper(nil) + 1\n}", "script.go:2:2: runtime error: index out of range [9] with length 0"},
	}
	for _, tc := range cases {
		_, _, err := mustCompile(t, env, tc.src).Call(context.Background(), DefaultLimits, "Run")
		if err == nil || err.Error() != tc.want {
			t.Errorf("got  %v\nwant %s", err, tc.want)
		}
	}
}

func TestHostPanicsBecomeErrors(t *testing.T) {
	host := NewPackage("host").
		Func("Boom", func() string { panic("kaboom") }).
		Func("Nil", func() int { var m map[string]int; m["x"] = 1; return 0 })
	env := mustEnv(t, host)
	for _, fn := range []string{"Boom", "Nil"} {
		src := fmt.Sprintf("import \"host\"\nfunc Run() { host.%s() }", fn)
		_, _, err := mustCompile(t, env, src).Call(context.Background(), DefaultLimits, "Run")
		var he hostPanic
		if !errors.As(err, &he) {
			t.Fatalf("%s: got %v", fn, err)
		}
		t.Log(err)
	}
}

func TestRunsAreIsolatedAndDeterministic(t *testing.T) {
	env := mustEnv(t)
	p := mustCompile(t, env, `
import (
	"fmt"
	"strings"
)

var calls int

func Run(seed int) string {
	calls++
	m := map[string]int{}
	for i := range 500 {
		m[fmt.Sprintf("k%d", (i*seed)%997)] = i
	}
	var b []string
	for k, v := range m {
		b = append(b, fmt.Sprintf("%s=%d", k, v))
	}
	return fmt.Sprint(calls, " ", len(m), " ", strings.Join(b, ",")[:200])
}`)
	first, firstStats, err := p.Call(context.Background(), DefaultLimits, "Run", 31)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first[0].(string), "1 500 ") {
		t.Fatalf("globals leaked between runs or map lost entries: %s", first[0])
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, stats, err := p.Call(context.Background(), DefaultLimits, "Run", 31)
			if err != nil || out[0] != first[0] || stats != firstStats {
				t.Errorf("run diverged: %v %v %v", err, stats, firstStats)
			}
		}()
	}
	wg.Wait()
}

func TestHostValuesAreCopiedAcrossTheBoundary(t *testing.T) {
	type Item struct {
		Name string
		Tags []string
	}
	host := NewPackage("host").Type("Item", Item{})
	env := mustEnv(t, host)
	p := mustCompile(t, env, `
import "host"

func Run(it host.Item, xs []int) host.Item {
	it.Tags[0] = "changed"
	it.Name = "changed"
	xs[0] = 99
	it.Tags = append(it.Tags, "added")
	return it
}`)
	in := Item{Name: "orig", Tags: []string{"a"}}
	xs := []int{1}
	out, _, err := p.Call(context.Background(), DefaultLimits, "Run", in, xs)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "orig" || in.Tags[0] != "a" || xs[0] != 1 {
		t.Fatalf("script mutated host memory: %+v %v", in, xs)
	}
	got := out[0].(Item)
	if got.Name != "changed" || strings.Join(got.Tags, ",") != "changed,added" {
		t.Fatalf("result: %+v", got)
	}
}
