package testworld

import (
	"context"
	"io"

	"github.com/creack/pty"
)

func (s *Stack) LaunchInTerminal(inv Invocation) *Running {
	s.T.Helper()
	r := &Running{t: s.T, args: inv.Args, grew: make(chan struct{}), done: make(chan struct{})}
	cmd := s.command(context.Background(), inv)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		s.T.Fatalf("start attn %q on a terminal: %v", inv.Args, err)
	}
	r.process = cmd.Process
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(runningStream{r, &r.stdout}, tty)
		close(copied)
	}()
	go func() {
		err := cmd.Wait()
		tty.Close()
		<-copied
		r.mu.Lock()
		r.code = exitCode(s.T, inv, err)
		r.mu.Unlock()
		close(r.done)
	}()
	s.T.Cleanup(func() {
		select {
		case <-r.done:
		default:
			r.interrupt()
		}
	})
	return r
}
