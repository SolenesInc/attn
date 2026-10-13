package tendril

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/dop251/goja"
	"github.com/victorarias/attn/internal/automation"
)

const fibGo = `
func Fib(n int) int {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
}`

const fibJS = `function Fib(n) { if (n < 2) { return n } return Fib(n-1) + Fib(n-2) }`

const loopGo = `
func Loop(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		if i%3 == 0 {
			total += i * 2
		} else {
			total -= i
		}
	}
	return total
}`

const loopJS = `function Loop(n) { let total = 0; for (let i = 0; i < n; i++) { if (i % 3 == 0) { total += i * 2 } else { total -= i } } return total }`

const wordsGo = `
import (
	"slices"
	"strings"
)

func Words(text string) string {
	counts := map[string]int{}
	for _, w := range strings.Fields(text) {
		counts[strings.ToLower(w)]++
	}
	var keys []string
	for k, n := range counts {
		if n > 1 {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}`

const wordsJS = `function Words(text) {
	const counts = new Map();
	for (const w of text.split(/\s+/)) { if (w) { const k = w.toLowerCase(); counts.set(k, (counts.get(k) || 0) + 1) } }
	const keys = [];
	for (const [k, n] of counts) { if (n > 1) keys.push(k) }
	keys.sort();
	return keys.join(",");
}`

func fibNative(n int) int {
	if n < 2 {
		return n
	}
	return fibNative(n-1) + fibNative(n-2)
}

var benchText = strings.Repeat("The quick brown fox jumps over the lazy dog while the cat watches the fox ", 50)

var unlimited = Limits{Fuel: 1 << 62, Memory: 1 << 40, Depth: 10_000}

func benchTendril(b *testing.B, src, fn string, arg any) {
	p := mustCompile(b, mustEnv(b), src)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := p.Call(context.Background(), unlimited, fn, arg); err != nil {
			b.Fatal(err)
		}
	}
}

func benchGoja(b *testing.B, src, fn string, arg any) {
	vm := goja.New()
	if _, err := vm.RunString(src); err != nil {
		b.Fatal(err)
	}
	f, _ := goja.AssertFunction(vm.Get(fn))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := f(goja.Undefined(), vm.ToValue(arg)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFib20(b *testing.B) {
	b.Run("tendril", func(b *testing.B) { benchTendril(b, fibGo, "Fib", 20) })
	b.Run("goja", func(b *testing.B) { benchGoja(b, fibJS, "Fib", 20) })
	b.Run("native", func(b *testing.B) {
		for b.Loop() {
			fibNative(20)
		}
	})
}

func BenchmarkLoop100k(b *testing.B) {
	b.Run("tendril", func(b *testing.B) { benchTendril(b, loopGo, "Loop", 100_000) })
	b.Run("goja", func(b *testing.B) { benchGoja(b, loopJS, "Loop", 100_000) })
}

func BenchmarkWords(b *testing.B) {
	b.Run("tendril", func(b *testing.B) { benchTendril(b, wordsGo, "Words", benchText) })
	b.Run("goja", func(b *testing.B) { benchGoja(b, wordsJS, "Words", benchText) })
}

func BenchmarkReviewGate(b *testing.B) {
	src, err := os.ReadFile("testdata/examples/review_gate.go")
	if err != nil {
		b.Fatal(err)
	}
	env := reviewGateEnv(b, map[int][]string{7: {"README.md", "internal/pty/wirefeed.go", "app/src/main.ts"}})
	pr := automation.PullRequestInput{Owner: "victorarias", Repository: "attn", URL: "https://github.com/victorarias/attn/pull/7", Number: 7}
	b.Run("compile", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := env.Compile("review_gate.go", string(src)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("call", func(b *testing.B) {
		p, err := env.Compile("review_gate.go", string(src))
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := p.Call(context.Background(), DefaultLimits, "Gate", pr); err != nil {
				b.Fatal(err)
			}
		}
	})
}
