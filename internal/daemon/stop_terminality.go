package daemon

import (
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func runningBackgroundTasks(msg *protocol.StopMessage) []string {
	var running []string
	for _, task := range msg.BackgroundTasks {
		if strings.EqualFold(strings.TrimSpace(task.Status), "running") {
			running = append(running, describeBackgroundTask(task.Type, task))
		}
	}
	return running
}

func describeBackgroundTasks(msg *protocol.StopMessage) string {
	parts := make([]string, 0, len(msg.BackgroundTasks))
	for _, task := range msg.BackgroundTasks {
		parts = append(parts, describeBackgroundTask(strings.TrimSpace(task.Type)+" "+strings.TrimSpace(task.Status), task))
	}
	return strings.Join(parts, ", ")
}

func describeBackgroundTask(label string, task protocol.StopBackgroundTask) string {
	label = strings.TrimSpace(label)
	if name := strings.TrimSpace(protocol.Deref(task.Name)); name != "" {
		label += " " + strconv.Quote(name)
	}
	return label
}

func hasActiveBackgroundTask(msg *protocol.StopMessage) bool {
	return len(runningBackgroundTasks(msg)) > 0
}

func hasPendingSessionCron(msg *protocol.StopMessage) bool {
	return protocol.Deref(msg.PendingSessionCrons) > 0
}

func stopIsNonTerminal(msg *protocol.StopMessage, relaxBackgroundWork bool) bool {
	return !relaxBackgroundWork && hasActiveBackgroundTask(msg)
}
