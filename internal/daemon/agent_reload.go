package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleAgentReload(conn net.Conn, msg *protocol.AgentReloadMessage) {
	b, err := d.bindings()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	r, err := d.requestFromSession(msg.SourceSessionID, b)
	if err != nil {
		d.replyTargetError(conn, err)
		return
	}
	caller, _ := r.AskingSession()
	if msg.All == (len(msg.Targets) > 0) {
		d.sendError(conn, "name one or more targets, or use --all")
		return
	}
	targets := slices.Clone(msg.Targets)
	if msg.All {
		if !d.requestedByChief(r) {
			d.sendError(conn, "only the profile's chief may reload --all")
			return
		}
		for _, session := range d.store.List("") {
			if session.ProfileID == r.ProfileID() && session.Agent != "shell" {
				targets = append(targets, "session:"+string(session.ID))
			}
		}
		slices.Sort(targets)
	}

	result := &protocol.AgentReloadResult{Outcomes: []protocol.AgentReloadOutcome{}}
	sessions := []*protocol.Session{}
	seen := map[protocol.SessionID]bool{}
	for _, ref := range targets {
		target, err := d.resolveSession(r, b, ref)
		if err == nil && target.Agent == "shell" {
			err = fmt.Errorf("%s is a shell, not an agent", ref)
		}
		if err != nil {
			result.Outcomes = append(result.Outcomes, protocol.AgentReloadOutcome{Target: ref, Error: protocol.Ptr(err.Error())})
			continue
		}
		if !seen[target.ID] {
			seen[target.ID] = true
			sessions = append(sessions, target)
		}
	}
	if i := slices.IndexFunc(sessions, func(s *protocol.Session) bool { return s.ID == caller }); i >= 0 {
		self := sessions[i]
		sessions = append(slices.Delete(sessions, i, i+1), self)
	}
	for _, target := range sessions {
		err := d.mayActOnSession(caller, target, "reload")
		if err == nil {
			err = d.reloadSessionForClient(target.ID, 80, 24)
		}
		outcome := protocol.AgentReloadOutcome{Target: string(target.ID), SessionID: protocol.Ptr(target.ID), Success: err == nil}
		if err != nil {
			outcome.Error = protocol.Ptr(err.Error())
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentReloadResult: result})
}
