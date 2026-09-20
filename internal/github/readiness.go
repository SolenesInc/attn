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

type QueryTransport interface {
	GraphQL(context.Context, string, map[string]any) ([]byte, error)
}

type readinessPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type readinessActor struct {
	TypeName string `json:"__typename"`
	ID       string `json:"id"`
	Login    string `json:"login"`
}

type readinessReaction struct {
	ID      string         `json:"id"`
	Content string         `json:"content"`
	User    readinessActor `json:"user"`
}

type readinessReview struct {
	ID          string         `json:"id"`
	State       string         `json:"state"`
	BodyText    string         `json:"bodyText"`
	SubmittedAt time.Time      `json:"submittedAt"`
	Author      readinessActor `json:"author"`
}

type readinessCheck struct {
	TypeName   string `json:"__typename"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
}

type readinessConnection[T any] struct {
	Nodes    []T               `json:"nodes"`
	PageInfo readinessPageInfo `json:"pageInfo"`
}

type readinessPullRequest struct {
	Number           int                                    `json:"number"`
	URL              string                                 `json:"url"`
	Title            string                                 `json:"title"`
	State            string                                 `json:"state"`
	IsDraft          bool                                   `json:"isDraft"`
	Merged           bool                                   `json:"merged"`
	HeadRefOID       string                                 `json:"headRefOid"`
	HeadRefName      string                                 `json:"headRefName"`
	MergeStateStatus string                                 `json:"mergeStateStatus"`
	ReviewDecision   string                                 `json:"reviewDecision"`
	Reactions        readinessConnection[readinessReaction] `json:"reactions"`
	LatestOpinions   readinessConnection[readinessReview]   `json:"latestOpinionatedReviews"`
	Reviews          readinessConnection[readinessReview]   `json:"reviews"`
	Commits          struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts readinessConnection[readinessCheck] `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

const pullRequestReadinessQuery = `
query PullRequestReadiness($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    number url title state isDraft merged headRefOid headRefName mergeStateStatus reviewDecision
    reactions(first:100){nodes{id content user{__typename id login}} pageInfo{hasNextPage endCursor}}
    latestOpinionatedReviews(first:100){nodes{id state bodyText submittedAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
    reviews(first:100){nodes{id state bodyText submittedAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
    commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100){
      nodes{__typename ... on CheckRun{id name status conclusion detailsUrl} ... on StatusContext{id context state targetUrl}}
      pageInfo{hasNextPage endCursor}
    }}}}}
  }}}
}`

const pullRequestReactionPageQuery = `
query PullRequestReactions($owner:String!,$name:String!,$number:Int!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    headRefOid
    reactions(first:100,after:$cursor){nodes{id content user{__typename id login}} pageInfo{hasNextPage endCursor}}
  }}}
}`

const pullRequestOpinionPageQuery = `
query PullRequestOpinions($owner:String!,$name:String!,$number:Int!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    headRefOid
    latestOpinionatedReviews(first:100,after:$cursor){nodes{id state bodyText submittedAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
  }}}
}`

const pullRequestReviewPageQuery = `
query PullRequestReviews($owner:String!,$name:String!,$number:Int!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    headRefOid
    reviews(first:100,after:$cursor){nodes{id state bodyText submittedAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
  }}}
}`

const pullRequestCheckPageQuery = `
query PullRequestChecks($owner:String!,$name:String!,$number:Int!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){headRefOid commits(last:1){nodes{commit{statusCheckRollup{
    contexts(first:100,after:$cursor){
      nodes{__typename ... on CheckRun{id name status conclusion detailsUrl} ... on StatusContext{id context state targetUrl}}
      pageInfo{hasNextPage endCursor}
    }
  }}}}}}
}`

var ErrReadinessHeadChanged = errors.New("pull request head changed while reading readiness")

func FetchPullRequestReadiness(ctx context.Context, transport QueryTransport, repo string, number int) (*prreadiness.Observation, error) {
	owner, name, err := splitRepository(repo)
	if err != nil {
		return nil, err
	}
	variables := map[string]any{"owner": owner, "name": name, "number": number}
	pr, err := fetchReadinessPage(ctx, transport, pullRequestReadinessQuery, variables)
	if err != nil {
		return nil, err
	}
	if err := appendReadinessPages(ctx, transport, variables, pr); err != nil {
		return nil, err
	}
	return buildReadinessObservation(pr), nil
}

func (c *Client) FetchPullRequestReadiness(ctx context.Context, repo string, number int) (*prreadiness.Observation, error) {
	return FetchPullRequestReadiness(ctx, c, repo, number)
}

func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any) ([]byte, error) {
	return c.doRequestContext(ctx, "POST", c.graphQLURL(), map[string]any{"query": query, "variables": variables})
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

func splitRepository(repo string) (string, string, error) {
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repository %q must be owner/name", repo)
	}
	return parts[0], parts[1], nil
}

func fetchReadinessPage(ctx context.Context, transport QueryTransport, query string, variables map[string]any) (*readinessPullRequest, error) {
	body, err := transport.GraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data struct {
			Repository struct {
				PullRequest *readinessPullRequest `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse pull request readiness: %w", err)
	}
	if len(payload.Errors) > 0 {
		return nil, errors.New(payload.Errors[0].Message)
	}
	if payload.Data.Repository.PullRequest == nil {
		return nil, errors.New("GitHub returned no pull request")
	}
	return payload.Data.Repository.PullRequest, nil
}

func appendReadinessPages(ctx context.Context, transport QueryTransport, base map[string]any, pr *readinessPullRequest) error {
	for pr.Reactions.PageInfo.HasNextPage {
		page, err := nextReadinessPage(ctx, transport, pullRequestReactionPageQuery, base, pr.HeadRefOID, pr.Reactions.PageInfo)
		if err != nil {
			return fmt.Errorf("fetch pull request reactions: %w", err)
		}
		pr.Reactions.Nodes = append(pr.Reactions.Nodes, page.Reactions.Nodes...)
		pr.Reactions.PageInfo = page.Reactions.PageInfo
	}
	for pr.LatestOpinions.PageInfo.HasNextPage {
		page, err := nextReadinessPage(ctx, transport, pullRequestOpinionPageQuery, base, pr.HeadRefOID, pr.LatestOpinions.PageInfo)
		if err != nil {
			return fmt.Errorf("fetch pull request opinions: %w", err)
		}
		pr.LatestOpinions.Nodes = append(pr.LatestOpinions.Nodes, page.LatestOpinions.Nodes...)
		pr.LatestOpinions.PageInfo = page.LatestOpinions.PageInfo
	}
	for pr.Reviews.PageInfo.HasNextPage {
		page, err := nextReadinessPage(ctx, transport, pullRequestReviewPageQuery, base, pr.HeadRefOID, pr.Reviews.PageInfo)
		if err != nil {
			return fmt.Errorf("fetch pull request review history: %w", err)
		}
		pr.Reviews.Nodes = append(pr.Reviews.Nodes, page.Reviews.Nodes...)
		pr.Reviews.PageInfo = page.Reviews.PageInfo
	}
	contexts := checkContexts(pr)
	for contexts != nil && contexts.PageInfo.HasNextPage {
		page, err := nextReadinessPage(ctx, transport, pullRequestCheckPageQuery, base, pr.HeadRefOID, contexts.PageInfo)
		if err != nil {
			return fmt.Errorf("fetch pull request checks: %w", err)
		}
		pageContexts := checkContexts(page)
		if pageContexts == nil {
			return errors.New("GitHub returned no check page")
		}
		contexts.Nodes = append(contexts.Nodes, pageContexts.Nodes...)
		contexts.PageInfo = pageContexts.PageInfo
	}
	return nil
}

func nextReadinessPage(ctx context.Context, transport QueryTransport, query string, base map[string]any, headSHA string, info readinessPageInfo) (*readinessPullRequest, error) {
	if info.EndCursor == "" {
		return nil, errors.New("GitHub pagination has no end cursor")
	}
	variables := make(map[string]any, len(base)+1)
	for key, value := range base {
		variables[key] = value
	}
	variables["cursor"] = info.EndCursor
	page, err := fetchReadinessPage(ctx, transport, query, variables)
	if err != nil {
		return nil, err
	}
	if page.HeadRefOID != headSHA {
		return nil, ErrReadinessHeadChanged
	}
	return page, nil
}

func checkContexts(pr *readinessPullRequest) *readinessConnection[readinessCheck] {
	if len(pr.Commits.Nodes) == 0 || pr.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return nil
	}
	return &pr.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts
}

func buildReadinessObservation(pr *readinessPullRequest) *prreadiness.Observation {
	observation := &prreadiness.Observation{
		Number: pr.Number, URL: pr.URL, Title: pr.Title,
		State: strings.ToLower(pr.State), Draft: pr.IsDraft, Merged: pr.Merged,
		HeadSHA: pr.HeadRefOID, HeadRef: pr.HeadRefName,
		MergeStateStatus: strings.ToUpper(pr.MergeStateStatus),
		ReviewDecision:   strings.ToUpper(pr.ReviewDecision),
	}
	for _, reaction := range pr.Reactions.Nodes {
		if canonicalActor(reaction.User) != prreadiness.CodexActor {
			continue
		}
		switch strings.ToUpper(reaction.Content) {
		case "THUMBS_UP":
			observation.CodexThumbsUp = true
		case "EYES":
			observation.CodexEyes = true
		}
	}
	actors := make(map[string]string)
	for _, review := range pr.LatestOpinions.Nodes {
		actor := canonicalActor(review.Author)
		if actor != "" {
			actors[strings.ToLower(actor)] = actor
		}
	}
	actorKeys := make([]string, 0, len(actors))
	for key := range actors {
		actorKeys = append(actorKeys, key)
	}
	sort.Strings(actorKeys)
	for _, key := range actorKeys {
		observation.ReviewOpinions = append(observation.ReviewOpinions,
			selectReviewOpinion(pr.LatestOpinions.Nodes, pr.Reviews.Nodes, actors[key]))
	}
	if contexts := checkContexts(pr); contexts != nil {
		for _, check := range contexts.Nodes {
			name, state, target := "status:"+check.Context, statusState(check.State), check.TargetURL
			if check.TypeName == "CheckRun" {
				name, state, target = "check:"+check.Name, checkRunState(check.Status, check.Conclusion), check.DetailsURL
			}
			observation.Checks = append(observation.Checks, prreadiness.Check{ID: check.ID, Name: name, State: state, URL: target})
		}
		sort.Slice(observation.Checks, func(i, j int) bool { return observation.Checks[i].Name < observation.Checks[j].Name })
	}
	return observation
}

func canonicalActor(actor readinessActor) string {
	login := strings.TrimSpace(actor.Login)
	if actor.TypeName == "Bot" && !strings.HasSuffix(strings.ToLower(login), "[bot]") {
		login += "[bot]"
	}
	return login
}

func selectReviewOpinion(current, history []readinessReview, reviewer string) prreadiness.ReviewOpinion {
	var candidate *readinessReview
	for i := range current {
		if strings.EqualFold(canonicalActor(current[i].Author), reviewer) {
			if candidate != nil {
				return prreadiness.ReviewOpinion{Actor: reviewer, State: "UNKNOWN"}
			}
			candidate = &current[i]
		}
	}
	if candidate == nil {
		return prreadiness.ReviewOpinion{Actor: reviewer}
	}
	opinion := prreadiness.ReviewOpinion{Actor: canonicalActor(candidate.Author), State: strings.ToUpper(candidate.State), Body: candidate.BodyText}
	for _, review := range history {
		if review.ID == candidate.ID || !strings.EqualFold(canonicalActor(review.Author), reviewer) {
			continue
		}
		state := strings.ToUpper(review.State)
		if state != "APPROVED" && state != "CHANGES_REQUESTED" && state != "DISMISSED" {
			continue
		}
		if review.SubmittedAt.IsZero() || review.SubmittedAt.Equal(candidate.SubmittedAt) {
			opinion.State = "UNKNOWN"
			return opinion
		}
		if review.SubmittedAt.After(candidate.SubmittedAt) {
			if state == "DISMISSED" {
				opinion.State = "DISMISSED"
			} else {
				opinion.State = "UNKNOWN"
			}
			return opinion
		}
	}
	return opinion
}

func checkRunState(status, conclusion string) string {
	if !strings.EqualFold(status, "COMPLETED") {
		return "pending"
	}
	switch strings.ToUpper(conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return "success"
	case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return "failure"
	default:
		return "pending"
	}
}

func statusState(state string) string {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return "success"
	case "FAILURE", "ERROR":
		return "failure"
	default:
		return "pending"
	}
}

type feedbackComment struct {
	ID           string         `json:"id"`
	BodyText     string         `json:"bodyText"`
	CreatedAt    time.Time      `json:"createdAt"`
	Path         string         `json:"path"`
	Line         *int           `json:"line"`
	OriginalLine *int           `json:"originalLine"`
	Author       readinessActor `json:"author"`
}

func (c feedbackComment) location() string {
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

type feedbackThread struct {
	ID         string                               `json:"id"`
	IsResolved bool                                 `json:"isResolved"`
	Comments   readinessConnection[feedbackComment] `json:"comments"`
}

type feedbackPullRequest struct {
	Comments      readinessConnection[feedbackComment] `json:"comments"`
	Reviews       readinessConnection[readinessReview] `json:"reviews"`
	ReviewThreads readinessConnection[feedbackThread]  `json:"reviewThreads"`
}

const pullRequestFeedbackQuery = `
query PullRequestFeedback($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    comments(first:100){nodes{id bodyText createdAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
    reviews(first:100){nodes{id state bodyText submittedAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
    reviewThreads(first:100){nodes{id isResolved comments(first:100){
      nodes{id bodyText createdAt path line originalLine author{__typename id login}}
      pageInfo{hasNextPage endCursor}
    }} pageInfo{hasNextPage endCursor}}
  }}}
}`

const pullRequestFeedbackReviewPageQuery = `
query PullRequestReviewBodies($owner:String!,$name:String!,$number:Int!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    reviews(first:100,after:$cursor){nodes{id state bodyText submittedAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
  }}
}`

const pullRequestCommentPageQuery = `
query PullRequestComments($owner:String!,$name:String!,$number:Int!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    comments(first:100,after:$cursor){nodes{id bodyText createdAt author{__typename id login}} pageInfo{hasNextPage endCursor}}
  }}}
}`

const pullRequestThreadPageQuery = `
query PullRequestThreads($owner:String!,$name:String!,$number:Int!,$cursor:String!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    reviewThreads(first:100,after:$cursor){nodes{id isResolved comments(first:100){
      nodes{id bodyText createdAt path line originalLine author{__typename id login}}
      pageInfo{hasNextPage endCursor}
    }} pageInfo{hasNextPage endCursor}}
  }}}
}`

const pullRequestThreadCommentsPageQuery = `
query PullRequestThreadComments($id:ID!,$cursor:String!){
  node(id:$id){... on PullRequestReviewThread{comments(first:100,after:$cursor){
    nodes{id bodyText createdAt path line originalLine author{__typename id login}}
    pageInfo{hasNextPage endCursor}
  }}}
}`

func FetchPullRequestFeedback(ctx context.Context, transport QueryTransport, repo string, number int) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error) {
	owner, name, err := splitRepository(repo)
	if err != nil {
		return nil, nil, err
	}
	base := map[string]any{"owner": owner, "name": name, "number": number}
	pr, err := fetchFeedbackPage(ctx, transport, pullRequestFeedbackQuery, base)
	if err != nil {
		return nil, nil, err
	}
	for pr.Comments.PageInfo.HasNextPage {
		page, err := nextFeedbackPage(ctx, transport, pullRequestCommentPageQuery, base, pr.Comments.PageInfo)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch pull request comments: %w", err)
		}
		pr.Comments.Nodes = append(pr.Comments.Nodes, page.Comments.Nodes...)
		pr.Comments.PageInfo = page.Comments.PageInfo
	}
	for pr.Reviews.PageInfo.HasNextPage {
		page, err := nextFeedbackPage(ctx, transport, pullRequestFeedbackReviewPageQuery, base, pr.Reviews.PageInfo)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch pull request review bodies: %w", err)
		}
		pr.Reviews.Nodes = append(pr.Reviews.Nodes, page.Reviews.Nodes...)
		pr.Reviews.PageInfo = page.Reviews.PageInfo
	}
	for pr.ReviewThreads.PageInfo.HasNextPage {
		page, err := nextFeedbackPage(ctx, transport, pullRequestThreadPageQuery, base, pr.ReviewThreads.PageInfo)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch pull request threads: %w", err)
		}
		pr.ReviewThreads.Nodes = append(pr.ReviewThreads.Nodes, page.ReviewThreads.Nodes...)
		pr.ReviewThreads.PageInfo = page.ReviewThreads.PageInfo
	}
	for i := range pr.ReviewThreads.Nodes {
		thread := &pr.ReviewThreads.Nodes[i]
		for thread.Comments.PageInfo.HasNextPage {
			if thread.Comments.PageInfo.EndCursor == "" {
				return nil, nil, errors.New("GitHub thread pagination has no end cursor")
			}
			body, err := transport.GraphQL(ctx, pullRequestThreadCommentsPageQuery, map[string]any{"id": thread.ID, "cursor": thread.Comments.PageInfo.EndCursor})
			if err != nil {
				return nil, nil, fmt.Errorf("fetch pull request thread comments: %w", err)
			}
			var payload struct {
				Data struct {
					Node *struct {
						Comments readinessConnection[feedbackComment] `json:"comments"`
					} `json:"node"`
				} `json:"data"`
				Errors []struct {
					Message string `json:"message"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				return nil, nil, err
			}
			if len(payload.Errors) > 0 {
				return nil, nil, errors.New(payload.Errors[0].Message)
			}
			if payload.Data.Node == nil {
				return nil, nil, errors.New("GitHub returned no review thread")
			}
			thread.Comments.Nodes = append(thread.Comments.Nodes, payload.Data.Node.Comments.Nodes...)
			thread.Comments.PageInfo = payload.Data.Node.Comments.PageInfo
		}
	}
	return buildFeedback(pr), buildThreads(pr), nil
}

func (c *Client) FetchPullRequestFeedback(ctx context.Context, repo string, number int) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error) {
	return FetchPullRequestFeedback(ctx, c, repo, number)
}

func fetchFeedbackPage(ctx context.Context, transport QueryTransport, query string, variables map[string]any) (*feedbackPullRequest, error) {
	body, err := transport.GraphQL(ctx, query, variables)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data struct {
			Repository struct {
				PullRequest *feedbackPullRequest `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Errors) > 0 {
		return nil, errors.New(payload.Errors[0].Message)
	}
	if payload.Data.Repository.PullRequest == nil {
		return nil, errors.New("GitHub returned no pull request feedback")
	}
	return payload.Data.Repository.PullRequest, nil
}

func nextFeedbackPage(ctx context.Context, transport QueryTransport, query string, base map[string]any, info readinessPageInfo) (*feedbackPullRequest, error) {
	if info.EndCursor == "" {
		return nil, errors.New("GitHub pagination has no end cursor")
	}
	variables := make(map[string]any, len(base)+1)
	for key, value := range base {
		variables[key] = value
	}
	variables["cursor"] = info.EndCursor
	return fetchFeedbackPage(ctx, transport, query, variables)
}

func buildFeedback(pr *feedbackPullRequest) []prreadiness.FeedbackItem {
	items := make([]prreadiness.FeedbackItem, 0, len(pr.Comments.Nodes)+len(pr.Reviews.Nodes))
	for _, comment := range pr.Comments.Nodes {
		items = append(items, feedbackItem(comment, "comment"))
	}
	for _, review := range pr.Reviews.Nodes {
		if strings.TrimSpace(review.BodyText) == "" {
			continue
		}
		items = append(items, prreadiness.FeedbackItem{
			ID: review.ID, Kind: "review", Author: canonicalActor(review.Author), Body: review.BodyText, CreatedAt: review.SubmittedAt,
		})
	}
	for _, thread := range pr.ReviewThreads.Nodes {
		for index, comment := range thread.Comments.Nodes {
			kind := "reply"
			if index == 0 {
				kind = "inline_comment"
			}
			items = append(items, feedbackItem(comment, kind))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}

func feedbackItem(comment feedbackComment, kind string) prreadiness.FeedbackItem {
	return prreadiness.FeedbackItem{
		ID: comment.ID, Kind: kind, Author: canonicalActor(comment.Author), Body: comment.BodyText,
		Location: comment.location(), CreatedAt: comment.CreatedAt,
	}
}

func buildThreads(pr *feedbackPullRequest) []prreadiness.ThreadState {
	threads := make([]prreadiness.ThreadState, 0, len(pr.ReviewThreads.Nodes))
	for _, thread := range pr.ReviewThreads.Nodes {
		state := prreadiness.ThreadState{ID: thread.ID, Resolved: thread.IsResolved}
		if len(thread.Comments.Nodes) > 0 {
			first := thread.Comments.Nodes[0]
			state.Author, state.Body, state.Location = canonicalActor(first.Author), first.BodyText, first.location()
		}
		threads = append(threads, state)
	}
	sort.Slice(threads, func(i, j int) bool { return threads[i].ID < threads[j].ID })
	return threads
}
