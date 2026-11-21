package daemon

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) prCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"pr record": command.Typed(func(ctx context.Context, c command.Caller, a command.PRRecordArgs) (any, error) {
			return d.runPRRecorded(c, a.URL, d.recordSessionPullRequest)
		}),
		"pr forget": command.Typed(func(ctx context.Context, c command.Caller, a command.PRForgetArgs) (any, error) {
			return d.runPRRecorded(c, a.URL, d.forgetSessionPullRequest)
		}),
		"pr watch":   command.Typed(d.runPRWatch),
		"pr unwatch": command.Typed(d.runPRUnwatch),
		"pr ls": command.Typed(func(ctx context.Context, c command.Caller, a command.PRLsArgs) (any, error) {
			return d.runPRList(c, a.Seed, func(protocol.SessionPullRequest) bool { return true })
		}),
		"pr status": command.Typed(func(ctx context.Context, c command.Caller, a command.PRStatusArgs) (any, error) {
			return d.runPRList(c, a.Seed, func(pr protocol.SessionPullRequest) bool {
				return protocol.Deref(pr.Watching) && (a.URL == "" || pr.URL == a.URL)
			})
		}),
	}
}

func prSession(c command.Caller) (protocol.SessionID, error) {
	if c.Session == "" {
		return "", command.Usage("no session — pass {session} <id>, or run inside an attn session")
	}
	return c.Session, nil
}

func (d *Daemon) prRecord(c command.Caller, url string) (store.SessionPullRequestRecord, error) {
	session, err := prSession(c)
	if err != nil {
		return store.SessionPullRequestRecord{}, err
	}
	return d.sessionPullRequestIdentity(session, session, url)
}

func (d *Daemon) runPRRecorded(c command.Caller, url string, change func(store.SessionPullRequestRecord) error) (any, error) {
	rec, err := d.prRecord(c, url)
	if err != nil {
		return nil, err
	}
	if err := change(rec); err != nil {
		return nil, err
	}
	return &protocol.PullRequestResult{SessionID: rec.SessionID, URL: rec.URL}, nil
}

func (d *Daemon) runPRWatch(_ context.Context, c command.Caller, a command.PRWatchArgs) (any, error) {
	mode, err := prreadiness.ParseMode(a.Mode)
	if err != nil {
		return nil, command.Refuse(command.CodeBadMode, "mode %q is invalid; use green, codex, or formal-review", a.Mode)
	}
	reviewer := strings.TrimSpace(a.Reviewer)
	if reviewer != "" && mode != prreadiness.ModeFormalReview {
		return nil, command.Refuse(command.CodeReviewerWithoutFormalReview, "{reviewer} is only valid with {mode} formal-review")
	}
	rec, err := d.prRecord(c, a.URL)
	if err != nil {
		return nil, err
	}
	seed := strings.TrimSpace(a.Seed)
	if err := d.watchSessionPullRequest(rec, seed, mode, reviewer); err != nil {
		return nil, err
	}
	return &protocol.PullRequestWatchResult{SessionID: rec.SessionID, URL: rec.URL, Mode: protocol.PullRequestWatchMode(mode), Reviewer: optionalText(reviewer), Seed: optionalText(seed)}, nil
}

func (d *Daemon) runPRUnwatch(_ context.Context, c command.Caller, a command.PRUnwatchArgs) (any, error) {
	rec, err := d.prRecord(c, a.URL)
	if err != nil {
		return nil, err
	}
	seed := strings.TrimSpace(a.Seed)
	if seed != "" {
		err = d.unwatchSeedPullRequest(rec, seed)
	} else {
		err = d.unwatchSessionPullRequest(rec)
	}
	if err != nil {
		return nil, err
	}
	return &protocol.PullRequestResult{SessionID: rec.SessionID, URL: rec.URL, Seed: optionalText(seed)}, nil
}

func (d *Daemon) runPRList(c command.Caller, seed string, keep func(protocol.SessionPullRequest) bool) (any, error) {
	session, err := prSession(c)
	if err != nil {
		return nil, err
	}
	seed = strings.TrimSpace(seed)
	listed, err := d.listPullRequests(session, session, seed)
	if err != nil {
		return nil, err
	}
	kept := []protocol.SessionPullRequest{}
	for _, pr := range listed {
		if keep(pr) {
			kept = append(kept, pr)
		}
	}
	return &protocol.PullRequestListResult{SessionID: session, Seed: optionalText(seed), PullRequests: kept}, nil
}
