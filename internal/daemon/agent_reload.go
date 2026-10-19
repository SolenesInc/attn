package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleAgentReload(conn net.Conn, msg *protocol.AgentReloadMessage) {
	caller, code := d.resolveSessionByIDOrPrefix(string(msg.SourceSessionID))
	if caller == nil {
		d.replyAgentMsgError(conn, "sender_"+code, "reload requires a live caller session")
		return
	}
	if msg.All == (len(msg.Targets) > 0) {
		d.sendError(conn, "name one or more sessions, or use --all")
		return
	}
	targets := slices.Clone(msg.Targets)
	if msg.All {
		if d.chiefOfProfile(caller.ProfileID) != caller.ID {
			d.sendError(conn, "only the profile's chief may reload --all")
			return
		}
		for _, session := range d.store.List("") {
			if session.ProfileID == caller.ProfileID && session.Agent != "shell" {
				targets = append(targets, string(session.ID))
			}
		}
		slices.Sort(targets)
	}

	result := &protocol.AgentReloadResult{Outcomes: []protocol.AgentReloadOutcome{}}
	sessions := []*protocol.Session{}
	seen := map[protocol.SessionID]bool{}
	for _, ref := range targets {
		target, code := d.resolveSessionReference(ref, caller.ProfileID)
		var err error
		switch {
		case target == nil:
			if code == "ambiguous_session" {
				err = fmt.Errorf("%q matches more than one session; use more of the id", ref)
			} else {
				err = fmt.Errorf("no session matches %q in the caller's profile", ref)
			}
		case target.Agent == "shell":
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
	if i := slices.IndexFunc(sessions, func(s *protocol.Session) bool { return s.ID == caller.ID }); i >= 0 {
		self := sessions[i]
		sessions = append(slices.Delete(sessions, i, i+1), self)
	}
	for _, target := range sessions {
		err := d.mayActOnSession(caller.ID, target, "reload")
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
