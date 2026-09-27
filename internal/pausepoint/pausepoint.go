package pausepoint

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

const (
	EnvPoints = "ATTN_PAUSE_AT"
	EnvSocket = "ATTN_PAUSE_SOCK"
	Release   = "go"
)

const (
	PtyAttachSnapshot  = "pty-attach-snapshot"
	PtyOutputSequenced = "pty-output-sequenced"
	PtyOutputHeld      = "pty-output-held"
	PtyStreamRead      = "pty-stream-read"
	PtySubscriberDrop  = "pty-subscriber-drop"
	BusAnnounce        = "bus-announce"

	DaemonStartupRecovery     = "daemon-startup-recovery"
	SessionInputPasteGap      = "session-input-paste-gap"
	SessionInputLaneContended = "session-input-lane-contended"
)

type armed struct {
	socket string
	mu     sync.Mutex
	points map[string]bool
}

var load = sync.OnceValue(func() *armed {
	names, socket := os.Getenv(EnvPoints), os.Getenv(EnvSocket)
	if names == "" || socket == "" {
		return nil
	}
	a := &armed{socket: socket, points: map[string]bool{}}
	for _, name := range strings.Split(names, ",") {
		if name = strings.TrimSpace(name); name != "" {
			a.points[name] = true
		}
	}
	return a
})

func At(name string) {
	if a := load(); a != nil {
		a.pause(name)
	}
}

func (a *armed) pause(name string) {
	a.mu.Lock()
	armed := a.points[name]
	// Once per process: a worker that re-execs itself for an upgrade arms again.
	delete(a.points, name)
	a.mu.Unlock()
	if !armed {
		return
	}
	conn, err := net.Dial("unix", a.socket)
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "%s %d\n", name, os.Getpid()); err != nil {
		return
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == Release {
			return
		}
	}
}
