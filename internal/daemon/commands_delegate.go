package daemon

import (
	"context"
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) delegateCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"delegate":        command.Typed(d.runDelegate),
		"delegate status": command.Typed(d.runDelegateStatus),
	}
}

func (d *Daemon) runDelegate(ctx context.Context, c command.Caller, a command.DelegateArgs) (any, error) {
	request, err := delegateRequest(a)
	if err != nil {
		return nil, err
	}
	if c.Session != "" {
		request.SourceSessionID = protocol.Ptr(c.Session)
	}
	if c.Profile != "" {
		request.ProfileID = protocol.Ptr(c.Profile)
	}
	if request.CwdBase == nil {
		if row := d.store.Get(c.Session); row != nil && row.Directory != "" {
			request.CwdBase = protocol.Ptr(row.Directory)
		}
	}
	return d.startDelegation(ctx, request, d.sessionOrOwner(c.Session))
}

func delegateRequest(a command.DelegateArgs) (*protocol.DelegateMessage, error) {
	role, choice := strings.TrimSpace(a.Role), strings.TrimSpace(a.Choice)
	if strings.TrimSpace(protocol.Deref(a.Model)) == "" && role == "" && !a.Fallback {
		return nil, command.Usage("{model} is required (for example, {model} gpt-5.6-sol or {model} opus); {effort} defaults to medium when supported")
	}
	if a.Fallback && role != "" {
		return nil, command.Usage("{role} and {fallback} cannot be combined")
	}
	if choice != "" && role == "" {
		return nil, command.Usage("{choice} requires {role}")
	}
	brief, seed := strings.TrimSpace(a.Brief), strings.TrimSpace(a.Seed)
	if (brief == "") == (seed == "") {
		return nil, command.Usage("pass exactly one of {brief} or {seed}")
	}
	if a.Handover && seed == "" {
		return nil, command.Usage("{handover} requires {seed}")
	}
	note := strings.TrimSpace(a.Note)
	if note != "" && !a.Handover {
		return nil, command.Usage("{note} requires {handover}")
	}
	cwd := strings.TrimSpace(a.Cwd)
	if cwd == "" {
		return nil, command.Usage("{cwd} is required")
	}
	checkout, err := delegateCheckout(a)
	if err != nil {
		return nil, err
	}

	request := &protocol.DelegateMessage{
		Cmd:        protocol.CmdDelegate,
		RequestID:  strings.TrimSpace(a.RequestID),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr(brief)},
		Cwd:        cwd,
		Checkout:   checkout,
		Model:      harnessDefault(trimmed(a.Model)),
		Effort:     harnessDefault(trimmed(a.Effort)),
	}
	if seed != "" {
		request.Assignment = protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(seed)}
		if a.Handover {
			request.Assignment.Handover = &protocol.DelegateHandover{Note: nonEmptyText(note)}
		}
	}
	if base := strings.TrimSpace(a.CwdBase); base != "" {
		request.CwdBase = protocol.Ptr(base)
	}
	if a.Desktop != nil {
		desktop := strings.TrimSpace(*a.Desktop)
		if desktop == "" {
			return nil, command.Usage("{desktop} needs a shortcut digit (1-9), a desktop name or a desktop id")
		}
		request.Desktop = protocol.Ptr(desktop)
	}
	request.Host = nonEmptyText(strings.TrimSpace(a.Host))
	request.Authorization = nonEmptyText(strings.TrimSpace(a.Authorization))
	request.Agent = nonEmptyText(strings.TrimSpace(a.Agent))
	request.Role = nonEmptyText(role)
	request.Choice = nonEmptyText(choice)
	request.Label = nonEmptyText(strings.TrimSpace(a.Name))
	if a.Priority {
		request.Priority = protocol.Ptr(true)
	}
	if a.Fallback {
		request.Fallback = protocol.Ptr(true)
	}
	if a.Yolo {
		request.YoloMode = protocol.Ptr(true)
	}
	if a.AllowWorktreeReuse {
		request.AllowWorktreeReuse = protocol.Ptr(true)
	}
	return request, nil
}

func delegateCheckout(a command.DelegateArgs) (*protocol.DelegateCheckout, error) {
	branch, existing := strings.TrimSpace(a.Branch), strings.TrimSpace(a.ExistingBranch)
	from, path := strings.TrimSpace(a.From), strings.TrimSpace(a.WorktreePath)
	switch {
	case a.ReuseCheckout && a.NewWorktree:
		return nil, command.Usage("choose only one of {reuse_checkout} or {new_worktree}")
	case a.ReuseCheckout:
		if branch == "" {
			return nil, command.Usage("{reuse_checkout} requires {branch}")
		}
		if existing != "" || from != "" || path != "" {
			return nil, command.Usage("{reuse_checkout} accepts {branch} only")
		}
		return &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: branch}, nil
	case a.NewWorktree:
		if (branch == "") == (existing == "") {
			return nil, command.Usage("{new_worktree} requires either {branch} <name> {from} <ref> or {existing_branch} <name>")
		}
		if branch != "" && from == "" {
			return nil, command.Usage("{new_worktree} {branch} requires {from}")
		}
		if existing != "" && from != "" {
			return nil, command.Usage("{new_worktree} {existing_branch} does not accept {from}")
		}
		checkout := &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: branch, From: nonEmptyText(from), Path: nonEmptyText(path)}
		if existing != "" {
			checkout.Kind, checkout.Branch = protocol.DelegateCheckoutKindExistingBranchWorktree, existing
		}
		return checkout, nil
	}
	for _, arg := range []struct {
		name  string
		given bool
	}{{"branch", a.Branch != ""}, {"existing_branch", a.ExistingBranch != ""}, {"from", a.From != ""}, {"worktree_path", a.WorktreePath != ""}, {"allow_worktree_reuse", a.AllowWorktreeReuse}} {
		if arg.given {
			return nil, command.Usage("{%s} requires {reuse_checkout} or {new_worktree}", arg.name)
		}
	}
	return nil, nil
}

func trimmed(value *string) *string {
	if value == nil {
		return nil
	}
	return protocol.Ptr(strings.TrimSpace(*value))
}

func nonEmptyText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (d *Daemon) runDelegateStatus(ctx context.Context, c command.Caller, a command.DelegateStatusArgs) (any, error) {
	msg := &protocol.DelegateStatusMessage{Cmd: protocol.CmdDelegateStatus, ID: strings.TrimSpace(a.ID), WaitPast: nonEmptyText(a.WaitPast)}
	if c.Session != "" {
		msg.SourceSessionID = protocol.Ptr(c.Session)
	}
	if c.Profile != "" {
		msg.ProfileID = protocol.Ptr(c.Profile)
	}
	operation, err := d.delegationStatus(msg, "", ctx.Done())
	if errors.Is(err, errCallerGone) {
		return nil, ctx.Err()
	}
	return operation, err
}
