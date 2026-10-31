package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/testhistory"
)

var testHistory string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "test-history-bin")
	if err != nil {
		panic(err)
	}
	testHistory = filepath.Join(dir, "test-history")
	if out, err := exec.Command("go", "build", "-o", testHistory, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func fixtureModule(t *testing.T) string {
	return absolute(t, "../../internal/testhistory/testdata/projects/go")
}

func absolute(t *testing.T, path string) string {
	dir, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func historyCommand(t *testing.T, spool, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(testHistory, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ATTN_TEST_HISTORY_DIR="+spool, "FIXTURE_SLEEP=")
	return cmd
}

func exitCode(err error) int {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		panic(err)
	}
	return 0
}

func sealedReceipts(t *testing.T, spool string) []testhistory.Receipt {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(spool, "receipts", "*.json"))
	var receipts []testhistory.Receipt
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var receipt testhistory.Receipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, receipt)
	}
	return receipts
}

func onlyReceipt(t *testing.T, spool string) testhistory.Receipt {
	t.Helper()
	receipts := sealedReceipts(t, spool)
	if len(receipts) != 1 {
		t.Fatalf("sealed %d receipts, want 1", len(receipts))
	}
	if working, _ := os.ReadDir(filepath.Join(spool, "working")); len(working) != 0 {
		t.Fatalf("left %d working entries after sealing", len(working))
	}
	return receipts[0]
}

func outcomeOf(receipt testhistory.Receipt, unit string) testhistory.Outcome {
	for _, result := range receipt.Results {
		if result.Unit == unit && result.Case == "" {
			return result.Outcome
		}
	}
	return ""
}

var durations = regexp.MustCompile(`\(\d+\.\d+s\)|\t\d+\.\d+s|\d+(\.\d+)? ns/op|\t\d+\t|goroutine \d+|0x[0-9a-f]+|\{0x[0-9a-f?, ]+\}`)

func TestCapturedGoRunPrintsWhatGoTestPrintsAndKeepsItsExitStatus(t *testing.T) {
	for _, tc := range []struct {
		module  string
		args    []string
		unit    string
		outcome testhistory.Outcome
	}{
		{module: "testdata/render", args: []string{"-count=1", "./order", "./panics"}, unit: "go ./order TestPrintsAndPasses", outcome: testhistory.Pass},
		{module: "testdata/render", args: []string{"-count=1", "-bench", ".", "./bench"}, unit: "go ./bench TestPasses", outcome: testhistory.Pass},
		{args: []string{"-count=1", "./..."}, unit: "go ./broken", outcome: testhistory.BuildFailure},
		{args: []string{"-count=1", "-v", "-run", "TestCases", "./good"}, unit: "go ./good TestCases", outcome: testhistory.Fail},
		{args: []string{"-count=1", "-run", "TestPasses", "./good"}, unit: "go ./good TestPasses", outcome: testhistory.Pass},
		{args: []string{"-count=1", "-cover", "-run", "NoSuchTest", "./good"}, unit: "go ./good", outcome: testhistory.Pass},
	} {
		args := tc.args
		module := fixtureModule(t)
		if tc.module != "" {
			module = absolute(t, tc.module)
		}
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			plain := exec.Command("go", append([]string{"test"}, args...)...)
			plain.Dir = module
			plain.Env = append(os.Environ(), "FIXTURE_SLEEP=")
			plainOut, plainErr := plain.CombinedOutput()

			spool := t.TempDir()
			captured := historyCommand(t, spool, module, append([]string{"capture", "go", "--"}, args...)...)
			capturedOut, capturedErr := captured.CombinedOutput()

			if got, want := exitCode(capturedErr), exitCode(plainErr); got != want {
				t.Fatalf("capture exited %d, go test exited %d", got, want)
			}
			if got, want := durations.ReplaceAllString(string(capturedOut), "D"), durations.ReplaceAllString(string(plainOut), "D"); got != want {
				t.Fatalf("capture printed:\n%s\ngo test printed:\n%s", got, want)
			}
			receipt := onlyReceipt(t, spool)
			if receipt.Command.Exit == nil || receipt.Command.Exit.Code != exitCode(plainErr) || !receipt.Complete {
				t.Fatalf("receipt command = %+v complete=%v, want exit %d and complete", receipt.Command, receipt.Complete, exitCode(plainErr))
			}
			if got := outcomeOf(receipt, tc.unit); got != tc.outcome {
				t.Fatalf("%s = %q, want %s", tc.unit, got, tc.outcome)
			}
		})
	}
}

func TestCaptureKeepsTheExitStatusOfACommandThatFailsWithoutResults(t *testing.T) {
	for _, tc := range []struct {
		script string
		code   int
		signal string
	}{
		{script: "exit 3", code: 3},
		{script: "kill -TERM $$", code: 128 + int(syscall.SIGTERM), signal: "SIGTERM"},
	} {
		spool := t.TempDir()
		err := historyCommand(t, spool, t.TempDir(), "capture", "native", "--", "sh", "-c", tc.script).Run()
		if got := exitCode(err); got != tc.code {
			t.Fatalf("%q: capture exited %d, want %d", tc.script, got, tc.code)
		}
		receipt := onlyReceipt(t, spool)
		if receipt.Command.Exit == nil || receipt.Command.Exit.Code != tc.code || receipt.Command.Exit.Signal != tc.signal {
			t.Fatalf("%q: receipt exit = %+v, want code %d signal %q", tc.script, receipt.Command.Exit, tc.code, tc.signal)
		}
		if receipt.Complete || len(receipt.Results) != 0 {
			t.Fatalf("%q: a command that reported nothing sealed complete=%v with %d results", tc.script, receipt.Complete, len(receipt.Results))
		}
	}
}

func TestAnInterruptedGoRunKeepsTheResultsItFinished(t *testing.T) {
	spool := t.TempDir()
	cmd := historyCommand(t, spool, fixtureModule(t), "capture", "go", "--", "-count=1", "-v", "-run", "TestPasses|TestSleeps", "./good")
	cmd.Env = append(cmd.Env, "FIXTURE_SLEEP=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	for lines.Scan() && !strings.HasPrefix(lines.Text(), "=== RUN   TestSleeps") {
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, stdout)
	code := exitCode(cmd.Wait())

	receipt := onlyReceipt(t, spool)
	if receipt.Command.Interrupted != "SIGINT" || receipt.Command.Exit == nil || receipt.Command.Exit.Code != code {
		t.Fatalf("receipt command = %+v, want interrupted by SIGINT with exit %d", receipt.Command, code)
	}
	if got := outcomeOf(receipt, "go ./good TestPasses"); got != testhistory.Pass {
		t.Fatalf("TestPasses = %q, want pass", got)
	}
	if got := outcomeOf(receipt, "go ./good TestSleeps"); got != testhistory.Interrupted {
		t.Fatalf("TestSleeps = %q, want interrupted", got)
	}
}

func TestRecoverSealsAnAbandonedRunOnlyAfterItsCommandEnds(t *testing.T) {
	spool := t.TempDir()
	partial := `{"complete":false,"results":[{"id":"agent-reload","code":0,"durationMs":5}]}`
	capture := historyCommand(t, spool, t.TempDir(), "capture", "native", "--",
		"sh", "-c", `printf '%s' "$1" >"$ATTN_TEST_RESULTS"; echo ready; read -r _; exec 3>&-`, "sh", partial)
	commandIn, stdin, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, commandOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	capture.Stdin, capture.Stdout = commandIn, commandOut
	if err := capture.Start(); err != nil {
		t.Fatal(err)
	}
	_ = commandIn.Close()
	_ = commandOut.Close()
	ready := bufio.NewReader(stdout)
	if line, err := ready.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("command said %q, %v", line, err)
	}
	if err := capture.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = capture.Wait()

	recover := func() string {
		out, err := historyCommand(t, spool, t.TempDir(), "recover").CombinedOutput()
		if err != nil {
			t.Fatalf("recover: %v\n%s", err, out)
		}
		return string(out)
	}
	if out := recover(); !strings.Contains(out, ": running") || len(sealedReceipts(t, spool)) != 0 {
		t.Fatalf("recover sealed a run whose command is still running:\n%s", out)
	}

	_ = stdin.Close()
	_, _ = io.Copy(io.Discard, ready)
	if out := recover(); !strings.Contains(out, ": sealed incomplete") {
		t.Fatalf("recover after the command ended said:\n%s", out)
	}
	receipt := onlyReceipt(t, spool)
	if receipt.Complete || receipt.Command.Exit != nil {
		t.Fatalf("recovered receipt complete=%v exit=%+v, want incomplete without an exit", receipt.Complete, receipt.Command.Exit)
	}
	if got := outcomeOf(receipt, "native agent-reload"); got != testhistory.Pass {
		t.Fatalf("recovered results = %+v, want the scenario that finished", receipt.Results)
	}
	sealed, _ := os.ReadFile(filepath.Join(spool, "receipts", receipt.InvocationID+".json"))
	if out := recover(); strings.TrimSpace(out) != "" {
		t.Fatalf("a second recover found more to do:\n%s", out)
	}
	if again, _ := os.ReadFile(filepath.Join(spool, "receipts", receipt.InvocationID+".json")); !bytes.Equal(again, sealed) {
		t.Fatal("a second recover changed the sealed receipt")
	}
}

func TestSummaryFailsARequestedUnitThatReportedNothing(t *testing.T) {
	module := fixtureModule(t)
	for _, tc := range []struct {
		units []string
		lines []string
		code  int
	}{
		{
			units: []string{"go ./good TestPasses", "go ./good TestSkips"},
			lines: []string{"PASS go ./good TestPasses ", "SKIP go ./good TestSkips "},
		},
		{
			units: []string{"go ./good TestPasses", "go ./good TestNoSuch", "go ./broken TestNeverBuilds"},
			lines: []string{
				"PASS go ./good TestPasses ",
				"MISSING go ./good TestNoSuch - did not report a result",
				"MISSING go ./broken TestNeverBuilds - its package did not build",
			},
			code: 1,
		},
	} {
		receipt := filepath.Join(t.TempDir(), "receipt.json")
		args := []string{"capture", "go", "--receipt-out", receipt}
		for _, unit := range tc.units {
			args = append(args, "--unit", unit)
		}
		args = append(args, "--", "-count=1", "-run", "TestPasses|TestSkips|TestNoSuch|TestNeverBuilds", "./good", "./broken")
		_ = historyCommand(t, t.TempDir(), module, args...).Run()

		out, err := historyCommand(t, t.TempDir(), module, "summary", receipt).Output()
		if got := exitCode(err); got != tc.code {
			t.Fatalf("%v: summary exited %d, want %d\n%s", tc.units, got, tc.code, out)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) != len(tc.lines) {
			t.Fatalf("%v: summary printed\n%s", tc.units, out)
		}
		for i, want := range tc.lines {
			if !strings.HasPrefix(lines[i], want) {
				t.Fatalf("%v: line %d = %q, want it to start with %q", tc.units, i, lines[i], want)
			}
		}
	}
}

func TestATerminationSentToCaptureReachesTheCommand(t *testing.T) {
	spool := t.TempDir()
	capture := historyCommand(t, spool, t.TempDir(), "capture", "native", "--", "sh", "-c", `trap 'exit 9' TERM; echo ready; read -r _`)
	commandIn, stdin, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stdout, commandOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	capture.Stdin, capture.Stdout = commandIn, commandOut
	if err := capture.Start(); err != nil {
		t.Fatal(err)
	}
	_ = commandIn.Close()
	_ = commandOut.Close()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("command said %q, %v", line, err)
	}
	if err := capture.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if got := exitCode(capture.Wait()); got != 9 {
		t.Fatalf("capture exited %d, want the command's 9", got)
	}
	receipt := onlyReceipt(t, spool)
	if receipt.Command.Interrupted != "SIGTERM" || receipt.Command.Exit == nil || receipt.Command.Exit.Code != 9 {
		t.Fatalf("receipt command = %+v, want interrupted by SIGTERM with exit 9", receipt.Command)
	}
}

func TestAnUnavailableSpoolStillHandsTheReceiptToItsCaller(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(t.TempDir(), "receipt.json")
	out, err := historyCommand(t, filepath.Join(blocked, "spool"), fixtureModule(t), "capture", "go",
		"--unit", "go ./good TestPasses", "--receipt-out", receipt, "--", "-count=1", "-run", "TestPasses", "./good").CombinedOutput()
	if got := exitCode(err); got != 0 {
		t.Fatalf("capture exited %d, want the passing run's 0\n%s", got, out)
	}
	if !strings.Contains(string(out), "test-history:") {
		t.Fatalf("capture did not warn that history kept nothing:\n%s", out)
	}
	summary, err := historyCommand(t, t.TempDir(), t.TempDir(), "summary", receipt).CombinedOutput()
	if exitCode(err) != 0 || !strings.HasPrefix(string(summary), "PASS go ./good TestPasses ") {
		t.Fatalf("summary of the handed-back receipt:\n%s", summary)
	}
}

func TestADirtyCheckoutIsRecordedAsDirtyFromAnySubdirectory(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(repo, "app", "new.test.ts"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fingerprint := func(dir string) string {
		spool := t.TempDir()
		if err := historyCommand(t, spool, dir, "capture", "native", "--", "true").Run(); err != nil {
			t.Fatal(err)
		}
		return onlyReceipt(t, spool).Revision.DirtyFingerprint
	}
	fromRoot, fromApp := fingerprint(repo), fingerprint(filepath.Join(repo, "app"))
	if fromRoot == "" || fromRoot == "unknown" || fromApp != fromRoot {
		t.Fatalf("dirty fingerprint from the root %q, from app/ %q; want the same fingerprint", fromRoot, fromApp)
	}

	unreadable := filepath.Join(repo, "app", "unreadable.txt")
	if err := os.WriteFile(unreadable, []byte("secret\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if got := fingerprint(filepath.Join(repo, "app")); got != "unknown" {
		t.Fatalf("a dirty checkout that cannot be fingerprinted recorded %q, want unknown", got)
	}
}
