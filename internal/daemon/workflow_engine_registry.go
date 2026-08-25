package daemon

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type workflowEngineSink interface {
	sendWorkflowControl(msg interface{}) error
}

type connWorkflowEngineSink struct {
	conn net.Conn
}

func (s connWorkflowEngineSink) sendWorkflowControl(msg interface{}) error {
	return json.NewEncoder(s.conn).Encode(msg)
}

// The map is lazy-inited so a directly-constructed &Daemon{store: ...} test
// daemon does not panic.
func (d *Daemon) registerWorkflowEngine(runID string, sink workflowEngineSink) {
	if d == nil || strings.TrimSpace(runID) == "" || sink == nil {
		return
	}
	d.workflowEngineMu.Lock()
	defer d.workflowEngineMu.Unlock()
	if d.workflowEngineConn == nil {
		d.workflowEngineConn = make(map[string]workflowEngineSink)
	}
	d.workflowEngineConn[runID] = sink
}

func (d *Daemon) unregisterWorkflowEngine(runID string) {
	if d == nil {
		return
	}
	d.workflowEngineMu.Lock()
	defer d.workflowEngineMu.Unlock()
	delete(d.workflowEngineConn, runID)
}

func (d *Daemon) relayWorkflowCancel(runID string) bool {
	if d == nil {
		return false
	}
	d.workflowEngineMu.Lock()
	sink := d.workflowEngineConn[runID]
	d.workflowEngineMu.Unlock()
	if sink == nil {
		return false
	}
	control := protocol.WorkflowRunCancelMessage{
		Cmd:   protocol.CmdWorkflowRunCancel,
		RunID: runID,
	}
	if err := sink.sendWorkflowControl(control); err != nil {
		d.logf("workflow cancel relay failed for run %s: %v", runID, err)
	}
	return true
}
