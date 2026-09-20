package github

import (
	"context"
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

type QueryTransport interface {
	GraphQL(context.Context, string, map[string]any) ([]byte, error)
}

type readinessPageInfo struct {
	HasNextPage     bool   `json:"hasNextPage"`
	HasPreviousPage bool   `json:"hasPreviousPage"`
	EndCursor       string `json:"endCursor"`
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
	PullRequestReview *struct {
		Commit struct {
			OID string `json:"oid"`
		} `json:"commit"`
	} `json:"pullRequestReview"`
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
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
	Context    string `json:"context"`
	State      string `json:"state"`
}

type readinessReaction struct {
	ID        string    `json:"id"`
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
	ReviewRequests struct {
		PageInfo readinessPageInfo `json:"pageInfo"`
		Nodes    []struct {
			RequestedReviewer struct {
				TypeName string `json:"__typename"`
				Login    string `json:"login"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
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
query($owner:String!,$name:String!,$number:Int!,$requestCursor:String,$checkCursor:String,$reviewCursor:String,$commentCursor:String,$reactionCursor:String,$threadCursor:String){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    number url title bodyText isDraft state merged mergeStateStatus headRefOid headRefName
    author{login} baseRefOid baseRefName
    headRepository{nameWithOwner} baseRepository{nameWithOwner}
    reviewRequests(first:100,after:$requestCursor){pageInfo{hasNextPage endCursor} nodes{requestedReviewer{__typename ... on User{login}}}}
    commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100,after:$checkCursor){
      pageInfo{hasNextPage endCursor} nodes{__typename ... on CheckRun{name status conclusion detailsUrl} ... on StatusContext{context state targetUrl}}
    }}}}}
    reviews(first:100,after:$reviewCursor){pageInfo{hasNextPage endCursor} nodes{id state bodyText submittedAt author{__typename login} commit{oid}
      comments(first:100){pageInfo{hasNextPage endCursor} nodes{id bodyText createdAt path line originalLine author{__typename login}}}}}
    comments(first:100,after:$commentCursor){pageInfo{hasNextPage endCursor} nodes{id bodyText createdAt author{__typename login}}}
		reactions(first:100,after:$reactionCursor){pageInfo{hasNextPage endCursor} nodes{id content createdAt user{login}}}
    reviewThreads(first:100,after:$threadCursor){pageInfo{hasNextPage endCursor} nodes{id isResolved comments(first:1){
      nodes{id bodyText createdAt path line originalLine author{__typename login} pullRequestReview{commit{oid}}}
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

func FetchPullRequestReadiness(ctx context.Context, transport QueryTransport, repo string, number int) (*prreadiness.Observation, error) {
	owner, name, ok := strings.Cut(strings.Trim(repo, "/"), "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("invalid repository %q", repo)
	}
	variables := map[string]any{"owner": owner, "name": name, "number": number}
	var combined *readinessPullRequest
	for {
		body, err := transport.GraphQL(ctx, pullRequestReadinessQuery, variables)
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
		if err := fetchRemainingReviewComments(ctx, transport, owner, name, number, combined.HeadRefOID, &combined.Reviews.Nodes[i]); err != nil {
			return nil, err
		}
	}
	return buildPullRequestReadiness(combined), nil
}

func (c *Client) FetchPullRequestReadiness(repo string, number int) (*prreadiness.Observation, error) {
	return FetchPullRequestReadiness(context.Background(), c, repo, number)
}

func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any) ([]byte, error) {
	return c.doRequestContext(ctx, "POST", c.graphQLURL(), map[string]any{"query": query, "variables": variables})
}

func fetchRemainingReviewComments(
	ctx context.Context,
	transport QueryTransport,
	owner, name string,
	number int,
	head string,
	review *readinessReview,
) error {
	for review.Comments.PageInfo.HasNextPage {
		cursor := review.Comments.PageInfo.EndCursor
		if cursor == "" {
			return ErrReadinessTruncated
		}
		body, err := transport.GraphQL(ctx, pullRequestReviewCommentsQuery, map[string]any{
			"owner": owner, "name": name, "number": number, "id": review.ID, "cursor": cursor,
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
		if review.Comments.PageInfo.HasNextPage && review.Comments.PageInfo.EndCursor == cursor {
			return ErrReadinessTruncated
		}
	}
	return nil
}

func advanceReadinessCursors(variables map[string]any, pr *readinessPullRequest) (bool, error) {
	connections := []struct {
		name string
		info readinessPageInfo
	}{
		{"requestCursor", pr.ReviewRequests.PageInfo},
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
		previous, _ := variables[connection.name].(string)
		if connection.info.EndCursor != "" {
			variables[connection.name] = connection.info.EndCursor
		}
		if !connection.info.HasNextPage {
			continue
		}
		if connection.info.EndCursor == "" {
			return false, ErrReadinessTruncated
		}
		if connection.info.EndCursor == previous {
			return false, ErrReadinessTruncated
		}
		more = true
	}
	return more, nil
}

func mergeReadinessPage(target, page *readinessPullRequest) {
	target.ReviewRequests.Nodes = append(target.ReviewRequests.Nodes, page.ReviewRequests.Nodes...)
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

func ParsePullRequestReadiness(body []byte) (*prreadiness.Observation, error) {
	pr, err := decodePullRequestReadiness(body)
	if err != nil {
		return nil, err
	}
	if hasMoreReadinessPages(pr) {
		return nil, fmt.Errorf("%w; readiness cannot be verified without truncation (100-item verification window)", ErrReadinessTruncated)
	}
	return buildPullRequestReadiness(pr), nil
}

func hasMoreReadinessPages(pr *readinessPullRequest) bool {
	if pr.ReviewRequests.PageInfo.HasPreviousPage || pr.Reviews.PageInfo.HasPreviousPage || pr.Comments.PageInfo.HasPreviousPage ||
		pr.ReviewRequests.PageInfo.HasNextPage ||
		pr.Reviews.PageInfo.HasNextPage || pr.Comments.PageInfo.HasNextPage ||
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

func buildPullRequestReadiness(pr *readinessPullRequest) *prreadiness.Observation {
	result := &prreadiness.Observation{
		Number: pr.Number, URL: pr.URL, Title: pr.Title, State: strings.ToLower(pr.State),
		HeadSHA: pr.HeadRefOID, HeadRef: pr.HeadRefName, Draft: pr.IsDraft, Merged: pr.Merged,
		MergeableState: strings.ToLower(pr.MergeStateStatus), CheckState: prreadiness.ChecksNone,
	}
	for _, request := range pr.ReviewRequests.Nodes {
		if request.RequestedReviewer.TypeName == "User" {
			result.RequestedReviewers = append(result.RequestedReviewers, request.RequestedReviewer.Login)
		}
	}
	if len(pr.Commits.Nodes) > 0 {
		commit := pr.Commits.Nodes[0].Commit
		if commit.StatusCheckRollup != nil {
			result.CheckState = prreadiness.ChecksGreen
			if len(commit.StatusCheckRollup.Contexts.Nodes) == 0 {
				result.CheckState = prreadiness.ChecksNone
			}
			for _, check := range commit.StatusCheckRollup.Contexts.Nodes {
				label, url := "status:"+check.Context, check.TargetURL
				state := readinessStatusState(check.State)
				if check.TypeName == "CheckRun" {
					label, url = "check:"+check.Name, check.DetailsURL
					state = readinessCheckRunState(check.Status, check.Conclusion)
				}
				result.Checks = append(result.Checks, prreadiness.Check{Name: label, State: state, URL: url})
				if state == prreadiness.ChecksFailed {
					result.CheckState = prreadiness.ChecksFailed
				} else if state != prreadiness.ChecksGreen && result.CheckState != prreadiness.ChecksFailed {
					result.CheckState = prreadiness.ChecksPending
				}
			}
		}
	}
	addComment := func(comment readinessComment, kind, reviewState string) {
		item := prreadiness.Comment{
			ID: comment.ID, Author: comment.Author.Login, Body: comment.BodyText,
			Kind: kind, ReviewState: reviewState,
			Location:  comment.location(),
			CreatedAt: comment.CreatedAt, Bot: comment.Author.TypeName != "User",
		}
		result.Comments = append(result.Comments, item)
	}
	for _, review := range pr.Reviews.Nodes {
		item := prreadiness.Review{ID: review.ID, Author: review.Author.Login, State: review.State,
			Body: review.BodyText, CommitOID: review.Commit.OID, SubmittedAt: review.SubmittedAt}
		if strings.TrimSpace(review.BodyText) != "" {
			addComment(readinessComment{
				ID: review.ID, Author: review.Author, BodyText: review.BodyText, CreatedAt: review.SubmittedAt,
			}, "review", strings.ToUpper(review.State))
		}
		for _, comment := range review.Comments.Nodes {
			item.Findings = append(item.Findings, prreadiness.Finding{
				ID: comment.ID, Author: comment.Author.Login, Body: comment.BodyText, Location: comment.location(),
			})
			addComment(comment, "inline", "")
		}
		result.Reviews = append(result.Reviews, item)
	}
	for _, comment := range pr.Comments.Nodes {
		addComment(comment, "issue", "")
	}
	for _, reaction := range pr.Reactions.Nodes {
		result.Reactions = append(result.Reactions, prreadiness.Reaction{
			ID: reaction.ID, Author: reaction.User.Login, Content: reaction.Content, CreatedAt: reaction.CreatedAt,
		})
	}
	for _, thread := range pr.ReviewThreads.Nodes {
		if len(thread.Comments.Nodes) == 0 {
			continue
		}
		comment := thread.Comments.Nodes[0]
		commitOID := ""
		if comment.PullRequestReview != nil {
			commitOID = comment.PullRequestReview.Commit.OID
		}
		result.Threads = append(result.Threads, prreadiness.Thread{
			ID: comment.ID, Author: comment.Author.Login, Resolved: thread.IsResolved,
			Body: comment.BodyText, Location: comment.location(), CommitOID: commitOID,
		})
	}
	sort.Slice(result.Checks, func(i, j int) bool { return result.Checks[i].Name < result.Checks[j].Name })
	return result
}

func readinessCheckRunState(status, conclusion string) prreadiness.CheckState {
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

func readinessStatusState(state string) prreadiness.CheckState {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return prreadiness.ChecksGreen
	case "FAILURE", "ERROR":
		return prreadiness.ChecksFailed
	default:
		return prreadiness.ChecksPending
	}
}
