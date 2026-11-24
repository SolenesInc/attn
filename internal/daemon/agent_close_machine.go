package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) closeAgentMachine(_ context.Context, msg protocol.AgentLaunchCloseMessage) (protocol.AgentLaunchCloseResult, error) {
	monitor := d.sessionMonitor(msg.SessionID)
	result := protocol.AgentLaunchCloseResult{}
	switch msg.Phase {
	case "prepare":
		monitor.MarkForcedStop()
		driver := monitor.EndRun()
		raw, err := json.Marshal(driver)
		if err != nil {
			return result, err
		}
		result.Driver = protocol.Ptr(string(raw))
	case "cancel":
		monitor.ClearForcedStop()
		monitor.ResumeUsage()
		monitor.ResetInput()
		monitor.EnsureTranscriptWatcher(d.store.GetSessionConversation(msg.SessionID).TranscriptPath)
		var driver sessionmonitor.RunRecord
		if msg.Driver != nil {
			if err := json.Unmarshal([]byte(*msg.Driver), &driver); err != nil {
				return result, err
			}
			if driver.ID != "" {
				monitor.RestoreRun(driver)
			}
		}
	case "drain":
		if monitor == nil {
			return result, nil
		}
		monitor.FenceInput()
		monitor.FinishAndSettleUsage()
		view := monitor.Snapshot()
		if !monitor.Publish(view) {
			return result, fmt.Errorf("final monitor view for %s was not saved", msg.SessionID)
		}
		raw, err := json.Marshal(view)
		if err != nil {
			return result, err
		}
		result.View = protocol.Ptr(string(raw))
		pausepoint.At(pausepoint.AgentCloseDrained)
	case "committed":
		if len(msg.Copy) > 0 {
			if home := d.homeBridge(); home != nil {
				if err := d.applyOutpostCopy(home.Context(), home.Peer().DaemonID, msg.Copy); err != nil {
					d.logf("apply closed session copy for %s: %v", msg.SessionID, err)
				}
			}
		}
		d.forgetMonitor(msg.SessionID)
	case "released":
		d.harnessOf(protocol.Deref(msg.Agent)).End(d.life.Context(), harness.Ending{Session: msg.SessionID, Profile: protocol.Deref(msg.Profile), Released: protocol.Deref(msg.Conversation)})
	case "notify":
		var driver sessionmonitor.RunRecord
		if msg.Driver != nil {
			if err := json.Unmarshal([]byte(*msg.Driver), &driver); err != nil {
				return result, err
			}
		}
		if driver.ID != "" {
			d.runEnded(protocol.Deref(msg.Agent), harness.RunEnd{Session: msg.SessionID, Terminal: protocol.Deref(msg.TerminalID), Owner: driver.Owner, Run: driver.ID, Reason: harness.RunKilled, Signal: protocol.Deref(msg.Signal)})
		}
	default:
		return result, fmt.Errorf("unknown agent close phase %q", msg.Phase)
	}
	return result, nil
}

func (d *Daemon) agentCloseCall(msg protocol.AgentLaunchCloseMessage) (protocol.AgentLaunchCloseResult, error) {
	msg.Cmd = protocol.CmdAgentLaunchClose
	return Call[protocol.AgentLaunchCloseResult](context.Background(), d.router(), msg)
}

func (d *Daemon) drainAgentBeforeClose(sessionID protocol.SessionID) error {
	result, err := d.agentCloseCall(protocol.AgentLaunchCloseMessage{SessionID: sessionID, Phase: "drain"})
	if err != nil {
		return err
	}
	if result.View == nil || d.sessionHost(sessionID) == "" {
		return nil
	}
	var view sessionmonitor.View
	if err := json.Unmarshal([]byte(*result.View), &view); err != nil {
		return err
	}
	if !d.applyHomeView(sessionID, view, "", nil) {
		return fmt.Errorf("final monitor view for %s was not applied", sessionID)
	}
	return nil
}
