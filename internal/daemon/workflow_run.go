package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func workflowRunProtoToRow(run *protocol.WorkflowRun) *store.WorkflowRunRow {
	if run == nil {
		return nil
	}
	return &store.WorkflowRunRow{
		RunID:       run.RunID,
		ScriptPath:  run.ScriptPath,
		ScriptHash:  run.ScriptHash,
		ArgsJSON:    run.ArgsJson,
		SessionID:   run.SessionID,
		WorkspaceID: run.WorkspaceID,
		Status:      string(run.Status),
		Phase:       run.Phase,
		Harness:     run.Harness,
		ResultJSON:  run.ResultJson,
		LastError:   run.LastError,
		Resumable:   run.Resumable,
		CreatedAt:   run.CreatedAt,
		UpdatedAt:   run.UpdatedAt,
		CompletedAt: run.CompletedAt,
	}
}

func workflowRunRowToProto(row *store.WorkflowRunRow) *protocol.WorkflowRun {
	if row == nil {
		return nil
	}
	return &protocol.WorkflowRun{
		RunID:       row.RunID,
		ScriptPath:  row.ScriptPath,
		ScriptHash:  row.ScriptHash,
		ArgsJson:    row.ArgsJSON,
		SessionID:   row.SessionID,
		WorkspaceID: row.WorkspaceID,
		Status:      protocol.WorkflowRunStatus(row.Status),
		Phase:       row.Phase,
		Harness:     row.Harness,
		ResultJson:  row.ResultJSON,
		LastError:   row.LastError,
		Resumable:   row.Resumable,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CompletedAt: row.CompletedAt,
	}
}

func workflowCallProtoToRow(call *protocol.WorkflowAgentCall) *store.WorkflowAgentCallRow {
	if call == nil {
		return nil
	}
	return &store.WorkflowAgentCallRow{
		RunID:           call.RunID,
		Ordinal:         call.Ordinal,
		Label:           call.Label,
		Phase:           call.Phase,
		PromptHash:      call.PromptHash,
		SchemaHash:      call.SchemaHash,
		ResolvedModel:   call.ResolvedModel,
		ResolvedHarness: call.ResolvedHarness,
		AgentType:       call.AgentType,
		ResultJSON:      call.ResultJson,
		Status:          string(call.Status),
		Error:           call.Error,
		ResultPath:      call.ResultPath,
		StartedAt:       call.StartedAt,
		CompletedAt:     call.CompletedAt,
	}
}

func workflowCallRowToProto(row *store.WorkflowAgentCallRow) protocol.WorkflowAgentCall {
	if row == nil {
		return protocol.WorkflowAgentCall{}
	}
	return protocol.WorkflowAgentCall{
		RunID:           row.RunID,
		Ordinal:         row.Ordinal,
		Label:           row.Label,
		Phase:           row.Phase,
		PromptHash:      row.PromptHash,
		SchemaHash:      row.SchemaHash,
		ResolvedModel:   row.ResolvedModel,
		ResolvedHarness: row.ResolvedHarness,
		AgentType:       row.AgentType,
		ResultJson:      row.ResultJSON,
		Status:          protocol.WorkflowAgentCallStatus(row.Status),
		Error:           row.Error,
		ResultPath:      row.ResultPath,
		StartedAt:       row.StartedAt,
		CompletedAt:     row.CompletedAt,
	}
}

func (d *Daemon) getWorkflowRunHydrated(runID string) (*protocol.WorkflowRun, error) {
	row, err := d.store.GetWorkflowRun(runID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	run := workflowRunRowToProto(row)

	calls, err := d.store.ListWorkflowAgentCalls(runID)
	if err != nil {
		return nil, err
	}
	if len(calls) > 0 {
		run.AgentCalls = make([]protocol.WorkflowAgentCall, 0, len(calls))
		for _, call := range calls {
			run.AgentCalls = append(run.AgentCalls, workflowCallRowToProto(call))
		}
	}
	return run, nil
}

func (d *Daemon) listWorkflowRunsHydrated(sessionID string) ([]*protocol.WorkflowRun, error) {
	rows, err := d.store.ListWorkflowRuns(sessionID)
	if err != nil {
		return nil, err
	}
	runs := make([]*protocol.WorkflowRun, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, workflowRunRowToProto(row))
	}
	return runs, nil
}

func (d *Daemon) applyWorkflowRunUpsert(run *protocol.WorkflowRun) (*protocol.WorkflowRun, error) {
	if run == nil {
		return nil, nil
	}
	if err := d.store.UpsertWorkflowRun(workflowRunProtoToRow(run)); err != nil {
		return nil, err
	}
	for i := range run.AgentCalls {
		call := run.AgentCalls[i]
		if call.RunID == "" {
			call.RunID = run.RunID
		}
		if err := d.store.UpsertWorkflowAgentCall(workflowCallProtoToRow(&call)); err != nil {
			return nil, err
		}
	}
	hydrated, err := d.getWorkflowRunHydrated(run.RunID)
	if err != nil {
		return nil, err
	}
	if isTerminalWorkflowRunStatus(run.Status) {
		d.unregisterWorkflowEngine(run.RunID)
	}
	d.markWorkflowRunDirty(run.RunID)
	return hydrated, nil
}

func isTerminalWorkflowRunStatus(status protocol.WorkflowRunStatus) bool {
	switch status {
	case protocol.WorkflowRunStatusCompleted,
		protocol.WorkflowRunStatusFailed,
		protocol.WorkflowRunStatusCanceled:
		return true
	default:
		return false
	}
}

func (d *Daemon) applyWorkflowCallUpsert(runID string, call *protocol.WorkflowAgentCall) (*protocol.WorkflowRun, error) {
	if call == nil {
		return d.getWorkflowRunHydrated(runID)
	}
	row := workflowCallProtoToRow(call)
	if row.RunID == "" {
		row.RunID = runID
	}
	if err := d.store.UpsertWorkflowAgentCall(row); err != nil {
		return nil, err
	}
	hydrated, err := d.getWorkflowRunHydrated(runID)
	if err != nil {
		return nil, err
	}
	d.markWorkflowRunDirty(runID)
	return hydrated, nil
}

func (d *Daemon) cancelWorkflowRun(runID string) (*protocol.WorkflowRun, bool, error) {
	row, err := d.store.GetWorkflowRun(runID)
	if err != nil {
		return nil, false, err
	}
	if row == nil {
		return nil, false, nil
	}

	now := string(protocol.TimestampNow())
	row.Status = string(protocol.WorkflowRunStatusCanceled)
	row.UpdatedAt = now
	row.CompletedAt = protocol.Ptr(now)
	if err := d.store.UpsertWorkflowRun(row); err != nil {
		return nil, false, err
	}

	relayed := d.relayWorkflowCancel(runID)
	d.unregisterWorkflowEngine(runID)

	hydrated, err := d.getWorkflowRunHydrated(runID)
	if err != nil {
		return nil, relayed, err
	}
	d.markWorkflowRunDirty(runID)
	return hydrated, relayed, nil
}

func (d *Daemon) sendWorkflowActionResult(conn net.Conn, action string, run *protocol.WorkflowRun, runs []*protocol.WorkflowRun, runID string, err error) {
	result := buildWorkflowActionResult(action, run, runs, runID, err)
	_ = json.NewEncoder(conn).Encode(result)
}

func buildWorkflowActionResult(action string, run *protocol.WorkflowRun, runs []*protocol.WorkflowRun, runID string, err error) *protocol.WorkflowActionResultMessage {
	result := &protocol.WorkflowActionResultMessage{
		Event:   protocol.EventWorkflowActionResult,
		Action:  action,
		Success: err == nil,
	}
	if strings.TrimSpace(runID) != "" {
		result.RunID = protocol.Ptr(runID)
	}
	if run != nil {
		result.Run = run
	}
	if len(runs) > 0 {
		values := make([]protocol.WorkflowRun, 0, len(runs))
		for _, r := range runs {
			if r != nil {
				values = append(values, *r)
			}
		}
		result.Runs = values
	}
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return result
}

func (d *Daemon) guardWorkflowRunStart(run *protocol.WorkflowRun) error {
	if run == nil || run.Status != protocol.WorkflowRunStatusRunning {
		return nil
	}
	if parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)) {
		return nil
	}
	return fmt.Errorf("workflows are disabled; enable Workflows in attn Settings (Agents) to run one")
}

func (d *Daemon) handleWorkflowRunUpsert(conn net.Conn, msg *protocol.WorkflowRunUpsertMessage) {
	if err := d.guardWorkflowRunStart(&msg.Run); err != nil {
		d.sendWorkflowActionResult(conn, "upsert", nil, nil, msg.Run.RunID, err)
		return
	}
	d.registerWorkflowEngine(msg.Run.RunID, connWorkflowEngineSink{conn: conn})
	run, err := d.applyWorkflowRunUpsert(&msg.Run)
	d.sendWorkflowActionResult(conn, "upsert", run, nil, msg.Run.RunID, err)
}

func (d *Daemon) handleWorkflowCallUpsert(conn net.Conn, msg *protocol.WorkflowCallUpsertMessage) {
	d.registerWorkflowEngine(msg.RunID, connWorkflowEngineSink{conn: conn})
	run, err := d.applyWorkflowCallUpsert(msg.RunID, &msg.Call)
	d.sendWorkflowActionResult(conn, "call_upsert", run, nil, msg.RunID, err)
}

func (d *Daemon) handleWorkflowRunGet(conn net.Conn, msg *protocol.WorkflowRunGetMessage) {
	run, err := d.getWorkflowRunHydrated(msg.RunID)
	d.sendWorkflowActionResult(conn, "get", run, nil, msg.RunID, err)
}

func (d *Daemon) handleWorkflowRunList(conn net.Conn, msg *protocol.WorkflowRunListMessage) {
	runs, err := d.listWorkflowRunsHydrated(protocol.Deref(msg.SessionID))
	d.sendWorkflowActionResult(conn, "list", nil, runs, "", err)
}

func (d *Daemon) handleWorkflowRunCancel(conn net.Conn, msg *protocol.WorkflowRunCancelMessage) {
	run, _, err := d.cancelWorkflowRun(msg.RunID)
	d.sendWorkflowActionResult(conn, "cancel", run, nil, msg.RunID, err)
}
