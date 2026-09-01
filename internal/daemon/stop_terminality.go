package daemon

import (
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// Status comparison is case-insensitive: the status string is the agent harness's, not ours.
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

// Presence-only: session_crons carries no per-item status, and a fired or deleted cron leaves the list entirely.
func hasPendingSessionCron(msg *protocol.StopMessage) bool {
	return protocol.Deref(msg.PendingSessionCrons) > 0
}

// A parked cron is deliberately NOT non-terminal: the transcript is flushed and the wakeup may be hours away, so treating it as a yield left cron-parked sessions unclassified.
func stopIsNonTerminal(msg *protocol.StopMessage, relaxBackgroundWork bool) bool {
	return !relaxBackgroundWork && hasActiveBackgroundTask(msg)
}
