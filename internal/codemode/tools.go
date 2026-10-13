package codemode

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

type Caller struct {
	Client  *client.Client
	Session protocol.SessionID
}

type callerKey struct{}

func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerOf(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey{}).(Caller)
	return c
}

type Seed struct {
	ID     string
	Title  string
	Body   string
	State  string
	Tender string
	Plot   string
	Ready  bool
}

type Note struct {
	Author string
	Body   string
	At     string
}

type PlantOptions struct {
	Body string
	Plot string
}

type Agent struct {
	ID        string
	Name      string
	Harness   string
	State     string
	Directory string
}

func seedOf(s protocol.Seed) Seed {
	tender := string(s.TenderSession)
	if s.TenderMember != "" {
		tender = s.TenderMember
	}
	plot := ""
	for _, e := range s.Edges {
		if e.Kind == garden.EdgePartOf {
			plot = e.To
		}
	}
	return Seed{ID: s.ID, Title: s.Title, Body: s.Body, State: seedState(s.Status), Tender: tender, Plot: plot, Ready: s.Ready}
}

func seedState(status string) string {
	if status == "dormant" {
		return "parked"
	}
	return status
}

func seedsOf(in []protocol.Seed) []Seed {
	out := make([]Seed, len(in))
	for i, s := range in {
		out[i] = seedOf(s)
	}
	return out
}

func me(ctx context.Context) string { return string(callerOf(ctx).Session) }

func plant(ctx context.Context, title string, opts PlantOptions) (Seed, error) {
	c := callerOf(ctx)
	r, err := c.Client.SeedPlant(c.Session, title, opts.Body, opts.Plot, "", "")
	if err != nil {
		return Seed{}, err
	}
	return seedOf(r.Seed), nil
}

func show(ctx context.Context, id string) (Seed, []Note, error) {
	c := callerOf(ctx)
	r, err := c.Client.SeedShow(c.Session, id)
	if err != nil {
		return Seed{}, nil, err
	}
	notes := make([]Note, len(r.Notes))
	for i, n := range r.Notes {
		author := string(n.AuthorSession)
		if n.AuthorMember != "" {
			author = n.AuthorMember
		}
		notes[i] = Note{Author: author, Body: n.Body, At: n.CreatedAt}
	}
	slices.Reverse(notes)
	return seedOf(r.Seed), notes, nil
}

func seeds(ctx context.Context) ([]Seed, error) {
	c := callerOf(ctx)
	r, err := c.Client.SeedList(c.Session, false, 0)
	if err != nil {
		return nil, err
	}
	return seedsOf(r.Seeds), nil
}

func search(ctx context.Context, query string, limit int) ([]Seed, error) {
	c := callerOf(ctx)
	r, err := c.Client.SeedSearch(c.Session, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Seed, len(r.Hits))
	for i, h := range r.Hits {
		out[i] = seedOf(h.Seed)
	}
	return out, nil
}

func addNote(ctx context.Context, id, body string) error {
	c := callerOf(ctx)
	_, err := c.Client.SeedNote(c.Session, id, body, "", "", false, nil)
	return err
}

func transition(verb string) func(ctx context.Context, id, reason string) (Seed, error) {
	return func(ctx context.Context, id, reason string) (Seed, error) {
		c := callerOf(ctx)
		r, err := c.Client.SeedTransition(c.Session, id, verb, reason, "", false, client.SeedTransitionOptions{})
		if err != nil {
			return Seed{}, err
		}
		return seedOf(r.Seed), nil
	}
}

func tend(ctx context.Context, id string) (Seed, error) { return transition("tend")(ctx, id, "") }

func agents(ctx context.Context) ([]Agent, error) {
	sessions, err := callerOf(ctx).Client.Query("")
	if err != nil {
		return nil, err
	}
	out := make([]Agent, len(sessions))
	for i, s := range sessions {
		out[i] = Agent{ID: string(s.ID), Name: s.Label, Harness: s.Agent, State: string(s.State), Directory: s.Directory}
	}
	return out, nil
}

func message(ctx context.Context, to, body string) error {
	c := callerOf(ctx)
	if c.Session == "" {
		return errors.New("message needs a calling session; run the program from an agent's terminal")
	}
	r, err := c.Client.AgentMsg(to, c.Session, body)
	if err != nil {
		return err
	}
	if r.Status == protocol.AgentMsgStatusRefused {
		return fmt.Errorf("message to %s refused: %s", to, r.Detail)
	}
	return nil
}
