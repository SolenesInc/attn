package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

var errLegacyDelegationRequest = errors.New("this pending delegation used the retired implicit launch contract")

type resolvedDelegationLaunch struct {
	Cmd                  string
	RequestID            string
	ProfileID            *string
	SourceSessionID      *protocol.SessionID
	Assignment           protocol.DelegateAssignment
	Checkout             *protocol.DelegateCheckout
	InstallWorkflowSkill bool
	RequireWorkflowSkill bool
	Host                 *string
	Inspection           protocol.CheckoutInspection
	Cwd                  string
	Agent                *string
	Label                *string
	YoloMode             *bool
	Priority             *bool
	Model                *string
	Effort               *string
	AllowWorktreeReuse   *bool
	Role                 *string
	Choice               *string
	Fallback             *bool
	Provider             *string
	Review               *protocol.SeedReviewActionContext
	Desktop              *string

	Brief                 *string
	Worktree              *protocol.DelegateWorktreeRequest
	Plot                  *string
	Handover              *protocol.SeedHandoverRequest
	Confirm               *bool
	PreferencesRevision   *int
	SeedTitle             string
	ParentSeedID          string
	PreviousTenderSession protocol.SessionID
}

func resolveLaunchInput(msg *protocol.DelegateMessage) resolvedDelegationLaunch {
	return resolvedDelegationLaunch{
		ProfileID: msg.ProfileID, RequestID: msg.RequestID, SourceSessionID: msg.SourceSessionID,
		Assignment: msg.Assignment, Checkout: msg.Checkout, Cwd: msg.Cwd, Host: msg.Host,
		Agent: msg.Agent, Label: msg.Label, YoloMode: msg.YoloMode, Priority: msg.Priority,
		Model: msg.Model, Effort: msg.Effort, AllowWorktreeReuse: msg.AllowWorktreeReuse,
		Role: msg.Role, Choice: msg.Choice, Fallback: msg.Fallback, Provider: msg.Provider,
		Review: msg.Review, Desktop: msg.Desktop,
	}
}

func validateDelegateRequestShape(msg *protocol.DelegateMessage) error {
	if msg.Assignment.Kind == "" {
		return errLegacyDelegationRequest
	}
	brief := strings.TrimSpace(protocol.Deref(msg.Assignment.Brief))
	seedID := strings.TrimSpace(protocol.Deref(msg.Assignment.SeedID))
	switch msg.Assignment.Kind {
	case protocol.DelegateAssignmentKindNew:
		if brief == "" || seedID != "" || msg.Assignment.Handover != nil {
			return fmt.Errorf("new assignment requires a non-empty brief and no seed or handover")
		}
	case protocol.DelegateAssignmentKindSeed:
		if seedID == "" || brief != "" {
			return fmt.Errorf("seed assignment requires seed_id and no brief")
		}
	default:
		return fmt.Errorf("unsupported assignment kind %q", msg.Assignment.Kind)
	}
	if msg.Review != nil && msg.Assignment.Handover == nil {
		return fmt.Errorf("review evidence is valid only for a seed handover")
	}
	if strings.TrimSpace(msg.Cwd) == "" {
		return fmt.Errorf("cwd is required")
	}
	if msg.Choice != nil && strings.TrimSpace(protocol.Deref(msg.Role)) == "" {
		return fmt.Errorf("choice requires role")
	}
	if protocol.Deref(msg.Fallback) && msg.Role != nil {
		return fmt.Errorf("role and fallback cannot be combined")
	}
	if msg.Checkout != nil {
		branch := strings.TrimSpace(msg.Checkout.Branch)
		from := strings.TrimSpace(protocol.Deref(msg.Checkout.From))
		path := strings.TrimSpace(protocol.Deref(msg.Checkout.Path))
		if branch == "" {
			return fmt.Errorf("checkout branch is required")
		}
		switch msg.Checkout.Kind {
		case protocol.DelegateCheckoutKindReuse:
			if from != "" || path != "" {
				return fmt.Errorf("reuse checkout accepts branch only")
			}
		case protocol.DelegateCheckoutKindNewWorktree:
			if from == "" {
				return fmt.Errorf("new worktree requires an explicit from ref")
			}
		case protocol.DelegateCheckoutKindExistingBranchWorktree:
			if from != "" {
				return fmt.Errorf("existing branch worktree does not accept from")
			}
		default:
			return fmt.Errorf("unsupported checkout kind %q", msg.Checkout.Kind)
		}
	}
	return nil
}

func (d *Daemon) resolveDelegateRuntimeWithHandoverSnapshot(
	msg *protocol.DelegateMessage,
	reservedSeedID string, reservedBaseCommit string, reservedNoteID string, sessionID protocol.SessionID, ownedWorktreePath string,
	handoverSeedRev int,
	handoverTender who.Party,
	operationID string,
	parentSeedID string,
) (*resolvedDelegationLaunch, error) {
	if err := validateDelegateRequestShape(msg); err != nil {
		return nil, err
	}
	runtime := resolveLaunchInput(msg)
	runtime.Brief = nil
	runtime.Plot = nil
	runtime.Handover = nil
	runtime.Worktree = nil
	runtime.ParentSeedID = strings.TrimSpace(parentSeedID)

	if msg.Assignment.Kind == protocol.DelegateAssignmentKindNew {
		seedID := strings.TrimSpace(reservedSeedID)
		if seedID == "" {
			var err error
			seedID, err = d.mintSeedID()
			if err != nil {
				return nil, err
			}
		}
		runtime.Brief = protocol.Ptr(strings.TrimSpace(protocol.Deref(msg.Assignment.Brief)))
		runtime.SeedTitle = strings.TrimSpace(protocol.Deref(msg.Label))
		if runtime.SeedTitle == "" {
			runtime.SeedTitle = garden.TitleFromBrief(protocol.Deref(runtime.Brief))
		}
		runtime.Plot = protocol.Ptr(seedID)
	} else {
		seedID := strings.TrimSpace(protocol.Deref(msg.Assignment.SeedID))
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return nil, err
		}
		if garden.Closed(seed.Status) {
			return nil, fmt.Errorf("seed %s is %s; replant it before delegating", seedID, seed.Status)
		}
		runtime.Brief = protocol.Ptr(seed.Body)
		runtime.SeedTitle = seed.Title
		runtime.Plot = protocol.Ptr(seedID)
		if msg.Assignment.Handover != nil {
			previousTender := seed.Tender
			if handoverSeedRev > 0 {
				previousTender = handoverTender
			}
			runtime.PreviousTenderSession, _ = d.liveSessionOf(previousTender)
			alreadyBound := strings.TrimSpace(operationID) != "" && d.handoverAlreadyBound(operationID, sessionID, seedID)
			if handoverSeedRev > 0 && !alreadyBound {
				if int(doc.Rev) < handoverSeedRev || seed.Tender != handoverTender {
					return nil, fmt.Errorf("seed %s ownership changed after the delegation request was accepted", seedID)
				}
			}
			runtime.Handover = &protocol.SeedHandoverRequest{
				SeedID: seedID, ExpectedRev: int(doc.Rev), Review: msg.Review,
			}
			if note := strings.TrimSpace(protocol.Deref(msg.Assignment.Handover.Note)); note != "" {
				runtime.Handover.Handoff = protocol.Ptr(note)
				noteID := strings.TrimSpace(reservedNoteID)
				if noteID == "" {
					noteID, err = d.mintNoteID()
					if err != nil {
						return nil, err
					}
				}
				runtime.Handover.NoteID = protocol.Ptr(noteID)
			}
		}
	}

	inspection, err := d.delegationCheckout(protocol.Deref(msg.Host), msg.Cwd, msg.Checkout)
	if err != nil {
		return nil, err
	}
	runtime.Cwd, runtime.Inspection = inspection.Cwd, inspection
	if inspection.MainRepo == "" {
		if msg.Checkout != nil {
			return nil, fmt.Errorf("checkout flags are invalid outside Git: %s", inspection.Cwd)
		}
		return &runtime, nil
	}
	if msg.Checkout == nil {
		return nil, fmt.Errorf("git checkout requires reuse or new_worktree with branch arguments; no worker started")
	}
	branch := strings.TrimSpace(msg.Checkout.Branch)
	switch msg.Checkout.Kind {
	case protocol.DelegateCheckoutKindReuse:
		if inspection.Branch == "" {
			return nil, fmt.Errorf("cannot reuse detached or unreadable checkout %s", inspection.RepoRoot)
		}
		if inspection.Branch != branch {
			return nil, fmt.Errorf("branch mismatch: expected %s; %s is on %s. No worker started; seed ownership unchanged", branch, inspection.RepoRoot, inspection.Branch)
		}
	case protocol.DelegateCheckoutKindNewWorktree, protocol.DelegateCheckoutKindExistingBranchWorktree:
		runtime.Worktree = &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(inspection.MainRepo), Branch: branch}
		if msg.Checkout.Path != nil {
			runtime.Worktree.Path = protocol.Ptr(inspection.WorktreePath)
		}
		exists := checkoutCommit(inspection, "refs/heads/"+branch) != ""
		if msg.Checkout.Kind == protocol.DelegateCheckoutKindExistingBranchWorktree {
			if !exists {
				return nil, fmt.Errorf("local branch %q does not exist; create it with --branch and --from when starting from a remote ref", branch)
			}
			runtime.Worktree.ExistingBranch = protocol.Ptr(true)
		} else {
			ownedRecovery := ownedWorktreePath != "" && ownedWorktreePath == inspection.WorktreePath
			if exists && !ownedRecovery {
				return nil, fmt.Errorf("branch %q already exists; use --existing-branch or choose another name", branch)
			}
			commit := reservedBaseCommit
			if commit == "" {
				commit = checkoutCommit(inspection, protocol.Deref(msg.Checkout.From))
			}
			if commit == "" {
				return nil, fmt.Errorf("base ref %q is unavailable; fetch it explicitly or choose another ref", protocol.Deref(msg.Checkout.From))
			}
			runtime.Worktree.StartingFrom = protocol.Ptr(commit)
		}
	}
	return &runtime, nil
}
