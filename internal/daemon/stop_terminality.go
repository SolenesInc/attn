package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func runningWork(msg *protocol.StopMessage) []string {
	var running []string
	for _, work := range msg.RunningWork {
		if work = strings.TrimSpace(work); work != "" {
			running = append(running, work)
		}
	}
	return running
}

func hasRunningWork(msg *protocol.StopMessage) bool {
	return len(runningWork(msg)) > 0
}

func hasScheduledWork(msg *protocol.StopMessage) bool {
	return protocol.Deref(msg.ScheduledWork) > 0
}

func stopIsNonTerminal(msg *protocol.StopMessage, relaxBackgroundWork bool) bool {
	return !relaxBackgroundWork && hasRunningWork(msg)
}
