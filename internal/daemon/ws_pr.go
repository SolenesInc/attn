package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleApprovePRWS(client *wsClient, msg *protocol.ApprovePRMessage) {
	d.life.Go("handleApprovePRWS", func() {
		err := d.approvePR(msg.ID)
		result := protocol.PRActionResultMessage{
			Event:   protocol.EventPRActionResult,
			Action:  "approve",
			ID:      msg.ID,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		}
		d.sendToClient(client, result)
		d.RefreshPRs()
	})
}

// approvePR approves the pull request id on GitHub and keeps the approval.
func (d *Daemon) approvePR(id string) error {
	d.logf("Processing approve for %s", id)
	ghClient, repo, number, _, err := d.clientForPRID(id)
	if err == nil {
		err = ghClient.ApprovePR(repo, number)
	}
	if err != nil {
		d.logf("Approve failed for %s: %v", id, err)
		return err
	}
	d.logf("Approve succeeded for %s", id)
	d.store.MarkPRApproved(id)
	d.store.SetPRHot(id)
	d.life.Go("fetchPRDetailsImmediate", func() { d.fetchPRDetailsImmediate(id) })
	return nil
}

func (d *Daemon) handleMergePRWS(client *wsClient, msg *protocol.MergePRMessage) {
	d.life.Go("handleMergePRWS", func() {
		err := d.mergePR(msg.ID, msg.Method)
		result := protocol.PRActionResultMessage{
			Event:   protocol.EventPRActionResult,
			Action:  "merge",
			ID:      msg.ID,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
		}
		d.sendToClient(client, result)
		d.RefreshPRs()
	})
}

// mergePR merges the pull request id on GitHub by method.
func (d *Daemon) mergePR(id, method string) error {
	ghClient, repo, number, _, err := d.clientForPRID(id)
	if err != nil {
		return err
	}
	return ghClient.MergePR(repo, number, method)
}

func (d *Daemon) handleMutePRWS(msg *protocol.MutePRMessage) {
	pr := d.store.GetPR(msg.ID)
	wasMuted := pr != nil && pr.Muted

	d.store.ToggleMutePR(msg.ID)

	d.coalesceSnapshots(func() {
		d.publishFact(FactPRMuteChanged, msg.ID, nil)
		if wasMuted {
			d.store.SetPRHot(msg.ID)
			d.life.Go("fetchPRDetailsImmediate", func() { d.fetchPRDetailsImmediate(msg.ID) })
			d.publishFact(FactPRHeatChanged, msg.ID, nil)
		}
	})
}

func (d *Daemon) handleMuteRepoWS(msg *protocol.MuteRepoMessage) {
	repoState := d.store.GetRepoState(msg.Repo)
	wasMuted := repoState != nil && repoState.Muted

	d.store.ToggleMuteRepo(msg.Repo)

	d.coalesceSnapshots(func() {
		if wasMuted {
			for _, pr := range d.store.ListPRsByRepo(msg.Repo) {
				d.store.SetPRHot(pr.ID)
				d.life.Go("fetchPRDetailsImmediate", func() { d.fetchPRDetailsImmediate(pr.ID) })
				d.publishFact(FactPRHeatChanged, pr.ID, nil)
			}
		}
		d.publishFact(FactRepoMuteChanged, msg.Repo, nil)
	})
}

func (d *Daemon) handleMuteAuthorWS(msg *protocol.MuteAuthorMessage) {
	d.store.ToggleMuteAuthor(msg.Author)
	d.publishFact(FactAuthorMuteChanged, msg.Author, nil)
}

func (d *Daemon) handleRefreshPRsWS(client *wsClient) {
	d.logf("Refreshing PRs on request")
	d.life.Go("handleRefreshPRsWS", func() {
		err := d.doRefreshPRsWithResult()
		result := protocol.RefreshPRsResultMessage{
			Event:   protocol.EventRefreshPRsResult,
			Success: err == nil,
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Refresh PRs failed: %v", err)
		} else {
			d.logf("Refresh PRs succeeded")
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handleFetchPRDetailsWS(client *wsClient, msg *protocol.FetchPRDetailsMessage) {
	d.logf("Fetching PR details")
	d.life.Go("handleFetchPRDetailsWS", func() {
		updatedPRs, err := d.fetchPRDetailsForID(msg.ID)
		result := protocol.WebSocketEvent{
			Event:   protocol.EventFetchPRDetailsResult,
			Success: protocol.Ptr(err == nil),
		}
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.logf("Fetch PR details failed: %v", err)
		} else {
			result.Prs = protocol.PRsToValues(updatedPRs)
			d.logf("Fetch PR details succeeded")
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) handlePRVisitedWS(msg *protocol.PRVisitedMessage) {
	d.logf("Marking PR %s as visited", msg.ID)
	d.store.MarkPRVisited(msg.ID)
	d.coalesceSnapshots(func() {
		d.publishFact(FactPRVisited, msg.ID, nil)
		if _, repo, _, err := protocol.ParsePRID(msg.ID); err == nil {
			for _, pr := range d.store.ListPRs("") {
				if pr.Repo == repo {
					d.store.SetPRHot(pr.ID)
					d.life.Go("fetchPRDetailsImmediate", func() { d.fetchPRDetailsImmediate(pr.ID) })
					d.publishFact(FactPRHeatChanged, pr.ID, nil)
				}
			}
		} else {
			d.store.SetPRHot(msg.ID)
			d.life.Go("fetchPRDetailsImmediate", func() { d.fetchPRDetailsImmediate(msg.ID) })
			d.publishFact(FactPRHeatChanged, msg.ID, nil)
		}
	})
}

func (d *Daemon) publishPRChanges(changes store.PRChanges) {
	d.coalesceSnapshots(func() {
		for _, id := range changes.Appeared {
			d.publishFact(FactPRAppeared, id, nil)
		}
		for _, id := range changes.Updated {
			d.publishFact(FactPRUpdated, id, nil)
		}
		for _, id := range changes.Disappeared {
			d.publishFact(FactPRDisappeared, id, nil)
		}
	})
}

func (d *Daemon) projectPRsUpdated() {
	d.projectSnapshot(snapshotPRs, func() {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event: protocol.EventPRsUpdated,
			Prs:   protocol.PRsToValues(d.store.ListPRs("")),
		})
	})
}

func (d *Daemon) projectRepoStatesUpdated() {
	d.projectSnapshot(snapshotRepos, func() {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event: protocol.EventReposUpdated,
			Repos: protocol.RepoStatesToValues(d.store.ListRepoStates()),
		})
	})
}

func (d *Daemon) projectAuthorStatesUpdated() {
	d.projectSnapshot(snapshotAuthors, func() {
		d.wsHub.Broadcast(&protocol.WebSocketEvent{
			Event:   protocol.EventAuthorsUpdated,
			Authors: protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		})
	})
}
