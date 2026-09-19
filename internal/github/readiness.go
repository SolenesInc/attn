package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/prreadiness"
)

var ErrReadinessTruncated = errors.New("pull request readiness exceeds a GitHub page")
var ErrReadinessHeadChanged = errors.New("pull request head changed while reading readiness")

type PullRequestReadiness struct {
	Snapshot *PullRequestSnapshot
	Evidence prreadiness.Evidence
}

type readinessPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type readinessComment struct {
	ID           string    `json:"id"`
	BodyText     string    `json:"bodyText"`
	CreatedAt    time.Time `json:"createdAt"`
	Path         string    `json:"path"`
	Line         *int      `json:"line"`
	OriginalLine *int      `json:"originalLine"`
	Author       struct {
		TypeName string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
}

type readinessComments struct {
	PageInfo readinessPageInfo  `json:"pageInfo"`
	Nodes    []readinessComment `json:"nodes"`
}

type readinessReview struct {
	ID          string    `json:"id"`
	State       string    `json:"state"`
	BodyText    string    `json:"bodyText"`
	SubmittedAt time.Time `json:"submittedAt"`
	Author      struct {
		TypeName string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
	Commit struct {
		OID string `json:"oid"`
	} `json:"commit"`
	Comments readinessComments `json:"comments"`
}

type readinessCheck struct {
	TypeName   string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Context    string `json:"context"`
	State      string `json:"state"`
}

type readinessReaction struct {
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

type readinessThread struct {
	ID         string            `json:"id"`
	IsResolved bool              `json:"isResolved"`
	Comments   readinessComments `json:"comments"`
}

type readinessPullRequest struct {
	Number           int    `json:"number"`
	URL              string `json:"url"`
	Title            string `json:"title"`
	BodyText         string `json:"bodyText"`
	IsDraft          bool   `json:"isDraft"`
	State            string `json:"state"`
	Merged           bool   `json:"merged"`
	MergeStateStatus string `json:"mergeStateStatus"`
	HeadRefOID       string `json:"headRefOid"`
	HeadRefName      string `json:"headRefName"`
	BaseRefOID       string `json:"baseRefOid"`
	BaseRefName      string `json:"baseRefName"`
	Author           struct {
		Login string `json:"login"`
	} `json:"author"`
	HeadRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	BaseRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"baseRepository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						PageInfo readinessPageInfo `json:"pageInfo"`
						Nodes    []readinessCheck  `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Reviews struct {
		PageInfo readinessPageInfo `json:"pageInfo"`
		Nodes    []readinessReview `json:"nodes"`
	} `json:"reviews"`
	Comments struct {
		PageInfo readinessPageInfo  `json:"pageInfo"`
		Nodes    []readinessComment `json:"nodes"`
	} `json:"comments"`
	Reactions struct {
		PageInfo readinessPageInfo   `json:"pageInfo"`
		Nodes    []readinessReaction `json:"nodes"`
	} `json:"reactions"`
	ReviewThreads struct {
		PageInfo readinessPageInfo `json:"pageInfo"`
		Nodes    []readinessThread `json:"nodes"`
	} `json:"reviewThreads"`
}

type readinessResponse struct {
	Data struct {
		Repository struct {
			PullRequest *readinessPullRequest `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

const pullRequestReadinessQuery = `
query($owner:String!,$name:String!,$number:Int!,$checkCursor:String,$reviewCursor:String,$commentCursor:String,$reactionCursor:String,$threadCursor:String){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    number url title bodyText isDraft state merged mergeStateStatus headRefOid headRefName
    author{login} baseRefOid baseRefName
    headRepository{nameWithOwner} baseRepository{nameWithOwner}
    commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100,after:$checkCursor){
      pageInfo{hasNextPage endCursor} nodes{__typename ... on CheckRun{name status conclusion} ... on StatusContext{context state}}
    }}}}}
    reviews(first:100,after:$reviewCursor){pageInfo{hasNextPage endCursor} nodes{id state bodyText submittedAt author{__typename login} commit{oid}
      comments(first:100){pageInfo{hasNextPage endCursor} nodes{id bodyText createdAt path line originalLine author{__typename login}}}}}
    comments(first:100,after:$commentCursor){pageInfo{hasNextPage endCursor} nodes{id bodyText createdAt author{__typename login}}}
    reactions(first:100,after:$reactionCursor){pageInfo{hasNextPage endCursor} nodes{content createdAt user{login}}}
    reviewThreads(first:100,after:$threadCursor){pageInfo{hasNextPage endCursor} nodes{id isResolved comments(first:1){
      nodes{id bodyText createdAt path line originalLine author{__typename login}}
    }}}
  }}}
`

const pullRequestReviewCommentsQuery = `
query($owner:String!,$name:String!,$number:Int!,$id:ID!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){headRefOid}}
  node(id:$id){... on PullRequestReview{comments(first:100,after:$cursor){
    pageInfo{hasNextPage endCursor} nodes{id bodyText createdAt path line originalLine author{__typename login}}
  }}}
}`

func (c *Client) FetchPullRequestReadiness(repo string, number int, reviewer string) (*PullRequestReadiness, error) {
	owner, name, ok := strings.Cut(strings.Trim(repo, "/"), "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("invalid repository %q", repo)
	}
	variables := map[string]any{"owner": owner, "name": name, "number": number}
	var combined *readinessPullRequest
	for page := 0; ; page++ {
		if page == 100 {
			return nil, ErrReadinessTruncated
		}
		body, err := c.doRequest("POST", c.graphQLURL(), map[string]any{
			"query": pullRequestReadinessQuery, "variables": variables,
		})
		if err != nil {
			return nil, fmt.Errorf("fetch pull request readiness: %w", err)
		}
		current, err := decodePullRequestReadiness(body)
		if err != nil {
			return nil, err
		}
		if combined == nil {
			combined = current
		} else {
			if current.HeadRefOID != combined.HeadRefOID {
				return nil, ErrReadinessHeadChanged
			}
			mergeReadinessPage(combined, current)
		}
		more, err := advanceReadinessCursors(variables, current)
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
	}
	for i := range combined.Reviews.Nodes {
		if err := c.fetchRemainingReviewComments(owner, name, number, combined.HeadRefOID, &combined.Reviews.Nodes[i]); err != nil {
			return nil, err
		}
	}
	return buildPullRequestReadiness(combined, reviewer)
}

func (c *Client) fetchRemainingReviewComments(owner, name string, number int, head string, review *readinessReview) error {
	for page := 0; review.Comments.PageInfo.HasNextPage; page++ {
		if page == 100 || review.Comments.PageInfo.EndCursor == "" {
			return ErrReadinessTruncated
		}
		body, err := c.doRequest("POST", c.graphQLURL(), map[string]any{
			"query":     pullRequestReviewCommentsQuery,
			"variables": map[string]any{"owner": owner, "name": name, "number": number, "id": review.ID, "cursor": review.Comments.PageInfo.EndCursor},
		})
		if err != nil {
			return fmt.Errorf("fetch pull request review comments: %w", err)
		}
		var payload struct {
			Data struct {
				Repository struct {
					PullRequest *struct {
						HeadRefOID string `json:"headRefOid"`
					} `json:"pullRequest"`
				} `json:"repository"`
				Node *struct {
					Comments readinessComments `json:"comments"`
				} `json:"node"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("parse pull request review comments: %w", err)
		}
		if len(payload.Errors) > 0 {
			return errors.New(payload.Errors[0].Message)
		}
		if payload.Data.Repository.PullRequest == nil || payload.Data.Node == nil {
			return errors.New("GitHub returned no pull request review comments")
		}
		if payload.Data.Repository.PullRequest.HeadRefOID != head {
			return ErrReadinessHeadChanged
		}
		review.Comments.Nodes = append(review.Comments.Nodes, payload.Data.Node.Comments.Nodes...)
		review.Comments.PageInfo = payload.Data.Node.Comments.PageInfo
	}
	return nil
}

func advanceReadinessCursors(variables map[string]any, pr *readinessPullRequest) (bool, error) {
	connections := []struct {
		name string
		info readinessPageInfo
	}{
		{"reviewCursor", pr.Reviews.PageInfo},
		{"commentCursor", pr.Comments.PageInfo},
		{"reactionCursor", pr.Reactions.PageInfo},
		{"threadCursor", pr.ReviewThreads.PageInfo},
	}
	if len(pr.Commits.Nodes) > 0 && pr.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
		connections = append(connections, struct {
			name string
			info readinessPageInfo
		}{"checkCursor", pr.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts.PageInfo})
	}
	more := false
	for _, connection := range connections {
		if connection.info.EndCursor != "" {
			variables[connection.name] = connection.info.EndCursor
		}
		if !connection.info.HasNextPage {
			continue
		}
		if connection.info.EndCursor == "" {
			return false, ErrReadinessTruncated
		}
		more = true
	}
	return more, nil
}

func mergeReadinessPage(target, page *readinessPullRequest) {
	target.Reviews.Nodes = append(target.Reviews.Nodes, page.Reviews.Nodes...)
	target.Comments.Nodes = append(target.Comments.Nodes, page.Comments.Nodes...)
	target.Reactions.Nodes = append(target.Reactions.Nodes, page.Reactions.Nodes...)
	target.ReviewThreads.Nodes = append(target.ReviewThreads.Nodes, page.ReviewThreads.Nodes...)
	if len(target.Commits.Nodes) > 0 && len(page.Commits.Nodes) > 0 {
		targetRollup := target.Commits.Nodes[0].Commit.StatusCheckRollup
		pageRollup := page.Commits.Nodes[0].Commit.StatusCheckRollup
		if targetRollup != nil && pageRollup != nil {
			targetRollup.Contexts.Nodes = append(targetRollup.Contexts.Nodes, pageRollup.Contexts.Nodes...)
		}
	}
}

func (c *Client) graphQLURL() string {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return c.baseURL + "/graphql"
	}
	if strings.HasSuffix(parsed.Path, "/api/v3") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/api/v3") + "/api/graphql"
	} else {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/graphql"
	}
	return parsed.String()
}

func (c readinessComment) location() string {
	if c.Path == "" {
		return ""
	}
	line := c.Line
	if line == nil {
		line = c.OriginalLine
	}
	if line == nil {
		return c.Path
	}
	return fmt.Sprintf("%s:%d", c.Path, *line)
}

func decodePullRequestReadiness(body []byte) (*readinessPullRequest, error) {
	var payload readinessResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse pull request readiness: %w", err)
	}
	if len(payload.Errors) > 0 {
		return nil, errors.New(payload.Errors[0].Message)
	}
	if payload.Data.Repository.PullRequest == nil || payload.Data.Repository.PullRequest.HeadRefOID == "" {
		return nil, errors.New("GitHub returned no pull request head")
	}
	return payload.Data.Repository.PullRequest, nil
}

func parsePullRequestReadiness(body []byte, reviewer string) (*PullRequestReadiness, error) {
	pr, err := decodePullRequestReadiness(body)
	if err != nil {
		return nil, err
	}
	if hasMoreReadinessPages(pr) {
		return nil, ErrReadinessTruncated
	}
	return buildPullRequestReadiness(pr, reviewer)
}

func hasMoreReadinessPages(pr *readinessPullRequest) bool {
	if pr.Reviews.PageInfo.HasNextPage || pr.Comments.PageInfo.HasNextPage ||
		pr.Reactions.PageInfo.HasNextPage || pr.ReviewThreads.PageInfo.HasNextPage {
		return true
	}
	if len(pr.Commits.Nodes) > 0 && pr.Commits.Nodes[0].Commit.StatusCheckRollup != nil &&
		pr.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts.PageInfo.HasNextPage {
		return true
	}
	for _, review := range pr.Reviews.Nodes {
		if review.Comments.PageInfo.HasNextPage {
			return true
		}
	}
	return false
}

func buildPullRequestReadiness(pr *readinessPullRequest, reviewer string) (*PullRequestReadiness, error) {
	mergeableState := strings.ToLower(pr.MergeStateStatus)
	evidence := prreadiness.Evidence{
		State: strings.ToLower(pr.State), Draft: pr.IsDraft, MergeableState: mergeableState,
		HeadSHA: pr.HeadRefOID,
	}
	if len(pr.Commits.Nodes) > 0 {
		commit := pr.Commits.Nodes[0].Commit
		if commit.StatusCheckRollup != nil {
			evidence.CheckState = prreadiness.ChecksGreen
			if len(commit.StatusCheckRollup.Contexts.Nodes) == 0 {
				evidence.CheckState = prreadiness.ChecksNone
			}
			for _, check := range commit.StatusCheckRollup.Contexts.Nodes {
				name := check.Context
				state := readinessStatusState(check.State)
				if check.TypeName == "CheckRun" {
					name = check.Name
					state = readinessCheckRunState(check.Status, check.Conclusion)
				}
				if state == prreadiness.ChecksFailed {
					evidence.CheckState = prreadiness.ChecksFailed
					evidence.FailedChecks = append(evidence.FailedChecks, name)
				} else if state != prreadiness.ChecksGreen && evidence.CheckState != prreadiness.ChecksFailed {
					evidence.CheckState = prreadiness.ChecksPending
				}
			}
		}
	}
	for _, review := range pr.Reviews.Nodes {
		item := prreadiness.Review{ID: review.ID, Author: review.Author.Login, State: review.State,
			Body: review.BodyText, CommitOID: review.Commit.OID, SubmittedAt: review.SubmittedAt}
		if strings.TrimSpace(review.BodyText) != "" {
			evidence.Comments = append(evidence.Comments, prreadiness.Comment{
				ID: review.ID, Author: review.Author.Login, Body: review.BodyText,
				CreatedAt: review.SubmittedAt, Bot: review.Author.TypeName != "User",
			})
		}
		for _, comment := range review.Comments.Nodes {
			item.Findings = append(item.Findings, prreadiness.Finding{
				ID: comment.ID, Author: comment.Author.Login, Body: comment.BodyText, Location: comment.location(),
			})
			evidence.Comments = append(evidence.Comments, prreadiness.Comment{
				ID: comment.ID, Author: comment.Author.Login, Body: comment.BodyText,
				CreatedAt: comment.CreatedAt, Bot: comment.Author.TypeName != "User",
			})
		}
		evidence.Reviews = append(evidence.Reviews, item)
	}
	for _, comment := range pr.Comments.Nodes {
		evidence.Comments = append(evidence.Comments, prreadiness.Comment{
			ID: comment.ID, Author: comment.Author.Login, Body: comment.BodyText, CreatedAt: comment.CreatedAt,
			Bot: comment.Author.TypeName != "User",
		})
	}
	for _, reaction := range pr.Reactions.Nodes {
		evidence.Reactions = append(evidence.Reactions, prreadiness.Reaction{
			Author: reaction.User.Login, Content: reaction.Content, CreatedAt: reaction.CreatedAt,
		})
	}
	for _, thread := range pr.ReviewThreads.Nodes {
		if len(thread.Comments.Nodes) == 0 {
			continue
		}
		comment := thread.Comments.Nodes[0]
		evidence.Threads = append(evidence.Threads, prreadiness.Thread{
			ID: thread.ID, Author: comment.Author.Login, Resolved: thread.IsResolved,
			Body: comment.BodyText, Location: comment.location(),
		})
	}
	sort.Strings(evidence.FailedChecks)
	return &PullRequestReadiness{
		Snapshot: &PullRequestSnapshot{
			Number: pr.Number, URL: pr.URL, Title: pr.Title, Body: pr.BodyText,
			Author: pr.Author.Login, Draft: pr.IsDraft, State: strings.ToLower(pr.State), Merged: pr.Merged,
			MergeableState: mergeableState, HeadSHA: pr.HeadRefOID, HeadRef: pr.HeadRefName,
			HeadRepository: pr.HeadRepository.NameWithOwner, BaseSHA: pr.BaseRefOID,
			BaseRef: pr.BaseRefName, BaseRepository: pr.BaseRepository.NameWithOwner,
		},
		Evidence: evidence,
	}, nil
}

func readinessCheckRunState(status, conclusion string) string {
	if !strings.EqualFold(status, "COMPLETED") {
		return prreadiness.ChecksPending
	}
	switch strings.ToUpper(conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return prreadiness.ChecksGreen
	case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return prreadiness.ChecksFailed
	default:
		return prreadiness.ChecksPending
	}
}

func readinessStatusState(state string) string {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return prreadiness.ChecksGreen
	case "FAILURE", "ERROR":
		return prreadiness.ChecksFailed
	default:
		return prreadiness.ChecksPending
	}
}
