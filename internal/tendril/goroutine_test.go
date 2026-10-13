package tendril

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGoroutineFailuresEndTheRun(t *testing.T) {
	env := mustEnv(t)
	lim := Limits{Fuel: 2_000_000, Memory: 16 << 20, Depth: 100, Goroutines: 50}
	cases := []struct {
		name, src string
		want      error
		message   string
	}{
		{"receive with no sender", "func Run() {\n\tch := make(chan int)\n\t<-ch\n}", ErrDeadlock, ""},
		{"wait with no done", "import \"sync\"\nfunc Run() {\n\tvar wg sync.WaitGroup\n\twg.Add(1)\n\twg.Wait()\n}", ErrDeadlock, ""},
		{"empty select", "func Run() { select {} }", ErrDeadlock, ""},
		{"all workers blocked", "func Run() {\n\tch := make(chan int)\n\tfor range 3 {\n\t\tgo func() { ch <- 1 }()\n\t}\n\tselect {}\n}", ErrDeadlock, ""},
		{"goroutine bomb", "func Run() {\n\tfor {\n\t\tgo func() { select {} }()\n\t}\n}", ErrGoroutines, ""},
		{"spinning goroutine", "func Run() {\n\tgo func() {\n\t\tfor {\n\t\t}\n\t}()\n\tselect {}\n}", ErrFuel, ""},
		{"panic in goroutine", "func Run() {\n\tdone := make(chan bool)\n\tgo func() {\n\t\tpanic(\"worker exploded\")\n\t}()\n\t<-done\n}", nil, "script.go:4:3: panic: worker exploded"},
		{"send on closed channel", "func Run() {\n\tch := make(chan int)\n\tclose(ch)\n\tch <- 1\n}", nil, "script.go:4:2: runtime error: send on closed channel"},
		{"double close", "func Run() {\n\tch := make(chan int)\n\tclose(ch)\n\tclose(ch)\n}", nil, "close of closed channel"},
		{"negative waitgroup", "import \"sync\"\nfunc Run() {\n\tvar wg sync.WaitGroup\n\twg.Done()\n}", nil, "negative WaitGroup counter"},
		{"unlock of unlocked mutex", "import \"sync\"\nfunc Run() {\n\tvar mu sync.Mutex\n\tmu.Unlock()\n}", nil, "unlock of unlocked mutex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			_, _, err := mustCompile(t, env, tc.src).Call(context.Background(), lim, "Run")
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.message != "" && (err == nil || !strings.Contains(err.Error(), tc.message)) {
				t.Fatalf("got %v, want %q", err, tc.message)
			}
			if after := runtime.NumGoroutine(); after > before {
				t.Fatalf("leaked %d goroutines", after-before)
			}
			t.Log(err)
		})
	}
}

func TestPreemptionKeepsOtherGoroutinesRunning(t *testing.T) {
	src := `
func Run() int {
	stop := false
	go func() {
		for !stop {
		}
	}()
	total := 0
	for i := range 1000 {
		total += i
	}
	stop = true
	return total
}`
	out, _, err := mustCompile(t, mustEnv(t), src).Call(context.Background(), DefaultLimits, "Run")
	if err != nil || out[0] != 499500 {
		t.Fatalf("%v %v", out, err)
	}
}

func TestMainReturningEndsOtherGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	src := `
func Run() string {
	ch := make(chan int)
	for range 20 {
		go func() { ch <- 1 }()
		go func() { select {} }()
	}
	<-ch
	return "done"
}`
	out, _, err := mustCompile(t, mustEnv(t), src).Call(context.Background(), DefaultLimits, "Run")
	if err != nil || out[0] != "done" {
		t.Fatalf("%v %v", out, err)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("leaked %d goroutines", after-before)
	}
}

func TestConcurrentHostCallsOverlap(t *testing.T) {
	slow := NewPackage("tools").Concurrent().
		Func("Fetch", func(ctx context.Context, id int) (string, error) {
			select {
			case <-time.After(100 * time.Millisecond):
				return "item", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
	env := mustEnv(t, slow)
	src := `
import (
	"fmt"
	"tools"

	"golang.org/x/sync/errgroup"
)

func Run() (string, error) {
	var g errgroup.Group
	got := make([]string, 8)
	for i := range got {
		g.Go(func() error {
			v, err := tools.Fetch(i)
			got[i] = fmt.Sprint(v, i)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}
	return fmt.Sprint(got), nil
}`
	p := mustCompile(t, env, src)
	start := time.Now()
	out, _, err := p.Call(context.Background(), DefaultLimits, "Run")
	elapsed := time.Since(start)
	if err != nil || out[0] != "[item0 item1 item2 item3 item4 item5 item6 item7]" {
		t.Fatalf("%v %v", out, err)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("8 concurrent 100ms calls took %v; they ran serially", elapsed)
	}
	t.Logf("8 x 100ms host calls in %v", elapsed.Round(time.Millisecond))

	before := runtime.NumGoroutine()
	abandon := `
import "tools"

func Run() string {
	for i := range 4 {
		go tools.Fetch(i)
	}
	return "left them running"
}`
	out, _, err = mustCompile(t, env, abandon).Call(context.Background(), DefaultLimits, "Run")
	if err != nil || out[0] != "left them running" {
		t.Fatalf("%v %v", out, err)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("leaked %d goroutines", after-before)
	}
}
