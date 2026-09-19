package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Operation string

const (
	OpMetadata Operation = "metadata"
	OpStatus   Operation = "status"
	OpDiff     Operation = "diff"
	OpWorktree Operation = "worktree"
	OpNetwork  Operation = "network"
	OpClone    Operation = "clone"
)

type commandRunner interface {
	run(context.Context, Operation, time.Duration, string, io.Reader, bool, map[string]string, ...string) ([]byte, error)
	runWithEnvironment(context.Context, Operation, time.Duration, string, []string, ...string) ([]byte, error)
}

type execCommandRunner struct{}

type Client struct {
	runner   commandRunner
	observer func(Operation)
}

func NewClient() *Client {
	return &Client{runner: execCommandRunner{}}
}

func (c *Client) WithCommandObserver(observer func(Operation)) *Client {
	if c == nil {
		return nil
	}
	return &Client{runner: c.runner, observer: observer}
}

var defaultClient = NewClient()

var (
	logMu               sync.RWMutex
	logf                func(format string, args ...interface{})
	slowGitLogThreshold = 2 * time.Second
	timeoutMu           sync.RWMutex
	timeoutByOp         = map[Operation]time.Duration{}
)

func SetLogFunc(fn func(format string, args ...interface{})) {
	logMu.Lock()
	defer logMu.Unlock()
	logf = fn
}

func setSlowLogThresholdForTesting(threshold time.Duration) func() {
	logMu.Lock()
	previous := slowGitLogThreshold
	slowGitLogThreshold = threshold
	logMu.Unlock()

	return func() {
		logMu.Lock()
		defer logMu.Unlock()
		slowGitLogThreshold = previous
	}
}

func defaultTimeout(op Operation) time.Duration {
	timeoutMu.RLock()
	if timeout, ok := timeoutByOp[op]; ok {
		timeoutMu.RUnlock()
		return timeout
	}
	timeoutMu.RUnlock()

	switch op {
	case OpStatus, OpMetadata:
		return 2 * time.Minute
	case OpDiff:
		return 10 * time.Minute
	case OpWorktree, OpNetwork:
		return 30 * time.Minute
	case OpClone:
		return 60 * time.Minute
	default:
		return 2 * time.Minute
	}
}

func setTimeoutForTesting(op Operation, timeout time.Duration) func() {
	timeoutMu.Lock()
	previous, hadPrevious := timeoutByOp[op]
	timeoutByOp[op] = timeout
	timeoutMu.Unlock()

	return func() {
		timeoutMu.Lock()
		defer timeoutMu.Unlock()
		if hadPrevious {
			timeoutByOp[op] = previous
			return
		}
		delete(timeoutByOp, op)
	}
}

func runGitOutput(op Operation, dir string, args ...string) ([]byte, error) {
	return defaultClient.Output(context.Background(), op, dir, args...)
}

func OutputContext(ctx context.Context, op Operation, dir string, args ...string) ([]byte, error) {
	return defaultClient.Output(ctx, op, dir, args...)
}

func Output(op Operation, dir string, args ...string) ([]byte, error) {
	return defaultClient.Output(context.Background(), op, dir, args...)
}

func OutputWithTimeout(op Operation, timeout time.Duration, dir string, args ...string) ([]byte, error) {
	return defaultClient.OutputWithTimeout(context.Background(), op, timeout, dir, args...)
}

func runGitCombinedWithHTTPAuthorization(op Operation, dir, authorizationURL, authorization string, args ...string) ([]byte, error) {
	return defaultClient.combinedWithHTTPAuthorization(context.Background(), op, dir, authorizationURL, authorization, args...)
}

func (c *Client) combinedWithHTTPAuthorization(ctx context.Context, op Operation, dir, authorizationURL, authorization string, args ...string) ([]byte, error) {
	var err error
	authorization, err = authorizationForGitURL(authorizationURL, authorization)
	if err != nil {
		return nil, err
	}
	return c.run(ctx, op, defaultTimeout(op), dir, nil, true, gitHTTPAuthorizationEnv(authorizationURL, authorization), args...)
}

func OutputWithStdin(op Operation, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	return defaultClient.OutputWithStdin(context.Background(), op, dir, stdin, args...)
}

func NoOutputContext(ctx context.Context, op Operation, dir string, args ...string) error {
	return defaultClient.NoOutput(ctx, op, dir, args...)
}

func (c *Client) Output(ctx context.Context, op Operation, dir string, args ...string) ([]byte, error) {
	return c.run(ctx, op, defaultTimeout(op), dir, nil, false, nil, args...)
}

func (c *Client) Combined(ctx context.Context, op Operation, dir string, args ...string) ([]byte, error) {
	return c.run(ctx, op, defaultTimeout(op), dir, nil, true, nil, args...)
}

func (c *Client) NoOutput(ctx context.Context, op Operation, dir string, args ...string) error {
	_, err := c.run(ctx, op, defaultTimeout(op), dir, nil, true, nil, args...)
	return err
}

func (c *Client) OutputWithStdin(ctx context.Context, op Operation, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	return c.run(ctx, op, defaultTimeout(op), dir, stdin, false, nil, args...)
}

func (c *Client) OutputWithTimeout(ctx context.Context, op Operation, timeout time.Duration, dir string, args ...string) ([]byte, error) {
	return c.run(ctx, op, timeout, dir, nil, false, nil, args...)
}

func (c *Client) run(ctx context.Context, op Operation, timeout time.Duration, dir string, stdin io.Reader, combined bool, env map[string]string, args ...string) ([]byte, error) {
	if c == nil || c.runner == nil {
		return nil, errors.New("git client has no command runner")
	}
	if c.observer != nil {
		c.observer(op)
	}
	return c.runner.run(ctx, op, timeout, dir, stdin, combined, env, args...)
}

func (execCommandRunner) run(parent context.Context, op Operation, timeout time.Duration, dir string, stdin io.Reader, combined bool, env map[string]string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = mergedCommandEnv(env)
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}

	started := time.Now()
	var out []byte
	var err error
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}
	duration := time.Since(started)

	logGitCommand(op, dir, args, duration, ctx.Err())

	if cause := context.Cause(parent); cause != nil {
		return out, cause
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("git %s timed out after %s: git %s", op, timeout, strings.Join(redactGitArgs(args), " "))
	}
	return out, err
}

func (execCommandRunner) runWithEnvironment(parent context.Context, op Operation, timeout time.Duration, dir string, environment []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	commandArgs := append([]string{"git"}, args...)
	cmd := exec.CommandContext(ctx, "/usr/bin/env", commandArgs...)
	cmd.Dir = dir
	if environment != nil {
		cmd.Env = append([]string{}, environment...)
	}

	started := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(started)
	logGitCommand(op, dir, args, duration, ctx.Err())

	if cause := context.Cause(parent); cause != nil {
		return out, cause
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("git %s timed out after %s: git %s", op, timeout, strings.Join(redactGitArgs(args), " "))
	}
	return out, err
}

func gitHTTPAuthorizationEnv(authorizationURL, authorization string) map[string]string {
	env := map[string]string{"GIT_TERMINAL_PROMPT": "0"}
	if authorization == "" {
		return env
	}
	count, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	parsed, _ := url.Parse(authorizationURL)
	scope := "https://" + parsed.Host + "/"
	env["GIT_CONFIG_COUNT"] = strconv.Itoa(count + 1)
	env[fmt.Sprintf("GIT_CONFIG_KEY_%d", count)] = "http." + scope + ".extraHeader"
	env[fmt.Sprintf("GIT_CONFIG_VALUE_%d", count)] = authorization
	return env
}

func mergedCommandEnv(overrides map[string]string) []string {
	values := make(map[string]string, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			values[entry[:index]] = entry[index+1:]
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func logGitCommand(op Operation, dir string, args []string, duration time.Duration, ctxErr error) {
	logMu.RLock()
	fn := logf
	threshold := slowGitLogThreshold
	logMu.RUnlock()
	if fn == nil {
		return
	}
	if duration < threshold && ctxErr == nil {
		return
	}
	status := "slow"
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		status = "timeout"
	}
	fn("git command %s: op=%s duration=%s dir=%s args=%q", status, op, duration.Round(time.Millisecond), dir, redactGitArgs(args))
}

func redactGitArgs(args []string) []string {
	redacted := make([]string, len(args))
	for i, arg := range args {
		redacted[i] = redactGitArg(arg)
	}
	return redacted
}

func redactGitArg(arg string) string {
	parsed, err := url.Parse(arg)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return arg
	}
	switch parsed.Scheme {
	case "http", "https", "ssh":
	default:
		return arg
	}
	if parsed.User != nil {
		parsed.User = url.User("REDACTED")
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "REDACTED"
	}
	if parsed.Fragment != "" {
		parsed.Fragment = "REDACTED"
	}
	return parsed.String()
}
