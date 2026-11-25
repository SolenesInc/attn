package main_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/script"
	"github.com/victorarias/attn/internal/testworld"
)

type scriptChild struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines *bufio.Scanner
}

type scriptMessage struct {
	Call *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"call"`
	Done *script.Output `json:"done"`
}

func startScript(t *testing.T, source string, limits script.Limits) *scriptChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	t.Cleanup(cancel)
	c := &scriptChild{t: t, cmd: exec.CommandContext(ctx, filepath.Join(filepath.Dir(testworld.AttnBinary(t)), "attnd"), "_run-script")}
	c.cmd.Env = []string{}
	c.cmd.Stderr = os.Stderr
	var err error
	if c.stdin, err = c.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c.lines = bufio.NewScanner(stdout)
	c.lines.Buffer(nil, 1<<20)
	c.send(map[string]any{"job": map[string]any{
		"source":    source,
		"functions": []string{"seed_show", "seed_note", "seed_harvest", "guide"},
		"limits":    map[string]any{"memory": limits.Memory, "stack": limits.Stack, "compute": limits.Compute},
	}})
	return c
}

func (c *scriptChild) send(message any) {
	c.t.Helper()
	line, err := json.Marshal(message)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

func (c *scriptChild) next() scriptMessage {
	c.t.Helper()
	if !c.lines.Scan() {
		c.t.Fatalf("the child closed its pipe: %v", c.lines.Err())
	}
	var m scriptMessage
	if err := json.Unmarshal(c.lines.Bytes(), &m); err != nil {
		c.t.Fatalf("%v in %s", err, c.lines.Text())
	}
	return m
}

func (c *scriptChild) answer(name string) {
	switch name {
	case "seed_harvest":
		c.send(map[string]any{"answer": map[string]any{"refusal": map[string]any{"code": "open_ask", "message": "seed s-a2 has an open ask", "fix": "Close the ask."}}})
	case "guide":
		c.send(map[string]any{"answer": map[string]any{"text": "# Guide"}})
	default:
		c.send(map[string]any{"answer": map[string]any{"json": `{"id":"s-n1","notes":[1,2]}`}})
	}
}

func (c *scriptChild) finish() (calls []string, out script.Output) {
	c.t.Helper()
	for {
		m := c.next()
		if m.Done != nil {
			if err := c.cmd.Wait(); err != nil {
				c.t.Fatalf("the child exited: %v", err)
			}
			return calls, *m.Done
		}
		calls = append(calls, m.Call.Name+" "+string(m.Call.Args))
		c.answer(m.Call.Name)
	}
}

func TestAScriptRunsCommandsAndEndsAsTheAgentIsTold(t *testing.T) {
	t.Parallel()
	short := script.DefaultLimits
	short.Compute = 100 * time.Millisecond
	stopped := func(limit string) script.Output {
		return script.Output{Failed: "the script stopped: it reached its " + limit}
	}
	for _, tc := range []struct {
		name, source string
		calls        []string
		want         script.Output
	}{
		{
			name:   "commands, a result, console.log and return",
			source: "const s = await seed_show({seed: 's-a1'});\nconsole.log('notes', s.notes, 2n);\nconsole.error(guide());\nreturn s.id",
			calls:  []string{`seed_show {"seed":"s-a1"}`, `guide {}`},
			want:   script.Output{Returned: "s-n1", Has: true, Logs: []string{"notes [1,2] 2", "# Guide"}},
		},
		{
			name:   "an object returned",
			source: "return {a: [1, 'x']}",
			want:   script.Output{Returned: `{"a":[1,"x"]}`, Has: true},
		},
		{
			name:   "nothing returned",
			source: "seed_note({text: 'x'})",
			calls:  []string{`seed_note {"text":"x"}`},
		},
		{
			name:   "an endless loop",
			source: "let i = 0;\nwhile (true) i++",
			want:   stopped("compute limit of 100ms of its own work"),
		},
		{
			name:   "an endless loop inside a try, after an await",
			source: "await null;\ntry {\n  for (;;) {}\n} catch { return 'caught' }",
			want:   stopped("compute limit of 100ms of its own work"),
		},
		{
			name:   "a regular expression that backtracks",
			source: "return /^(a+)+$/.test('a'.repeat(40) + 'b')",
			want:   stopped("compute limit of 100ms of its own work"),
		},
		{
			name:   "an endless allocation",
			source: "const a = [];\nfor (;;) a.push({x: [1, 2, 3]})",
			want:   stopped("memory limit of 64 MB"),
		},
		{
			name:   "an endless recursion",
			source: "const f = () => f() + 1;\nf()",
			want:   stopped("stack limit of 512 KB"),
		},
		{
			name:   "an endless recursion the script catches",
			source: "const f = () => f() + 1;\ntry { f() } catch (e) { return e.name }",
			want:   script.Output{Returned: "RangeError", Has: true},
		},
		{
			name: "a script that looks for another world",
			source: `const found = ['require', 'fetch', 'std', 'os', 'setTimeout', 'setInterval', 'process', 'WebAssembly', 'Worker'].filter((name) => name in globalThis);
for (const name of ['std', 'os', 'qjs:std', 'qjs:os', 'fs', './x.js']) {
  try { await import(name); found.push(name) } catch {}
}
try { Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0); found.push('Atomics.wait') } catch {}
return [found, typeof Date.now(), typeof Math.random()]`,
			want: script.Output{Returned: `[[],"number","number"]`, Has: true},
		},
		{
			name:   "a refused command caught with try",
			source: "try {\n  seed_harvest({seed: 's-a2'});\n} catch (e) {\n  console.log(e.name, e.code, e.message, e.fix, e.command);\n}\nreturn seed_show({}).id",
			calls:  []string{`seed_harvest {"seed":"s-a2"}`, `seed_show {}`},
			want:   script.Output{Returned: "s-n1", Has: true, Logs: []string{"Refusal open_ask seed s-a2 has an open ask Close the ask. seed_harvest"}},
		},
		{
			name:   "a refused command left uncaught",
			source: "seed_note({text: 'x'});\n\nseed_harvest({seed: undefined, reason: 'Shipped'});\nseed_note({text: 'y'})",
			calls:  []string{`seed_note {"text":"x"}`, `seed_harvest {"reason":"Shipped"}`},
			want:   script.Output{Line: 3, Failed: "seed_harvest refused at line 3: open_ask: seed s-a2 has an open ask\nundefined in the call: seed\nFix: Close the ask."},
		},
		{
			name:   "a refused command in a promise nothing awaits",
			source: "['s-a2'].forEach(async (seed) => {\n  seed_harvest({seed});\n});\nreturn 'all harvested'",
			calls:  []string{`seed_harvest {"seed":"s-a2"}`},
			want:   script.Output{Line: 2, Failed: "seed_harvest refused at line 2: open_ask: seed s-a2 has an open ask\nFix: Close the ask."},
		},
		{
			name:   "a name that is no command",
			source: "\nseed_notes({text: 'x'})",
			want:   script.Output{Line: 2, Failed: "ReferenceError at line 2: seed_notes is not defined; near it: seed_note, seed_show, seed_harvest"},
		},
		{
			name:   "an error after console.log",
			source: "console.log('before');\nnull.x",
			want:   script.Output{Line: 2, Failed: "TypeError at line 2: cannot read property 'x' of null", Logs: []string{"before"}},
		},
		{
			name:   "a script that does not parse",
			source: "const x = 1;\nif (x) {",
			want:   script.Output{Line: 2, Failed: "SyntaxError at line 2: unexpected token in expression: ')'"},
		},
		{
			name:   "a promise nothing resolves",
			source: "await new Promise(() => {})",
			want:   script.Output{Failed: "the script did not finish: it waits for a promise that nothing resolves"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls, out := startScript(t, tc.source, short).finish()
			if !reflect.DeepEqual(calls, tc.calls) {
				t.Errorf("calls %q, want %q", calls, tc.calls)
			}
			if !reflect.DeepEqual(out, tc.want) {
				t.Errorf("done\n%+v, want\n%+v", out, tc.want)
			}
		})
	}
}

func TestAScriptWaitingForAnAnswerSpendsNoneOfItsCompute(t *testing.T) {
	t.Parallel()
	limits := script.DefaultLimits
	limits.Compute = 300 * time.Millisecond
	waiting := startScript(t, "seed_show({});\nfor (let i = 0; i < 200000; i++);\nreturn 'done'", limits)
	if call := waiting.next().Call; call == nil || call.Name != "seed_show" {
		t.Fatalf("the script sent %+v first", call)
	}

	limits.Compute = 2 * limits.Compute
	busyFor := time.Now()
	startScript(t, "for (;;);", limits).finish()
	if waited := time.Since(busyFor); waited < limits.Compute {
		t.Fatalf("the busy script ended after %s", waited)
	}

	waiting.answer("seed_show")
	if _, out := waiting.finish(); out.Returned != "done" {
		t.Fatalf("the script that waited ended with %+v", out)
	}
}

func TestAScriptsChildExitsWhenItsPipeCloses(t *testing.T) {
	t.Parallel()
	limits := script.DefaultLimits
	limits.Compute = time.Hour
	for _, tc := range []struct {
		name     string
		answered bool
	}{{"waiting for an answer", false}, {"in an endless loop", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			child := startScript(t, "seed_show({});\nfor (;;);", limits)
			child.next()
			if tc.answered {
				child.answer("seed_show")
			}
			if err := child.stdin.Close(); err != nil {
				t.Fatal(err)
			}
			var exit *exec.ExitError
			if err := child.cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("the child ended with %v", err)
			}
		})
	}
}

func TestAKilledScriptFailsItsRunAndNoMore(t *testing.T) {
	var calls []string
	job := script.Job{Source: "seed_note({text: 'x'});\nseed_show({});\nreturn 'done'", Functions: []string{"seed_note", "seed_show"}, Limits: script.DefaultLimits}
	out := script.Run(context.Background(), job, func(name string, _ json.RawMessage) script.Answer {
		calls = append(calls, name)
		if name == "seed_show" {
			killOnlyChild(t)
		}
		return script.Answer{JSON: "{}"}
	})
	if want := (script.Output{Failed: "the script's runner died before the script ended: signal: killed"}); !reflect.DeepEqual(out, want) {
		t.Errorf("the run of a killed child ended with %+v, want %+v", out, want)
	}
	if want := []string{"seed_note", "seed_show"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}
}

func killOnlyChild(t *testing.T) {
	t.Helper()
	children, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid()), "-f", script.Command).Output()
	if err != nil {
		t.Fatalf("list the children: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(children)))
	if err != nil {
		t.Fatalf("not one child: %q", children)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
}

func TestACrashInTheInterpreterFailsItsRunAndNamesTheSignal(t *testing.T) {
	t.Parallel()
	job := script.Job{Source: "const f = () => f() + 1;\nf()", Functions: []string{"seed_show"}, Limits: script.DefaultLimits}
	job.Limits.Stack = 1 << 40
	out := script.Run(context.Background(), job, nil)
	if !regexp.MustCompile(`^the script's runner died before the script ended: exit status 2: SIG[A-Z]+: `).MatchString(out.Failed) {
		t.Errorf("the run of a crashed child ended with %+v", out)
	}
}

func TestAScriptThatFloodsItsLogStopsAtItsMemoryAndKeepsWhatItLogged(t *testing.T) {
	t.Parallel()
	job := script.Job{Source: "for (;;) console.log('x'.repeat(1000))", Functions: []string{"seed_show"}, Limits: script.DefaultLimits}
	job.Limits.Memory = 4 << 20
	out := script.Run(context.Background(), job, nil)
	if want := "the script stopped: it reached its memory limit of 4 MB"; out.Failed != want || len(out.Logs) == 0 {
		t.Errorf("the run ended with %q and %d logs, want %q and the logs", out.Failed, len(out.Logs), want)
	}
}

func TestAScriptEndsWithItsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	limits := script.DefaultLimits
	limits.Compute = time.Hour
	out := script.Run(ctx, script.Job{Source: "seed_show({});\nfor (;;);", Functions: []string{"seed_show"}, Limits: limits}, func(string, json.RawMessage) script.Answer {
		cancel(errors.New("the call's 50s are over"))
		return script.Answer{JSON: "{}"}
	})
	if want := (script.Output{Failed: "the script was stopped before it ended: the call's 50s are over"}); !reflect.DeepEqual(out, want) {
		t.Errorf("the run ended with %+v, want %+v", out, want)
	}
}
