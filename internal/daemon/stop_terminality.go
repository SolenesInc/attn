package daemon

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func runningWork(msg *protocol.StopMessage) []string {
	running := make([]string, 0, len(msg.BackgroundTasks))
	for _, task := range msg.BackgroundTasks {
		running = append(running, backgroundTaskLabel(task))
	}
	return running
}

func backgroundTaskLabel(task protocol.BackgroundTask) string {
	kind := strings.TrimSpace(protocol.Deref(task.Kind))
	name := strings.TrimSpace(protocol.Deref(task.Description))
	switch {
	case kind == "" && name == "":
		return "background task"
	case name == "":
		return kind
	case kind == "":
		return strconv.Quote(name)
	}
	return kind + " " + strconv.Quote(name)
}

func hasRunningWork(msg *protocol.StopMessage) bool {
	return len(msg.BackgroundTasks) > 0
}

func hasScheduledWork(msg *protocol.StopMessage) bool {
	return protocol.Deref(msg.ScheduledWork) > 0
}

func stopIsNonTerminal(msg *protocol.StopMessage, relaxBackgroundWork bool) bool {
	return !relaxBackgroundWork && hasRunningWork(msg)
}

func (d *Daemon) handleBackgroundTaskStarted(conn net.Conn, msg *protocol.BackgroundTaskStartedMessage) {
	sessionID := d.sessionInTerminal(msg.ID)
	if task := strings.TrimSpace(msg.TaskID); task != "" {
		d.monitorInTerminal(msg.ID, sessionID).RecordBackgroundLaunch(task, time.Now())
	}
	d.sendOK(conn)
}
