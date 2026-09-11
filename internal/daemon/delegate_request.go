package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

var errLegacyDelegationRequest = errors.New("this pending delegation used the retired implicit launch contract")

// resolvedDelegationLaunch is the daemon's execution description. Resolution fills its
// internal placement and seed-binding fields once for the existing launch machinery.
type resolvedDelegationLaunch struct {
	Cmd                string
	RequestID          string
	SourceSessionID    *string
	Assignment         protocol.DelegateAssignment
	Checkout           *protocol.DelegateCheckout
	Cwd                string
	Agent              *string
	Label              *string
	YoloMode           *bool
	Model              *string
	Effort             *string
	AllowWorktreeReuse *bool
	Role               *string
	Choice             *string
	Fallback           *bool
	Provider           *string
	Review             *protocol.SeedReviewActionContext

	Brief                 *string
	TicketID              *string
	Placement             *string
	WorkspaceID           *string
	Worktree              *protocol.DelegateWorktreeRequest
	Plot                  *string
	Handover              *protocol.SeedHandoverRequest
	Confirm               *bool
	PreferencesRevision   *int
	ParentSeedID          string
	PreviousTenderSession string
}

func resolveLaunchInput(msg *protocol.DelegateMessage) resolvedDelegationLaunch {
	return resolvedDelegationLaunch{
		RequestID: msg.RequestID, SourceSessionID: msg.SourceSessionID,
		Assignment: msg.Assignment, Checkout: msg.Checkout, Cwd: msg.Cwd,
		Agent: msg.Agent, Label: msg.Label, YoloMode: msg.YoloMode,
		Model: msg.Model, Effort: msg.Effort, AllowWorktreeReuse: msg.AllowWorktreeReuse,
		Role: msg.Role, Choice: msg.Choice, Fallback: msg.Fallback, Provider: msg.Provider,
		Review: msg.Review,
	}
}

func (msg *resolvedDelegationLaunch) preferenceRequest() *protocol.DelegateMessage {
	return &protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: msg.RequestID,
		SourceSessionID: msg.SourceSessionID, Assignment: msg.Assignment,
		Cwd: msg.Cwd, Checkout: msg.Checkout, Agent: msg.Agent, Label: msg.Label,
		YoloMode: msg.YoloMode, Model: msg.Model, Effort: msg.Effort,
		AllowWorktreeReuse: msg.AllowWorktreeReuse, Role: msg.Role, Choice: msg.Choice,
		Fallback: msg.Fallback, Provider: msg.Provider, Review: msg.Review,
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

func (d *Daemon) resolveDelegateRuntime(msg *protocol.DelegateMessage, reservedSeedID, reservedBaseCommit, reservedNoteID, sessionID, ownedWorktreePath string, worktreeOwned bool) (*resolvedDelegationLaunch, error) {
	return d.resolveDelegateRuntimeWithHandoverSnapshot(msg, reservedSeedID, reservedBaseCommit, reservedNoteID, sessionID, ownedWorktreePath, worktreeOwned, 0, "", "", "", "")
}

func resolveAcceptedDelegationBase(msg *protocol.DelegateMessage) (string, error) {
	if msg.Checkout == nil || msg.Checkout.Kind != protocol.DelegateCheckoutKindNewWorktree {
		return "", nil
	}
	directory, err := validateDelegationDirectory(msg.Cwd)
	if err != nil {
		return "", err
	}
	repoRoot, err := attngit.GetRepoRoot(directory)
	if err != nil {
		return "", fmt.Errorf("checkout flags are invalid outside Git: %s", directory)
	}
	base := strings.TrimSpace(protocol.Deref(msg.Checkout.From))
	commit, err := attngit.Output(attngit.OpMetadata, attngit.ResolveMainRepoPath(repoRoot), "rev-parse", "--verify", base+"^{commit}")
	if err != nil || strings.TrimSpace(string(commit)) == "" {
		return "", fmt.Errorf("base ref %q is unavailable; fetch it explicitly or choose another ref", base)
	}
	return strings.TrimSpace(string(commit)), nil
}

func (d *Daemon) resolveDelegateRuntimeWithHandoverSnapshot(
	msg *protocol.DelegateMessage,
	reservedSeedID, reservedBaseCommit, reservedNoteID, sessionID, ownedWorktreePath string,
	worktreeOwned bool,
	handoverSeedRev int,
	handoverTenderSession, handoverTenderMember string,
	operationID string,
	parentSeedID string,
) (*resolvedDelegationLaunch, error) {
	if err := validateDelegateRequestShape(msg); err != nil {
		return nil, err
	}
	runtime := resolveLaunchInput(msg)
	runtime.Placement = protocol.Ptr(delegationPlacementNew)
	runtime.Brief = nil
	runtime.Plot = nil
	runtime.Handover = nil
	runtime.Worktree = nil
	runtime.WorkspaceID = nil
	runtime.ParentSeedID = strings.TrimSpace(parentSeedID)

	if msg.Assignment.Kind == protocol.DelegateAssignmentKindNew {
		if err := d.requireHome(garden.Surface); err != nil {
			return nil, err
		}
		seedID := strings.TrimSpace(reservedSeedID)
		if seedID == "" {
			var err error
			seedID, err = d.mintSeedID()
			if err != nil {
				return nil, err
			}
		}
		runtime.Brief = protocol.Ptr(strings.TrimSpace(protocol.Deref(msg.Assignment.Brief)))
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
		runtime.Plot = protocol.Ptr(seedID)
		if msg.Assignment.Handover != nil {
			previousTenderSession := seed.TenderSession
			if handoverSeedRev > 0 {
				previousTenderSession = strings.TrimSpace(handoverTenderSession)
			}
			runtime.PreviousTenderSession = previousTenderSession
			alreadyBound := strings.TrimSpace(operationID) != "" && d.handoverAlreadyBound(operationID, sessionID, seedID)
			if handoverSeedRev > 0 && !alreadyBound {
				// Acceptance pins the intended holder, not the seed body. The binding transaction
				// guards allowed same-holder edits at their current revision.
				if int(doc.Rev) < handoverSeedRev ||
					seed.TenderSession != strings.TrimSpace(handoverTenderSession) ||
					seed.TenderMember != strings.TrimSpace(handoverTenderMember) {
					return nil, fmt.Errorf("seed %s ownership changed after the delegation request was accepted", seedID)
				}
			}
			runtime.Handover = &protocol.SeedHandoverRequest{
				SeedID: seedID, ExpectedRev: int(doc.Rev), ExpectedTenderSession: seed.TenderSession,
				ExpectedTenderMember: seed.TenderMember, Review: msg.Review,
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
			if runtime.Label == nil {
				runtime.Label = protocol.Ptr(handoverSessionName(seed.Title, sessionID))
			}
		}
	}

	directory, err := validateDelegationDirectory(msg.Cwd)
	if err != nil {
		return nil, err
	}
	runtime.Cwd = directory
	repoRoot, gitErr := attngit.GetRepoRoot(directory)
	if gitErr != nil {
		if msg.Checkout != nil {
			return nil, fmt.Errorf("checkout flags are invalid outside Git: %s", directory)
		}
		return &runtime, nil
	}
	if msg.Checkout == nil {
		return nil, fmt.Errorf("git checkout requires reuse or new_worktree with branch arguments; no worker started")
	}
	repoRoot = attngit.CanonicalizePath(repoRoot)
	switch msg.Checkout.Kind {
	case protocol.DelegateCheckoutKindReuse:
		branchOutput, err := attngit.Output(attngit.OpMetadata, directory, "symbolic-ref", "--short", "HEAD")
		branch := strings.TrimSpace(string(branchOutput))
		if err != nil || branch == "" {
			return nil, fmt.Errorf("cannot reuse detached or unreadable checkout %s: %v", repoRoot, err)
		}
		if branch != strings.TrimSpace(msg.Checkout.Branch) {
			return nil, fmt.Errorf("branch mismatch: expected %s; %s is on %s. No worker started; seed ownership unchanged", msg.Checkout.Branch, repoRoot, branch)
		}
	case protocol.DelegateCheckoutKindNewWorktree, protocol.DelegateCheckoutKindExistingBranchWorktree:
		mainRepo := attngit.ResolveMainRepoPath(repoRoot)
		runtime.Worktree = &protocol.DelegateWorktreeRequest{Repo: protocol.Ptr(mainRepo), Branch: strings.TrimSpace(msg.Checkout.Branch), Path: msg.Checkout.Path}
		if msg.Checkout.Kind == protocol.DelegateCheckoutKindExistingBranchWorktree {
			if !attngit.RefExists(mainRepo, "refs/heads/"+runtime.Worktree.Branch) {
				return nil, fmt.Errorf("local branch %q does not exist; create it with --branch and --from when starting from a remote ref", runtime.Worktree.Branch)
			}
			runtime.Worktree.ExistingBranch = protocol.Ptr(true)
		} else {
			expectedPath := strings.TrimSpace(protocol.Deref(msg.Checkout.Path))
			if expectedPath == "" {
				expectedPath = attngit.GenerateWorktreePath(mainRepo, runtime.Worktree.Branch)
			}
			ownedRecovery := worktreeOwned && strings.TrimSpace(ownedWorktreePath) != "" &&
				attngit.CanonicalizePath(ownedWorktreePath) == attngit.CanonicalizePath(expectedPath)
			if attngit.RefExists(mainRepo, "refs/heads/"+runtime.Worktree.Branch) && !ownedRecovery {
				return nil, fmt.Errorf("branch %q already exists; use --existing-branch or choose another name", runtime.Worktree.Branch)
			}
			base := strings.TrimSpace(protocol.Deref(msg.Checkout.From))
			resolvedCommit := strings.TrimSpace(reservedBaseCommit)
			if resolvedCommit == "" {
				commit, err := attngit.Output(attngit.OpMetadata, mainRepo, "rev-parse", "--verify", base+"^{commit}")
				if err != nil || strings.TrimSpace(string(commit)) == "" {
					return nil, fmt.Errorf("base ref %q is unavailable; fetch it explicitly or choose another ref", base)
				}
				resolvedCommit = strings.TrimSpace(string(commit))
			}
			runtime.Worktree.StartingFrom = protocol.Ptr(resolvedCommit)
		}
	}
	return &runtime, nil
}
