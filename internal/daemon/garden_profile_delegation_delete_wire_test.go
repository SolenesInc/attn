package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestProfileDeletionAccountsForDelegationsStillPreparingTheirCheckout(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "named"}[named], func(t *testing.T) {
			if named {
				t.Setenv("ATTN_INSTANCE", "scope-delete")
			}
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			home := app.SelectedProfile()
			side := createProfile(app, "Side")
			selectProfile(app, side.ID)
			repo := newRepo(t, "shop")
			root, err := filepath.EvalSymlinks(filepath.Dir(repo))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "pending-checkout")
			base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
			source := w.Spawn(w.AppOn(side.ID), fakeagent.Codex, repo)
			if queriedSession(t, cli, source).ProfileID != side.ID {
				t.Fatal("delegation source must belong to Side")
			}
			gate := newMaintenanceGitGate(t, "worktree add -b pending "+path+" "+base)
			gate.arm(t)
			request := delegateCheckoutAt(repo, delegateNewWorktree("pending", "main"))
			request.RequestID = uuid.NewString()
			request.SourceSessionID = protocol.Ptr(source)
			request.Checkout.Path = protocol.Ptr(path)
			scoped := cli.WithGardenProfile(side.ID, source)
			accepted, err := scoped.StartDelegation(request)
			if err != nil {
				t.Fatal(err)
			}
			gate.awaitBlocked(t)
			released := false
			defer func() {
				if !released {
					gate.release(t)
				}
			}()
			listed, err := scoped.SeedList(source, false, 0)
			if err != nil || len(listed.Seeds) != 0 {
				t.Fatalf("checkout preparation must precede seed creation: %+v %v", listed, err)
			}
			pending, err := scoped.DelegationStatus(accepted.OperationID)
			if err != nil || pending.State != protocol.DelegationOperationStatePreparing {
				t.Fatalf("accepted operation remains inspectable: %+v %v", pending, err)
			}
			remove := func() protocol.ProfileActionResultMessage {
				for _, current := range w.AppOn(side.ID).Initial.Profiles {
					if current.ID == side.ID {
						side = current
					}
				}
				id := uuid.NewString()
				return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: home}, id)
			}
			deleted := remove()
			if named {
				if !deleted.Success {
					t.Fatalf("named deletion bypass: %+v %s", deleted, protocol.Deref(deleted.Error))
				}
				return
			}
			if deleted.Success || !strings.Contains(protocol.Deref(deleted.Error), "0 open seeds and 0 live dispatched sessions, plus 1 pending delegations") || !strings.Contains(protocol.Deref(deleted.Error), "wait for the delegations to finish") {
				t.Fatalf("pending operation must block production deletion: %+v %s", deleted, protocol.Deref(deleted.Error))
			}
			if owner := queriedSession(t, cli, source).ProfileID; owner != side.ID {
				t.Fatalf("refused deletion moved the source to %s", owner)
			}
			gate.release(t)
			released = true
			worker, err := scoped.Delegate(request)
			if err != nil {
				t.Fatal(err)
			}
			lifeMove(t, scoped, worker.SessionID, worker.SeedID, "harvest", "finished", "")
			if _, err := cli.AgentClose(worker.SessionID, worker.SessionID, "finished my work"); err != nil {
				t.Fatal(err)
			}
			if deleted := remove(); !deleted.Success {
				t.Fatalf("finished delegation must permit deletion: %+v %s", deleted, protocol.Deref(deleted.Error))
			}
		})
	}
}

func TestDelegationCannotBeAcceptedIntoAProfileDeletedWhileResolvingItsCheckout(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	home := app.SelectedProfile()
	side := createProfile(app, "Side")
	selectProfile(app, side.ID)
	repo := newRepo(t, "shop")
	source := w.Spawn(w.AppOn(side.ID), fakeagent.Codex, repo)
	if queriedSession(t, cli, source).ProfileID != side.ID {
		t.Fatal("delegation source must belong to Side")
	}
	gate := newMaintenanceGitGate(t, "rev-parse --verify main^{commit}")
	gate.arm(t)
	request := delegateCheckoutAt(repo, delegateNewWorktree("pending", "main"))
	request.RequestID = uuid.NewString()
	request.SourceSessionID = protocol.Ptr(source)
	done := make(chan error, 1)
	go func() {
		_, err := cli.WithGardenProfile(side.ID, source).StartDelegation(request)
		done <- err
	}()
	gate.awaitBlocked(t)
	id := uuid.NewString()
	deleted := profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision, DestinationProfileID: home}, id)
	gate.release(t)
	if !deleted.Success {
		t.Fatalf("unaccepted request must not reserve the profile: %+v %s", deleted, protocol.Deref(deleted.Error))
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("request must refuse its deleted owning profile before acceptance: %v", err)
	}
}
