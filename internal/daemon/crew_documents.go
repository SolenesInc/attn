package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
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

type crewCharterVersion struct {
	hash     string
	revision uint64
}

func (d *Daemon) readCrewCharterLocked(member crew.Member) (protocol.CrewCharterDocument, string, error) {
	content, token, err := fsdoc.NewStore(member.HomeDir).Read(crew.CharterFileName)
	if err != nil {
		return protocol.CrewCharterDocument{}, "", fmt.Errorf("reading %s's charter: %w", crew.DisplayName(member.ID), err)
	}
	if d.crewCharterVersions == nil {
		d.crewCharterVersions = make(map[string]crewCharterVersion)
	}
	if d.crewCharterLifetime == "" {
		d.crewCharterLifetime = uuid.NewString()
	}
	version, ok := d.crewCharterVersions[member.ID]
	if !ok {
		version = crewCharterVersion{hash: token, revision: 1}
	} else if version.hash != token {
		version.hash = token
		version.revision++
	}
	d.crewCharterVersions[member.ID] = version
	return protocol.CrewCharterDocument{
		Content: string(content),
		Token:   fmt.Sprintf("%s:%d:%s", d.crewCharterLifetime, version.revision, token),
	}, token, nil
}

func (d *Daemon) crewCharterGet(name string) (*protocol.CrewCharterGetResult, error) {
	member, err := d.crewDocumentMember(name)
	if err != nil {
		return nil, err
	}
	d.crewDocumentMu.Lock()
	defer d.crewDocumentMu.Unlock()
	charter, _, err := d.readCrewCharterLocked(member)
	if err != nil {
		return nil, err
	}
	return &protocol.CrewCharterGetResult{Member: member.ID, Charter: charter}, nil
}

func (d *Daemon) crewCharterSet(name, content, expectedToken string) (*protocol.CrewCharterSetResult, error) {
	member, err := d.crewDocumentMember(name)
	if err != nil {
		return nil, err
	}
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" {
		return nil, fmt.Errorf("saving %s's charter requires the content token that was read", crew.DisplayName(member.ID))
	}

	d.crewDocumentMu.Lock()
	defer d.crewDocumentMu.Unlock()
	current, currentHash, err := d.readCrewCharterLocked(member)
	if err != nil {
		return nil, err
	}
	if expectedToken != current.Token {
		return &protocol.CrewCharterSetResult{Member: member.ID, Conflict: true, Charter: current}, nil
	}
	store := fsdoc.NewStore(member.HomeDir)
	hash, conflict, err := store.Write(crew.CharterFileName, []byte(content), currentHash)
	if err != nil {
		return nil, fmt.Errorf("saving %s's charter: %w", crew.DisplayName(member.ID), err)
	}
	if conflict != nil {
		current, _, readErr := d.readCrewCharterLocked(member)
		if readErr != nil {
			return nil, readErr
		}
		return &protocol.CrewCharterSetResult{Member: member.ID, Conflict: true, Charter: current}, nil
	}
	version := d.crewCharterVersions[member.ID]
	version.hash = hash
	version.revision++
	d.crewCharterVersions[member.ID] = version
	return &protocol.CrewCharterSetResult{
		Member: member.ID,
		Charter: protocol.CrewCharterDocument{
			Content: content,
			Token:   fmt.Sprintf("%s:%d:%s", d.crewCharterLifetime, version.revision, hash),
		},
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
		return &protocol.CrewHandoffsGetResult{Member: member.ID, Handoffs: []protocol.CrewHandoffDocument{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s's handoff history: %w", crew.DisplayName(member.ID), err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, entry.Name())
	}
	crew.SortHandoffNames(names)
	handoffs := make([]protocol.CrewHandoffDocument, 0, len(names))
	for _, filename := range names {
		path := filepath.Join(dir, filename)
		if err := d.validateCrewLetterPath(member, path); err != nil {
			return nil, err
		}
		occurredAt, err := crewHandoffTime(filename)
		if err != nil {
			return nil, fmt.Errorf("reading %s's handoff history: %w", crew.DisplayName(member.ID), err)
		}
		content, token, err := fsdoc.NewStore(dir).ReadWithLimit(filename, crew.MaxHandoffBytes)
		if err != nil {
			return nil, fmt.Errorf("reading %s's handoff %s: %w", crew.DisplayName(member.ID), filename, err)
		}
		handoffs = append(handoffs, protocol.CrewHandoffDocument{
			Filename: filename, OccurredAt: occurredAt, Content: string(content), Token: token,
		})
	}
	return &protocol.CrewHandoffsGetResult{Member: member.ID, Handoffs: handoffs}, nil
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
