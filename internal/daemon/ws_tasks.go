package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

func taskToProtocol(t *jobs.Job) protocol.Task {
	pt := protocol.Task{
		ID:            t.ID,
		Kind:          t.Kind,
		Subject:       jobSubject(t),
		State:         string(t.State),
		Attempts:      t.Attempts,
		NextAttemptAt: t.ScheduledAt.UTC().Format(time.RFC3339),
		CreatedAt:     t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.LastError != "" {
		pt.LastError = protocol.Ptr(t.LastError)
	}
	if t.LastDiagnostic != "" {
		pt.LastDiagnostic = protocol.Ptr(t.LastDiagnostic)
	}
	return pt
}

func tasksToProtocol(ts []*jobs.Job) []protocol.Task {
	out := make([]protocol.Task, 0, len(ts))
	for _, t := range ts {
		if t == nil {
			continue
		}
		out = append(out, taskToProtocol(t))
	}
	return out
}

func (d *Daemon) sendTaskListWSResult(client *wsClient, requestID string) {
	runner := d.jobQueueRef()
	if runner == nil {
		d.sendToClient(client, protocol.TaskListResultMessage{
			Event:     protocol.EventTaskListResult,
			RequestID: requestID,
			Success:   true,
		})
		return
	}
	list, err := runner.List()
	msg := protocol.TaskListResultMessage{
		Event:     protocol.EventTaskListResult,
		RequestID: requestID,
		Success:   err == nil,
		Tasks:     tasksToProtocol(list),
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendTaskRetryWSResult(client *wsClient, requestID, taskID string) {
	runner := d.jobQueueRef()
	if runner == nil {
		d.sendToClient(client, protocol.TaskRetryResultMessage{
			Event:     protocol.EventTaskRetryResult,
			RequestID: requestID,
			Success:   false,
			Error:     protocol.Ptr("task runner unavailable"),
		})
		return
	}
	task, err := runner.Retry(taskID)
	msg := protocol.TaskRetryResultMessage{
		Event:     protocol.EventTaskRetryResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	} else if task != nil {
		pt := taskToProtocol(task)
		msg.Task = &pt
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) projectTasksChanged() {
	d.projectSnapshot(snapshotTasks, func() {
		d.broadcastMessage(protocol.TasksChangedMessage{
			Event: protocol.EventTasksChanged,
		})
	})
}
