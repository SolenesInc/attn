package daemon

import "github.com/victorarias/attn/internal/protocol"

type currentStateProjection struct {
	Sessions    []protocol.Session
	Prs         []protocol.PR
	Repos       []protocol.RepoState
	Authors     []protocol.AuthorState
	GithubHosts []string
	Crew        []protocol.CrewMember
}

func (d *Daemon) currentStateProjection() currentStateProjection {
	return currentStateProjection{
		Sessions:    d.sessionsForBroadcast(d.store.List("")),
		Prs:         protocol.PRsToValues(d.store.ListPRs("")),
		Repos:       protocol.RepoStatesToValues(d.store.ListRepoStates()),
		Authors:     protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		GithubHosts: d.gitHubHosts(),
		Crew:        nil,
	}
}
