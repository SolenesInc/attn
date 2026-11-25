// Package script runs a model's JavaScript in a child process. The script is
// hostile: it can send any call the pipe can carry. Run refuses a call to a
// command outside Job.Functions and a call whose arguments are not one JSON
// object, and nothing more. The host function must itself check, for every
// call, that the command's effect is one the tool may have and that the caller
// holds its grant.
package script

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const Command = "_run-script"

type Job struct {
	Source    string   `json:"source"`
	Functions []string `json:"functions"`
	Limits    Limits   `json:"limits"`
}

type Limits struct {
	Memory  int           `json:"memory"`
	Stack   int           `json:"stack"`
	Compute time.Duration `json:"compute"`
}

var DefaultLimits = Limits{
	Memory:  64 << 20,        // Parsing a 211 KB result leaves the child at 28 to 31 MB; an endless allocation ended at 90 to 99 MB.
	Stack:   512 << 10,       // 494 nested calls fit on darwin/arm64 and 816 on linux/amd64.
	Compute: 2 * time.Second, // 1,000 commands took 0.04 to 0.08 s of CPU, and 192 reads of a 211 KB result 0.5 to 1.3 s.
}

const (
	ResultText = 100_000 // Claude Code 2.1.330 saves a longer MCP result to a file, so what a script logs or returns past this never reaches its model.
	failedText = 20_000  // The daemon's result cuts an error at 25,000; the longest command's rules, which a refusal carries, take 2,900.
	Arguments  = 8 << 20 // All calls of one script. Of 1,000 seeds the longest body is 113 KB and the largest plot's bodies together 430 KB; a seed's body may be 1 MB.

	fromChild = Arguments + 2<<20 // Beside its calls a run sends its logs, returned text and error, cut to at most 1.4 MB at 6 bytes a character.
)

type Answer struct {
	JSON    string   `json:"json,omitempty"`
	Text    string   `json:"text,omitempty"`
	Refusal *Refusal `json:"refusal,omitempty"`
}

type Refusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
	Command string `json:"command,omitempty"`
}

type Output struct {
	Returned string   `json:"returned,omitempty"`
	Has      bool     `json:"has,omitempty"`
	Logs     []string `json:"logs,omitempty"`
	Dropped  int      `json:"dropped,omitempty"` // characters of Logs and Returned past ResultText
	Failed   string   `json:"failed,omitempty"`
	Line     int      `json:"line,omitempty"`
}

type message struct {
	Job    *Job    `json:"job,omitempty"`
	Call   *call   `json:"call,omitempty"`
	Answer *Answer `json:"answer,omitempty"`
	Done   *Output `json:"done,omitempty"`
}

type call struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

func newEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

func encodeLine(m message) []byte {
	var line bytes.Buffer
	_ = newEncoder(&line).Encode(m)
	return line.Bytes()
}

func Run(ctx context.Context, job Job, host func(name string, args json.RawMessage) Answer) Output {
	out, err := runChild(ctx, job, host)
	switch {
	case err == nil:
		return out
	case ctx.Err() != nil:
		return Output{Failed: fmt.Sprintf("the script was stopped before it ended: %v", context.Cause(ctx))}
	default:
		return Output{Failed: err.Error()}
	}
}

func runChild(ctx context.Context, job Job, host func(name string, args json.RawMessage) Answer) (Output, error) {
	executable, err := os.Executable()
	if err != nil {
		return Output{}, fmt.Errorf("the script's runner did not start: %w", err)
	}
	cmd := exec.CommandContext(ctx, executable, Command)
	cmd.Dir = "/"
	cmd.Env = []string{}
	var crash firstLine
	cmd.Stderr = &crash
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Output{}, fmt.Errorf("the script's runner did not start: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Output{}, fmt.Errorf("the script's runner did not start: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return Output{}, fmt.Errorf("the script's runner did not start: %w", err)
	}
	out, err := converse(job, host, stdin, stdout)
	_ = stdin.Close()
	if err != nil && !errors.Is(err, errRunnerGone) {
		_ = cmd.Process.Kill()
	}
	exit := cmd.Wait()
	if errors.Is(err, errRunnerGone) {
		return Output{}, fmt.Errorf("the script's runner died before the script ended: %v%s", exit, crash.suffix())
	}
	return out, err
}

var errRunnerGone = errors.New("the script's runner closed its pipe")

func converse(job Job, host func(name string, args json.RawMessage) Answer, stdin io.Writer, stdout io.Reader) (Output, error) {
	enc := newEncoder(stdin)
	if err := enc.Encode(message{Job: &job}); err != nil {
		return Output{}, errRunnerGone
	}
	pipe := &io.LimitedReader{R: stdout, N: fromChild}
	dec := json.NewDecoder(pipe)
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			switch {
			case pipe.N == 0:
				return Output{}, fmt.Errorf("the script stopped: its runner sent more than %s", size(fromChild))
			case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
				return Output{}, errRunnerGone
			default:
				return Output{}, fmt.Errorf("the script's runner sent a broken message: %w", err)
			}
		}
		switch {
		case m.Done != nil:
			return *m.Done, nil
		case m.Call != nil:
			answer := answerCall(job, *m.Call, host)
			if err := enc.Encode(message{Answer: &answer}); err != nil {
				return Output{}, errRunnerGone
			}
		default:
			return Output{}, errors.New("the script's runner sent a message that is neither a call nor done")
		}
	}
}

func answerCall(job Job, c call, host func(name string, args json.RawMessage) Answer) Answer {
	switch {
	case !slices.Contains(job.Functions, c.Name):
		return Answer{Refusal: &Refusal{Code: "no_such_command", Message: "this script may not call that command"}}
	case !bytes.HasPrefix(bytes.TrimSpace(c.Args), []byte("{")):
		return Answer{Refusal: &Refusal{Code: "usage", Message: c.Name + " takes one object of named arguments", Command: c.Name}}
	}
	return host(c.Name, c.Args)
}

type firstLine struct{ kept []byte }

const firstLineKept = 4096 // a Go crash names its signal in the first 100 bytes of stderr

func (l *firstLine) Write(p []byte) (int, error) {
	l.kept = append(l.kept, p[:min(len(p), firstLineKept-len(l.kept))]...)
	return len(p), nil
}

func (l *firstLine) suffix() string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(l.kept)), "\n")
	if line == "" {
		return ""
	}
	return ": " + line
}
