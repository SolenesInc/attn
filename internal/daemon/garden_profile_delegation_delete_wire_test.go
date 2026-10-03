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
				return profileRequest(app, protocol.ProfileDeleteMessage{Cmd: protocol.CmdProfileDelete, RequestID: id, ProfileID: side.ID, ExpectedRevision: side.Revision}, id)
			}
			deleted := remove()
			if deleted.Success || !strings.Contains(protocol.Deref(deleted.Error), "1 pending delegations") {
				t.Fatalf("pending operation must block deletion: %+v %s", deleted, protocol.Deref(deleted.Error))
			}
			if named {
				return
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
			if _, err := cli.AgentClose(source, source, "finished dispatching"); err != nil {
				t.Fatal(err)
			}

			if deleted := remove(); !deleted.Success {
				t.Fatalf("finished delegation must permit deletion: %+v %s", deleted, protocol.Deref(deleted.Error))
			}
		})
	}
}
