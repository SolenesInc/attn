package outpostssh

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func (c *Client) InstallPath() string {
	if c.Instance == "" || c.Instance == "default" {
		return "~/.local/bin/attn"
	}
	return "~/.local/share/attn-outpost-instances/" + c.Instance + "/attn"
}
func (c *Client) binaryPath() string {
	return `"` + strings.Replace(c.InstallPath(), "~", "$attn_root", 1) + `"`
}

func (c *Client) command(ctx context.Context, script string) (*exec.Cmd, error) {
	if strings.ContainsAny(script+c.Instance, "'\n\r") {
		return nil, fmt.Errorf("remote script must contain no single quotes or newlines")
	}
	prefix := `attn_root="$HOME"; if [ -n "$ATTN_HARNESS_DATA_DIR" ] && [ -n "$ATTN_HARNESS_OUTPOST_INSTALL_ROOT" ]; then attn_root="$ATTN_HARNESS_OUTPOST_INSTALL_ROOT"; fi; `
	if c.Instance != "" && c.Instance != "default" {
		prefix += `export ATTN_INSTANCE="` + c.Instance + `"; `
	}
	args := append(c.options(true), "-o", "ControlMaster=no", "-T", "-a", "-x", c.Destination, "sh -c '"+prefix+script+"'")
	return exec.CommandContext(ctx, "ssh", args...), nil
}
func run(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		detail := stderr.String()
		if detail == "" {
			detail = err.Error()
		}
		return out, &Error{code, detail}
	}
	return out, nil
}
func (c *Client) Run(ctx context.Context, script string) ([]byte, error) {
	cmd, err := c.command(ctx, script)
	if err != nil {
		return nil, err
	}
	return run(ctx, cmd)
}
func (c *Client) Stream(ctx context.Context, script string, stdin io.Reader, stdout func(string)) error {
	cmd, err := c.command(ctx, script)
	if err != nil {
		return err
	}
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	scan := bufio.NewScanner(pipe)
	for scan.Scan() {
		if stdout != nil {
			stdout(scan.Text())
		}
	}
	scanErr := scan.Err()
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return &Error{cmd.ProcessState.ExitCode(), stderr.String()}
	}
	return scanErr
}

type pipeConn struct {
	destination    string
	reader, writer *os.File
	cmd            *exec.Cmd
	stderr         bytes.Buffer
	stderrReader   io.ReadCloser
	done           chan struct{}
	err            error
	once           sync.Once
}

func (c *Client) Bridge(ctx context.Context) (net.Conn, error) {
	cmd, err := c.command(ctx, "exec "+c.binaryPath()+" _bridge")
	if err != nil {
		return nil, err
	}
	reader, out, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	in, writer, err := os.Pipe()
	if err != nil {
		reader.Close()
		out.Close()
		return nil, err
	}
	p := &pipeConn{destination: c.Destination, reader: reader, writer: writer, cmd: cmd, done: make(chan struct{})}
	cmd.Stdin = in
	cmd.Stdout = out
	p.stderrReader, err = cmd.StderrPipe()
	if err != nil {
		in.Close()
		out.Close()
		reader.Close()
		writer.Close()
		return nil, err
	}
	err = cmd.Start()
	in.Close()
	out.Close()
	if err != nil {
		reader.Close()
		writer.Close()
		return nil, err
	}
	go func() { _, _ = io.Copy(&p.stderr, p.stderrReader); p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}
func (p *pipeConn) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	if err == io.EOF {
		<-p.done
		if p.err != nil {
			return n, &Error{p.cmd.ProcessState.ExitCode(), p.stderr.String()}
		}
	}
	return n, err
}
func (p *pipeConn) Write(b []byte) (int, error) { return p.writer.Write(b) }
func (p *pipeConn) Close() error {
	p.once.Do(func() { p.reader.Close(); p.writer.Close(); p.stderrReader.Close(); _ = p.cmd.Process.Kill(); <-p.done })
	return nil
}
func (p *pipeConn) CloseWrite() error { return p.writer.Close() }

type pipeAddr string

func (a pipeAddr) Network() string       { return "ssh" }
func (a pipeAddr) String() string        { return string(a) }
func (p *pipeConn) LocalAddr() net.Addr  { return pipeAddr("home") }
func (p *pipeConn) RemoteAddr() net.Addr { return pipeAddr(p.destination) }
func (p *pipeConn) SetDeadline(t time.Time) error {
	if err := p.reader.SetReadDeadline(t); err != nil {
		return err
	}
	return p.writer.SetWriteDeadline(t)
}
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return p.reader.SetReadDeadline(t) }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return p.writer.SetWriteDeadline(t) }
