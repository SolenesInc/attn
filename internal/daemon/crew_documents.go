package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) crewDocumentMember(name string) (crew.Member, error) {
	member, ok, err := d.resolveCrewMember(strings.TrimSpace(name))
	if err != nil {
		return crew.Member{}, err
	}
	if !ok {
		return crew.Member{}, fmt.Errorf("no crew member %q is registered", name)
	}
	if err := d.validateCrewMemberPaths(member); err != nil {
		return crew.Member{}, err
	}
	return member, nil
}

func crewCharterRead(member crew.Member) (protocol.CrewCharterDocument, error) {
	content, hash, err := fsdoc.NewStore(member.HomeDir).Read(crew.CharterFileName)
	if err != nil {
		return protocol.CrewCharterDocument{}, fmt.Errorf("reading %s's charter: %w", crew.DisplayName(member.ID), err)
	}
	return protocol.CrewCharterDocument{Content: string(content), Token: hash}, nil
}

func (d *Daemon) crewCharterGet(name string) (*protocol.CrewCharterGetResult, error) {
	member, err := d.crewDocumentMember(name)
	if err != nil {
		return nil, err
	}
	charter, err := crewCharterRead(member)
	if err != nil {
		return nil, err
	}
	return &protocol.CrewCharterGetResult{Member: member.ID, Charter: charter}, nil
}

func (d *Daemon) crewCharterSet(name, content, expectedToken string) (*protocol.CrewCharterSetResult, error) {
	d.crewCharterMu.Lock()
	defer d.crewCharterMu.Unlock()

	member, err := d.crewDocumentMember(name)
	if err != nil {
		return nil, err
	}
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" {
		return nil, fmt.Errorf("saving %s's charter requires the content token that was read", crew.DisplayName(member.ID))
	}
	hash, conflict, err := fsdoc.NewStore(member.HomeDir).Write(crew.CharterFileName, []byte(content), expectedToken)
	if err != nil {
		return nil, fmt.Errorf("saving %s's charter: %w", crew.DisplayName(member.ID), err)
	}
	if conflict != nil {
		current, err := crewCharterRead(member)
		if err != nil {
			return nil, err
		}
		return &protocol.CrewCharterSetResult{Member: member.ID, Conflict: true, Charter: current}, nil
	}
	return &protocol.CrewCharterSetResult{
		Member:  member.ID,
		Charter: protocol.CrewCharterDocument{Content: content, Token: hash},
	}, nil
}

func crewHandoffTime(filename string) (time.Time, error) {
	stampLength := len(time.Unix(0, 0).UTC().Format(crew.HandoffStampLayout))
	if len(filename) <= stampLength || filename[stampLength] != '-' || !strings.HasSuffix(filename, ".md") {
		return time.Time{}, fmt.Errorf("handoff filename %q does not carry a %s UTC date", filename, crew.HandoffStampLayout)
	}
	when, err := time.Parse(crew.HandoffStampLayout, filename[:stampLength])
	if err != nil {
		return time.Time{}, fmt.Errorf("handoff filename %q has an invalid date: %w", filename, err)
	}
	return when, nil
}

func (d *Daemon) crewHandoffsGet(name string) (*protocol.CrewHandoffsGetResult, error) {
	member, err := d.crewDocumentMember(name)
	if err != nil {
		return nil, err
	}
	dir, err := d.validateCrewHandoffsDir(member)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return &protocol.CrewHandoffsGetResult{Member: member.ID, Handoffs: []protocol.CrewHandoffSummary{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s's handoff history: %w", crew.DisplayName(member.ID), err)
	}

	handoffs := make([]protocol.CrewHandoffSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		occurredAt, err := crewHandoffTime(entry.Name())
		if err != nil {
			d.logf("crew: %s's handoffs dir holds %q, which is not a filed letter; skipping it", crew.DisplayName(member.ID), entry.Name())
			continue
		}
		if err := d.validateCrewLetterPath(member, filepath.Join(dir, entry.Name())); err != nil {
			d.logf("crew: skipping %s's handoff %q: %v", crew.DisplayName(member.ID), entry.Name(), err)
			continue
		}
		handoffs = append(handoffs, protocol.CrewHandoffSummary{Filename: entry.Name(), OccurredAt: occurredAt})
	}
	sort.Slice(handoffs, func(i, j int) bool { return handoffs[i].Filename > handoffs[j].Filename })
	return &protocol.CrewHandoffsGetResult{Member: member.ID, Handoffs: handoffs}, nil
}

func (d *Daemon) crewHandoffGet(name, filename string) (*protocol.CrewHandoffGetResult, error) {
	member, err := d.crewDocumentMember(name)
	if err != nil {
		return nil, err
	}
	dir, err := d.validateCrewHandoffsDir(member)
	if err != nil {
		return nil, err
	}
	filename = strings.TrimSpace(filename)
	if filename == "" || filename != filepath.Base(filename) {
		return nil, fmt.Errorf("handoff %q is not a filename in %s's handoff history", filename, crew.DisplayName(member.ID))
	}
	occurredAt, err := crewHandoffTime(filename)
	if err != nil {
		return nil, fmt.Errorf("reading %s's handoff %s: %w", crew.DisplayName(member.ID), filename, err)
	}
	if err := d.validateCrewLetterPath(member, filepath.Join(dir, filename)); err != nil {
		return nil, err
	}
	content, token, err := fsdoc.NewStore(dir).ReadWithLimit(filename, crew.MaxHandoffFileBytes)
	if err != nil {
		return nil, fmt.Errorf("reading %s's handoff %s: %w", crew.DisplayName(member.ID), filename, err)
	}
	return &protocol.CrewHandoffGetResult{Member: member.ID, Handoff: protocol.CrewHandoffDocument{
		Filename: filename, OccurredAt: occurredAt, Content: string(content), Token: token,
	}}, nil
}

func (d *Daemon) handleCrewCharterGet(conn net.Conn, msg *protocol.CrewCharterGetMessage) {
	result, err := d.crewCharterGet(msg.Member)
	if err != nil {
		d.sendCrewError(conn, "read charter", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewCharterGetResult: result})
}

func (d *Daemon) handleCrewCharterSet(conn net.Conn, msg *protocol.CrewCharterSetMessage) {
	result, err := d.crewCharterSet(msg.Member, msg.Content, msg.ExpectedToken)
	if err != nil {
		d.sendCrewError(conn, "save charter", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewCharterSetResult: result})
}

func (d *Daemon) handleCrewHandoffsGet(conn net.Conn, msg *protocol.CrewHandoffsGetMessage) {
	result, err := d.crewHandoffsGet(msg.Member)
	if err != nil {
		d.sendCrewError(conn, "read handoffs", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewHandoffsGetResult: result})
}

func (d *Daemon) handleCrewHandoffGet(conn net.Conn, msg *protocol.CrewHandoffGetMessage) {
	result, err := d.crewHandoffGet(msg.Member, msg.Filename)
	if err != nil {
		d.sendCrewError(conn, "read handoff", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewHandoffGetResult: result})
}

func crewDocumentRequestID(value *string) (string, error) {
	requestID := strings.TrimSpace(protocol.Deref(value))
	if requestID == "" {
		return "", fmt.Errorf("missing request id")
	}
	return requestID, nil
}

func (d *Daemon) handleCrewCharterGetWS(client *wsClient, msg *protocol.CrewCharterGetMessage) {
	requestID, err := crewDocumentRequestID(msg.RequestID)
	var result *protocol.CrewCharterGetResult
	if err == nil {
		result, err = d.crewCharterGet(msg.Member)
	}
	response := protocol.CrewCharterGetResultMessage{
		Event: protocol.EventCrewCharterGetResult, RequestID: requestID, Success: err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member, response.Charter = protocol.Ptr(result.Member), &result.Charter
	}
	d.sendToClient(client, response)
}

func (d *Daemon) handleCrewCharterSetWS(client *wsClient, msg *protocol.CrewCharterSetMessage) {
	requestID, err := crewDocumentRequestID(msg.RequestID)
	var result *protocol.CrewCharterSetResult
	if err == nil {
		result, err = d.crewCharterSet(msg.Member, msg.Content, msg.ExpectedToken)
	}
	response := protocol.CrewCharterSetResultMessage{
		Event: protocol.EventCrewCharterSetResult, RequestID: requestID, Success: err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member, response.Conflict, response.Charter = protocol.Ptr(result.Member), result.Conflict, &result.Charter
	}
	d.sendToClient(client, response)
}

func (d *Daemon) handleCrewHandoffsGetWS(client *wsClient, msg *protocol.CrewHandoffsGetMessage) {
	requestID, err := crewDocumentRequestID(msg.RequestID)
	var result *protocol.CrewHandoffsGetResult
	if err == nil {
		result, err = d.crewHandoffsGet(msg.Member)
	}
	response := protocol.CrewHandoffsGetResultMessage{
		Event: protocol.EventCrewHandoffsGetResult, RequestID: requestID, Success: err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member, response.Handoffs = protocol.Ptr(result.Member), result.Handoffs
	}
	d.sendToClient(client, response)
}

func (d *Daemon) handleCrewHandoffGetWS(client *wsClient, msg *protocol.CrewHandoffGetMessage) {
	requestID, err := crewDocumentRequestID(msg.RequestID)
	var result *protocol.CrewHandoffGetResult
	if err == nil {
		result, err = d.crewHandoffGet(msg.Member, msg.Filename)
	}
	response := protocol.CrewHandoffGetResultMessage{
		Event: protocol.EventCrewHandoffGetResult, RequestID: requestID, Success: err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Member, response.Handoff = protocol.Ptr(result.Member), &result.Handoff
	}
	d.sendToClient(client, response)
}
