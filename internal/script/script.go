package script

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	lines := bufio.NewScanner(stdout)
	lines.Buffer(nil, job.Limits.Memory)
	for lines.Scan() {
		var m message
		if err := json.Unmarshal(lines.Bytes(), &m); err != nil {
			return Output{}, fmt.Errorf("the script's runner sent a broken message: %w", err)
		}
		switch {
		case m.Done != nil:
			return *m.Done, nil
		case m.Call != nil:
			answer := host(m.Call.Name, m.Call.Args)
			if err := enc.Encode(message{Answer: &answer}); err != nil {
				return Output{}, errRunnerGone
			}
		default:
			return Output{}, errors.New("the script's runner sent a message that is neither a call nor done")
		}
	}
	if errors.Is(lines.Err(), bufio.ErrTooLong) {
		return Output{}, fmt.Errorf("the script sent more than its memory limit of %s in one message", size(job.Limits.Memory))
	}
	return Output{}, errRunnerGone
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
