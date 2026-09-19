package daemon

import (
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func runningBackgroundTaskCount(msg *protocol.StopMessage) int {
	running := 0
	for _, task := range msg.BackgroundTasks {
		if strings.EqualFold(strings.TrimSpace(task.Status), "running") {
			running++
		}
	}
	return running
}

func describeBackgroundTasks(msg *protocol.StopMessage) string {
	parts := make([]string, 0, len(msg.BackgroundTasks))
	for _, task := range msg.BackgroundTasks {
		part := strings.TrimSpace(task.Type) + " " + strings.TrimSpace(task.Status)
		if name := strings.TrimSpace(protocol.Deref(task.Name)); name != "" {
			part += " " + strconv.Quote(name)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func hasActiveBackgroundTask(msg *protocol.StopMessage) bool {
	return runningBackgroundTaskCount(msg) > 0
}

func hasPendingSessionCron(msg *protocol.StopMessage) bool {
	return protocol.Deref(msg.PendingSessionCrons) > 0
}

func stopIsNonTerminal(msg *protocol.StopMessage, relaxBackgroundWork bool) bool {
	return !relaxBackgroundWork && hasActiveBackgroundTask(msg)
}
