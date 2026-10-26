package client

import (
	"fmt"
	"strconv"

	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) MemoryAdd(session protocol.SessionID, text string) (*protocol.CrewMemoryAddResult, error) {
	r, err := c.send(protocol.CrewMemoryAddMessage{Cmd: protocol.CmdCrewMemoryAdd, SessionID: protocol.Ptr(session), Text: text})
	if err != nil {
		return nil, err
	}
	if r.CrewMemoryAddResult == nil {
		return nil, fmt.Errorf("the daemon answered without a memory add result")
	}
	return r.CrewMemoryAddResult, nil
}

func (c *Client) MemoryRecall(session protocol.SessionID, member, profile, pattern string) (*protocol.CrewMemoryReadResult, error) {
	r, err := c.send(protocol.CrewMemoryRecallMessage{Cmd: protocol.CmdCrewMemoryRecall, SessionID: protocol.Ptr(session), Member: protocol.Ptr(member), ProfileID: protocol.Ptr(profile), Pattern: pattern})
	if err != nil {
		return nil, err
	}
	if r.CrewMemoryReadResult == nil {
		return nil, fmt.Errorf("the daemon answered without a memory read result")
	}
	return r.CrewMemoryReadResult, nil
}

func (c *Client) MemoryZoom(session protocol.SessionID, member, profile, target string) (*protocol.CrewMemoryReadResult, error) {
	r, err := c.send(protocol.CrewMemoryZoomMessage{Cmd: protocol.CmdCrewMemoryZoom, SessionID: protocol.Ptr(session), Member: protocol.Ptr(member), ProfileID: protocol.Ptr(profile), Target: target})
	if err != nil {
		return nil, err
	}
	if r.CrewMemoryReadResult == nil {
		return nil, fmt.Errorf("the daemon answered without a memory read result")
	}
	return r.CrewMemoryReadResult, nil
}

func (c *Client) MemorySummarize(session protocol.SessionID, answers []protocol.CrewMemoryAnswer) (*protocol.CrewMemorySummarizeResult, error) {
	r, err := c.send(protocol.CrewMemorySummarizeMessage{Cmd: protocol.CmdCrewMemorySummarize, SessionID: protocol.Ptr(session), Answers: answers})
	if err != nil {
		return nil, err
	}
	if r.CrewMemorySummarizeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a memory summarize result")
	}
	return r.CrewMemorySummarizeResult, nil
}

func (c *Client) MemoryForget(session protocol.SessionID, id int) (*protocol.CrewMemoryChangeResult, error) {
	r, err := c.send(protocol.CrewMemoryForgetMessage{Cmd: protocol.CmdCrewMemoryForget, SessionID: protocol.Ptr(session), ID: strconv.Itoa(id)})
	if err != nil {
		return nil, err
	}
	if r.CrewMemoryChangeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a memory change result")
	}
	return r.CrewMemoryChangeResult, nil
}

func (c *Client) MemoryRewrite(session protocol.SessionID, block string) (*protocol.CrewMemoryChangeResult, error) {
	r, err := c.send(protocol.CrewMemoryRewriteMessage{Cmd: protocol.CmdCrewMemoryRewrite, SessionID: protocol.Ptr(session), Range: block})
	if err != nil {
		return nil, err
	}
	if r.CrewMemoryChangeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a memory change result")
	}
	return r.CrewMemoryChangeResult, nil
}

func (c *Client) MemoryImport(session protocol.SessionID, lines []protocol.CrewMemoryImportLine) (*protocol.CrewMemoryChangeResult, error) {
	r, err := c.send(protocol.CrewMemoryImportMessage{Cmd: protocol.CmdCrewMemoryImport, SessionID: protocol.Ptr(session), Lines: lines})
	if err != nil {
		return nil, err
	}
	if r.CrewMemoryChangeResult == nil {
		return nil, fmt.Errorf("the daemon answered without a memory change result")
	}
	return r.CrewMemoryChangeResult, nil
}
