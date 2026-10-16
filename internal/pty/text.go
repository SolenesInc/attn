package pty

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/ghosttyvt"
)

type TextBlock struct {
	ID       uint64  `json:"id"`
	Command  *string `json:"command,omitempty"`
	ExitCode *int32  `json:"exit_code,omitempty"`
	Output   string  `json:"output"`
}

type ReadTextInfo struct {
	Text            string     `json:"text"`
	Block           *TextBlock `json:"block,omitempty"`
	PromptExpected  bool       `json:"prompt_expected"`
	IntegrationSeen bool       `json:"integration_seen"`
	AtPrompt        bool       `json:"at_prompt"`
	LastBlockID     uint64     `json:"last_block_id"`
}

type WaitBlockParams struct {
	AfterBlockID *uint64 `json:"after_block_id,omitempty"`
	Until        string  `json:"until"`
	FirstPrompt  bool    `json:"first_prompt,omitempty"`
}

type WaitBlockResult struct {
	Block    *TextBlock `json:"block,omitempty"`
	Exited   bool       `json:"exited,omitempty"`
	ExitCode *int       `json:"exit_code,omitempty"`
}

var ErrNoShellIntegration = errors.New("OSC 133 rule: this shell has no command marks; run --wait and wait --until prompt cannot work here; use send or run without --wait")

func (bt *blockTable) readState() ReadTextInfo {
	return ReadTextInfo{IntegrationSeen: bt.integrationSeen, AtPrompt: bt.atPrompt, LastBlockID: bt.lastCompletedID}
}

func (bt *blockTable) readBlock(term *ghosttyvt.Terminal, after *uint64) (*TextBlock, error) {
	var found *trackedBlock
	if after == nil {
		if len(bt.completed) > 0 {
			found = bt.completed[len(bt.completed)-1]
		}
	} else {
		for _, b := range bt.completed {
			if b.id > *after {
				found = b
				break
			}
		}
	}
	if found == nil {
		return nil, errors.New("no completed command block is available (OSC 133 command-start marks are required to read output)")
	}
	sx, sy, startOK := found.outputRef.point()
	ex, ey, endOK := found.endRef.point()
	if !startOK || !endOK {
		return nil, fmt.Errorf("command block %d output is no longer in scrollback", found.id)
	}
	output, err := term.TextRange(sx, sy, ex, ey, found.endInclusive)
	if err != nil {
		return nil, fmt.Errorf("command block %d: %w", found.id, err)
	}
	return &TextBlock{ID: found.id, Command: found.command, ExitCode: found.exitCode, Output: output}, nil
}

func (s *Session) textChangedLocked() {
	if s.textChanged != nil {
		close(s.textChanged)
		s.textChanged = nil
	}
}

func (s *Session) readText(mode string, lines int) (ReadTextInfo, error) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	if s.wireFeed == nil || s.ghostty == nil {
		return ReadTextInfo{}, errors.New("terminal text is unavailable")
	}
	table := s.wireFeed.blocks.table
	info := table.readState()
	info.PromptExpected = s.promptExpected
	if !info.IntegrationSeen {
		if group, ok := s.foregroundProcessGroup(); ok {
			info.AtPrompt = group == s.childProcessGroup()
		}
	}
	switch mode {
	case "screen":
		info.Text = s.ghostty.ViewportText()
	case "scrollback":
		info.Text = s.ghostty.PlainText()
		if lines > 0 {
			parts := strings.Split(strings.TrimRight(info.Text, "\n"), "\n")
			if len(parts) > lines {
				info.Text = strings.Join(parts[len(parts)-lines:], "\n")
			}
		}
	case "last":
		block, err := table.readBlock(s.ghostty, nil)
		if err != nil {
			return info, err
		}
		info.Block, info.Text = block, block.Output
	default:
		return info, fmt.Errorf("unknown terminal read mode %q", mode)
	}
	return info, nil
}

func (s *Session) waitBlock(ctx context.Context, params WaitBlockParams) (WaitBlockResult, error) {
	if params.Until != "prompt" && params.Until != "exit" {
		return WaitBlockResult{}, fmt.Errorf("unknown wait condition %q", params.Until)
	}
	for {
		s.replayMu.Lock()
		if s.wireFeed == nil || s.ghostty == nil {
			s.replayMu.Unlock()
			info := s.sessionInfo()
			if !info.Running {
				return WaitBlockResult{Exited: true, ExitCode: info.ExitCode}, nil
			}
			return WaitBlockResult{}, errors.New("terminal closed before wait completed")
		}
		table := s.wireFeed.blocks.table
		state := table.readState()
		if params.Until == "prompt" {
			if !state.IntegrationSeen && (!params.FirstPrompt || !s.promptExpected) {
				s.replayMu.Unlock()
				return WaitBlockResult{}, ErrNoShellIntegration
			}
			if params.AfterBlockID != nil && state.LastBlockID > *params.AfterBlockID {
				block, err := table.readBlock(s.ghostty, params.AfterBlockID)
				s.replayMu.Unlock()
				return WaitBlockResult{Block: block}, err
			}
			if params.AfterBlockID == nil && state.AtPrompt {
				s.replayMu.Unlock()
				return WaitBlockResult{}, nil
			}
		}
		select {
		case <-s.exited:
			s.replayMu.Unlock()
			info := s.sessionInfo()
			return WaitBlockResult{Exited: true, ExitCode: info.ExitCode}, nil
		default:
		}
		if s.textChanged == nil {
			s.textChanged = make(chan struct{})
		}
		changed := s.textChanged
		s.replayMu.Unlock()
		select {
		case <-ctx.Done():
			return WaitBlockResult{}, ctx.Err()
		case <-s.exited:
			continue
		case <-changed:
		}
	}
}

func (m *Manager) ReadText(id, mode string, lines int) (ReadTextInfo, error) {
	session, err := m.getSession(id)
	if err != nil {
		return ReadTextInfo{}, err
	}
	return session.readText(mode, lines)
}

func (m *Manager) WaitBlock(ctx context.Context, id string, params WaitBlockParams) (WaitBlockResult, error) {
	session, err := m.getSession(id)
	if err != nil {
		return WaitBlockResult{}, err
	}
	return session.waitBlock(ctx, params)
}
