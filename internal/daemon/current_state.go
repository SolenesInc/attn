package daemon

import "github.com/victorarias/attn/internal/protocol"

type currentStateProjection struct {
	Sessions    []protocol.Session
	Endpoints   []protocol.EndpointInfo
	Workspaces  []protocol.Workspace
	Prs         []protocol.PR
	Repos       []protocol.RepoState
	Authors     []protocol.AuthorState
	GithubHosts []string
	Seeds       []protocol.Seed
	Crew        []protocol.CrewMember
}

func (d *Daemon) currentStateProjection() currentStateProjection {
	return currentStateProjection{
		Sessions:    d.mergedSessionsForBroadcast(),
		Endpoints:   d.listEndpointInfos(),
		Workspaces:  d.listWorkspaces(),
		Prs:         protocol.PRsToValues(d.store.ListPRs("")),
		Repos:       protocol.RepoStatesToValues(d.store.ListRepoStates()),
		Authors:     protocol.AuthorStatesToValues(d.store.ListAuthorStates()),
		GithubHosts: d.gitHubHosts(),
		Seeds:       d.seedsForBroadcast(),
		Crew:        d.crewForBroadcast(),
	}
}
