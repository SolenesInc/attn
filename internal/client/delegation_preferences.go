package client

import (
	"fmt"
	"github.com/victorarias/attn/internal/protocol"
)

func (c *Client) DelegationRoles() (*protocol.DelegationRolesResult, error) {
	response, err := c.send(protocol.DelegationRolesMessage{Cmd: protocol.CmdDelegationRoles})
	if err != nil {
		return nil, err
	}
	if response.DelegationRoles == nil {
		return nil, fmt.Errorf("daemon returned no delegation roles response")
	}
	return response.DelegationRoles, nil
}

func (c *Client) DelegationPreferencesShow() (*protocol.DelegationPreferencesRevision, error) {
	return c.delegationPreferencesRevision(protocol.DelegationPreferencesShowMessage{Cmd: protocol.CmdDelegationPreferencesShow})
}

func (c *Client) DelegationPreferencesCommit(preferences protocol.DelegationPreferences, message string) (*protocol.DelegationPreferencesRevision, error) {
	return c.delegationPreferencesRevision(protocol.DelegationPreferencesCommitMessage{
		Cmd:         protocol.CmdDelegationPreferencesCommit,
		Preferences: preferences,
		Message:     nonEmpty(message),
	})
}

func (c *Client) DelegationPreferencesRollback(revision *int, message string) (*protocol.DelegationPreferencesRevision, error) {
	return c.delegationPreferencesRevision(protocol.DelegationPreferencesRollbackMessage{
		Cmd:      protocol.CmdDelegationPreferencesRollback,
		Revision: revision,
		Message:  nonEmpty(message),
	})
}

func (c *Client) DelegationPreferencesHistory(limit int) (*protocol.DelegationPreferencesHistoryResult, error) {
	response, err := c.send(protocol.DelegationPreferencesHistoryMessage{Cmd: protocol.CmdDelegationPreferencesHistory, Limit: &limit})
	if err != nil {
		return nil, err
	}
	if response.DelegationPreferencesHistory == nil {
		return nil, fmt.Errorf("daemon returned no delegation preferences history")
	}
	return response.DelegationPreferencesHistory, nil
}

func (c *Client) delegationPreferencesRevision(msg any) (*protocol.DelegationPreferencesRevision, error) {
	response, err := c.send(msg)
	if err != nil {
		return nil, err
	}
	if response.DelegationPreferencesRevision == nil {
		return nil, fmt.Errorf("daemon returned no delegation preferences revision")
	}
	return response.DelegationPreferencesRevision, nil
}

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
