package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

const usage = `usage: wirecount <host> <port>

An ssh ProxyCommand that counts the bytes on the wire. It appends one JSON line
{pid, host, t, up, down, ev} with cumulative counts to $WIRECOUNT_LOG every
250 ms while they change, writes its pid to $WIRECOUNT_PIDS/<pid>, and closes the
connection on SIGUSR1 to drop the link. With $WIRECOUNT_JUMP set it reaches
<host>:<port> through ssh -W on that jump host, as ProxyJump would.

Put an ssh on the measured daemon's PATH that adds the ProxyCommand:

  #!/bin/sh
  exec /usr/bin/ssh -o "ProxyCommand=/path/to/wirecount %h %p" "$@"
`

func main() {
	if len(os.Args) != 3 {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}
	conn, err := dial(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "wirecount:", err)
		os.Exit(1)
	}
	var up, down atomic.Int64
	logf, _ := os.OpenFile(os.Getenv("WIRECOUNT_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if dir := os.Getenv("WIRECOUNT_PIDS"); dir != "" {
		os.WriteFile(dir+"/"+itoa(os.Getpid()), []byte(os.Args[1]), 0o644)
	}
	emit := func(ev string) {
		b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "host": os.Args[1], "t": float64(time.Now().UnixNano()) / 1e9, "up": up.Load(), "down": down.Load(), "ev": ev})
		logf.Write(append(b, '\n'))
	}
	emit("open")
	done := make(chan struct{}, 2)
	go func() { copyCount(conn, os.Stdin, &up); done <- struct{}{} }()
	go func() { copyCount(os.Stdout, conn, &down); done <- struct{}{} }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1, syscall.SIGTERM, syscall.SIGHUP)
	tick := time.NewTicker(250 * time.Millisecond)
	var lu, ld int64 = -1, -1
	for {
		select {
		case <-tick.C:
			if u, d := up.Load(), down.Load(); u != lu || d != ld {
				lu, ld = u, d
				emit("tick")
			}
		case s := <-sig:
			if s == syscall.SIGUSR1 {
				emit("cut")
			} else {
				emit("close")
			}
			if dir := os.Getenv("WIRECOUNT_PIDS"); dir != "" {
				os.Remove(dir + "/" + itoa(os.Getpid()))
			}
			conn.Close()
			os.Exit(0)
		case <-done:
			emit("close")
			conn.Close()
			if dir := os.Getenv("WIRECOUNT_PIDS"); dir != "" {
				os.Remove(dir + "/" + itoa(os.Getpid()))
			}
			os.Exit(0)
		}
	}
}

func copyCount(dst io.Writer, src io.Reader, n *atomic.Int64) (int64, error) {
	buf := make([]byte, 64<<10)
	var total int64
	for {
		r, err := src.Read(buf)
		if r > 0 {
			if _, werr := dst.Write(buf[:r]); werr != nil {
				return total, werr
			}
			total += int64(r)
			n.Add(int64(r))
		}
		if err != nil {
			return total, err
		}
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func dial(host, port string) (io.ReadWriteCloser, error) {
	jump := os.Getenv("WIRECOUNT_JUMP")
	if jump == "" {
		return net.Dial("tcp", net.JoinHostPort(host, port))
	}
	cmd := exec.Command("/usr/bin/ssh", "-W", net.JoinHostPort(host, port), jump)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return jumpConn{out, in, cmd}, nil
}

type jumpConn struct {
	io.Reader
	io.WriteCloser
	cmd *exec.Cmd
}

func (j jumpConn) Close() error {
	j.WriteCloser.Close()
	j.cmd.Process.Kill()
	return nil
}
