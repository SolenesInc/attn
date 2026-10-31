package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

type captureRequest struct {
	runner     testhistory.Runner
	units      []string
	shard      string
	receiptOut string
	command    []string
}

func parseCapture(args []string) (captureRequest, error) {
	if len(args) == 0 {
		return captureRequest{}, errors.New("capture needs a runner")
	}
	runner, err := testhistory.ParseRunner(args[0])
	if err != nil {
		return captureRequest{}, err
	}
	request := captureRequest{runner: runner}
	args = args[1:]
	for len(args) > 0 && args[0] != "--" {
		if len(args) < 2 {
			return captureRequest{}, fmt.Errorf("%s needs a value", args[0])
		}
		switch args[0] {
		case "--unit":
			request.units = append(request.units, args[1])
		case "--shard":
			request.shard = args[1]
		case "--receipt-out":
			request.receiptOut = args[1]
		default:
			return captureRequest{}, fmt.Errorf("unknown capture option %s", args[0])
		}
		args = args[2:]
	}
	if len(args) == 0 {
		return captureRequest{}, errors.New("capture needs -- before the command")
	}
	request.command = args[1:]
	if runner == testhistory.RunnerGo {
		request.command = append([]string{"go", "test", "-json"}, request.command...)
	}
	if len(request.command) == 0 {
		return captureRequest{}, errors.New("capture needs a command after --")
	}
	if request.shard == "" {
		request.shard = shardArgument(request.command)
	}
	return request, nil
}

func shardArgument(command []string) string {
	for i, arg := range command {
		if value, ok := strings.CutPrefix(arg, "--shard="); ok {
			return value
		}
		if arg == "--shard" && i+1 < len(command) {
			return command[i+1]
		}
	}
	return ""
}

func capture(args []string) int {
	request, err := parseCapture(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test-history: %v\n%s", err, usage)
		return 2
	}
	started := time.Now().UTC()
	receipt := describe(request, started)

	working, discard := begin(receipt, goModule(request.runner))
	defer discard()

	ending, runErr := run(request, working)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "test-history: %v\n", runErr)
	}
	if working == nil {
		return exitStatus(ending)
	}
	sealed, err := working.Seal(ending)
	if err != nil {
		warn("receipt %s: %v", receipt.InvocationID, err)
	}
	if request.receiptOut != "" && sealed.InvocationID != "" {
		if err := writeJSON(request.receiptOut, sealed); err != nil {
			warn("writing %s: %v", request.receiptOut, err)
		}
	}
	return exitStatus(ending)
}

func begin(receipt testhistory.Receipt, goModule string) (*testhistory.Working, func()) {
	dir, err := spoolDir()
	if err == nil {
		var working *testhistory.Working
		if working, err = (testhistory.Spool{Dir: dir}).Begin(receipt, goModule); err == nil {
			return working, func() {}
		}
	}
	warn("test history will not keep this run: %v", err)
	scratch, scratchErr := os.MkdirTemp("", "attn-test-history-")
	if scratchErr != nil {
		warn("not recording a receipt: %v", scratchErr)
		return nil, func() {}
	}
	discard := func() { _ = os.RemoveAll(scratch) }
	working, scratchErr := testhistory.Spool{Dir: scratch}.Begin(receipt, goModule)
	if scratchErr != nil {
		warn("not recording a receipt: %v", scratchErr)
		discard()
		return nil, func() {}
	}
	return working, discard
}

func exitStatus(ending testhistory.Ending) int {
	if ending.Exit == nil {
		return 127
	}
	return ending.Exit.Code
}

func run(request captureRequest, working *testhistory.Working) (testhistory.Ending, error) {
	cmd := exec.Command(request.command[0], request.command[1:]...)
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	var goEvents io.ReadCloser
	var results *os.File
	if working != nil {
		cmd.ExtraFiles = []*os.File{working.Lock()}
		cmd.Env = append(os.Environ(), "ATTN_TEST_RESULTS="+working.ResultsPath)
	}
	if request.runner == testhistory.RunnerGo {
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return testhistory.Ending{FinishedAt: time.Now()}, err
		}
		goEvents = pipe
		if working != nil {
			if results, err = os.OpenFile(working.ResultsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err != nil {
				return testhistory.Ending{FinishedAt: time.Now()}, err
			}
			defer func() { _ = results.Close() }()
		}
	} else {
		cmd.Stdout = os.Stdout
	}

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return testhistory.Ending{FinishedAt: time.Now()}, err
	}
	exited := make(chan struct{})
	received := make(chan string, 1)
	go relaySignals(signals, cmd.Process, received, exited)
	if goEvents != nil {
		renderGoEvents(goEvents, results, request.command)
	}
	waitErr := cmd.Wait()
	close(exited)
	ending := testhistory.Ending{FinishedAt: time.Now(), Exit: exitOf(cmd.ProcessState)}
	select {
	case name := <-received:
		ending.Interrupted = name
	default:
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return ending, waitErr
	}
	return ending, nil
}

func relaySignals(signals <-chan os.Signal, command *os.Process, received chan<- string, exited <-chan struct{}) {
	for {
		select {
		case <-exited:
			return
		case sig := <-signals:
			select {
			case received <- signalName(sig.(syscall.Signal)):
			default:
			}
			if sig != syscall.SIGINT {
				_ = command.Signal(sig)
			}
		}
	}
}

func renderGoEvents(stream io.Reader, results *os.File, command []string) {
	renderer := newGoRenderer(os.Stdout, os.Stderr, command)
	reader := bufio.NewReaderSize(stream, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if results != nil {
				_, _ = results.Write(line)
			}
			renderer.line(line)
		}
		if err != nil {
			break
		}
	}
	renderer.finish()
}

func exitOf(state *os.ProcessState) *testhistory.Exit {
	if state == nil {
		return nil
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return &testhistory.Exit{Code: 128 + int(status.Signal()), Signal: signalName(status.Signal())}
	}
	return &testhistory.Exit{Code: state.ExitCode()}
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGKILL:
		return "SIGKILL"
	default:
		return fmt.Sprintf("signal %d", int(sig))
	}
}

func describe(request captureRequest, started time.Time) testhistory.Receipt {
	receipt := testhistory.Receipt{
		Schema:       testhistory.Schema,
		Repo:         repository(),
		InvocationID: invocationID(started),
		Runner:       request.runner,
		Source:       source(request.shard),
		Revision:     revision(),
		Lane:         testhistory.Lane{OS: runtime.GOOS, Arch: runtime.GOARCH, Race: request.runner == testhistory.RunnerGo && raceRequested(request.command)},
		Diagnostics:  testhistory.Diagnostics{MachineClass: machineClass(), ToolVersions: toolVersions(request.runner)},
		Command:      testhistory.Command{Argv: request.command, RequestedUnits: request.units, StartedAt: started},
	}
	return receipt
}

func invocationID(started time.Time) string {
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	return started.Format("20060102T150405Z") + "-" + hex.EncodeToString(random)
}

func source(shard string) testhistory.Source {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return testhistory.Source{Kind: "local", Shard: shard}
	}
	return testhistory.Source{
		Kind:    "ci",
		RunID:   os.Getenv("GITHUB_RUN_ID"),
		Attempt: os.Getenv("GITHUB_RUN_ATTEMPT"),
		Job:     os.Getenv("GITHUB_JOB"),
		Shard:   shard,
	}
}

func raceRequested(command []string) bool {
	for _, arg := range command {
		if arg == "-race" || arg == "-race=true" || arg == "--race" {
			return true
		}
	}
	return false
}

func toolVersions(runner testhistory.Runner) map[string]string {
	if runner == testhistory.RunnerGo {
		return map[string]string{"go": runtime.Version()}
	}
	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		return nil
	}
	return map[string]string{"node": strings.TrimSpace(string(out))}
}

func goModule(runner testhistory.Runner) string {
	if runner != testhistory.RunnerGo {
		return ""
	}
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "test-history: "+format+"\n", args...)
}
