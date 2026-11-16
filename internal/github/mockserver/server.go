package mockserver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RequestLog struct {
	Method string
	Path   string
	Body   map[string]interface{}
	Status int
}

type Server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*RequestLog
	PRs      []MockPR
	holds    map[string]*reviewsHold

	rateLimitedUntil time.Time
	windowResetAt    time.Time
	windowUsed       map[string]int
}

const rateLimitPerWindow = 5000

type reviewsHold struct {
	held    chan struct{}
	release chan struct{}
}

type MockPR struct {
	Repo   string
	Number int
	Title  string
	Draft  bool
	Role   string

	MergeableState    string
	HeadSHA           string
	DetailsHTTPStatus int
	ReviewState       string
	State             string
	Merged            bool
}

func New() *Server {
	s := &Server{
		requests: []*RequestLog{},
		PRs:      []MockPR{},
		holds:    map[string]*reviewsHold{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)
	s.Server = httptest.NewServer(mux)
	return s
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	recorder := &statusRecorder{ResponseWriter: w}
	s.mu.Lock()
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	logged := &RequestLog{Method: r.Method, Path: r.URL.Path, Body: body}
	s.requests = append(s.requests, logged)
	s.mu.Unlock()

	s.respond(recorder, r)

	s.mu.Lock()
	logged.Status = recorder.status
	s.mu.Unlock()
}

func (s *Server) respond(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	rateLimitedUntil := s.rateLimitedUntil
	if !s.windowResetAt.IsZero() {
		resource := "core"
		if strings.HasSuffix(r.URL.Path, "/graphql") {
			resource = "graphql"
		}
		s.windowUsed[resource]++
		used := s.windowUsed[resource]
		w.Header().Set("X-RateLimit-Resource", resource)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rateLimitPerWindow))
		w.Header().Set("X-RateLimit-Used", strconv.Itoa(used))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(rateLimitPerWindow-used))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(s.windowResetAt.Unix(), 10))
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")

	if !rateLimitedUntil.IsZero() {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(rateLimitedUntil.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"message": "API rate limit exceeded"})
		return
	}

	if r.URL.Path == "/search/issues" {
		s.handleSearch(w, r)
		return
	}

	reviewPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)/reviews$`)
	if reviewPattern.MatchString(r.URL.Path) && r.Method == "POST" {
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1, "state": "APPROVED"})
		return
	}

	mergePattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)/merge$`)
	if mergePattern.MatchString(r.URL.Path) && r.Method == "PUT" {
		json.NewEncoder(w).Encode(map[string]interface{}{"merged": true, "sha": "abc123"})
		return
	}

	prPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)$`)
	if m := prPattern.FindStringSubmatch(r.URL.Path); m != nil && r.Method == "GET" {
		pr := s.detailsOf(m[1], m[2])
		if pr.DetailsHTTPStatus != 0 {
			w.WriteHeader(pr.DetailsHTTPStatus)
			json.NewEncoder(w).Encode(map[string]string{"message": http.StatusText(pr.DetailsHTTPStatus)})
			return
		}
		repo := map[string]string{"full_name": m[1]}
		writeConditionally(w, r, map[string]interface{}{
			"number":          pr.Number,
			"html_url":        fmt.Sprintf("https://github.com/%s/pull/%d", m[1], pr.Number),
			"title":           pr.Title,
			"state":           pr.State,
			"merged":          pr.Merged,
			"mergeable":       true,
			"mergeable_state": pr.MergeableState,
			"user":            map[string]string{"login": "octocat"},
			"head":            map[string]interface{}{"sha": pr.HeadSHA, "ref": "feature", "repo": repo},
			"base":            map[string]interface{}{"sha": "base123", "ref": "main", "repo": repo},
		})
		return
	}

	checkPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/commits/([^/]+)/check-runs$`)
	if checkPattern.MatchString(r.URL.Path) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"check_runs": []map[string]interface{}{{"conclusion": "success"}},
		})
		return
	}

	reviewsPattern := regexp.MustCompile(`^/repos/([^/]+/[^/]+)/pulls/(\d+)/reviews$`)
	if m := reviewsPattern.FindStringSubmatch(r.URL.Path); m != nil && r.Method == "GET" {
		s.awaitReleaseOfHeld(r.URL.Path)
		reviews := []map[string]interface{}{}
		if state := s.detailsOf(m[1], m[2]).ReviewState; state != "" {
			reviews = append(reviews, map[string]interface{}{"state": state})
		}
		writeConditionally(w, r, reviews)
		return
	}

	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
}

func writeConditionally(w http.ResponseWriter, r *http.Request, value interface{}) {
	body, err := json.Marshal(value)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(body))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(body)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")

	var items []map[string]interface{}
	s.mu.Lock()
	for _, pr := range s.PRs {
		isAuthorSearch := regexp.MustCompile(`author:@me`).MatchString(q)
		isReviewSearch := regexp.MustCompile(`review-requested:@me`).MatchString(q)

		if (isAuthorSearch && pr.Role == "author") || (isReviewSearch && pr.Role == "reviewer") {
			items = append(items, map[string]interface{}{
				"number":         pr.Number,
				"title":          pr.Title,
				"html_url":       fmt.Sprintf("https://github.com/%s/pull/%d", pr.Repo, pr.Number),
				"draft":          pr.Draft,
				"repository_url": fmt.Sprintf("https://api.github.com/repos/%s", pr.Repo),
			})
		}
	}
	s.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_count": len(items),
		"items":       items,
	})
}

func (s *Server) detailsOf(repo, number string) MockPR {
	s.mu.Lock()
	defer s.mu.Unlock()
	details := MockPR{MergeableState: "clean", HeadSHA: "abc123", State: "open"}
	details.Number, _ = strconv.Atoi(number)
	for _, pr := range s.PRs {
		if pr.Repo != repo || strconv.Itoa(pr.Number) != number {
			continue
		}
		details.Title = pr.Title
		if pr.State != "" {
			details.State = pr.State
		}
		details.Merged = pr.Merged
		if pr.MergeableState != "" {
			details.MergeableState = pr.MergeableState
		}
		if pr.HeadSHA != "" {
			details.HeadSHA = pr.HeadSHA
		}
		details.DetailsHTTPStatus = pr.DetailsHTTPStatus
		details.ReviewState = pr.ReviewState
		break
	}
	return details
}

func (s *Server) HoldReviews(repo string, number int) (held <-chan struct{}, release func()) {
	hold := &reviewsHold{held: make(chan struct{}), release: make(chan struct{})}
	s.mu.Lock()
	s.holds[fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)] = hold
	s.mu.Unlock()
	var once sync.Once
	return hold.held, func() { once.Do(func() { close(hold.release) }) }
}

func (s *Server) awaitReleaseOfHeld(path string) {
	s.mu.Lock()
	hold := s.holds[path]
	delete(s.holds, path)
	s.mu.Unlock()
	if hold == nil {
		return
	}
	close(hold.held)
	<-hold.release
}

func (s *Server) RateLimitUntil(resetAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateLimitedUntil = resetAt
}

func (s *Server) StartRateLimitWindow(resetAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowResetAt = resetAt
	s.windowUsed = map[string]int{}
}

func (s *Server) SpendElsewhere(resource string, requests int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.windowUsed[resource] += requests
}

func (s *Server) AddPR(pr MockPR) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PRs = append(s.PRs, pr)
}

func (s *Server) Merge(repo string, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.PRs {
		if s.PRs[i].Repo == repo && s.PRs[i].Number == number {
			s.PRs[i].State, s.PRs[i].Merged = "closed", true
		}
	}
}

func (s *Server) SetMergeableState(repo string, number int, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.PRs {
		if s.PRs[i].Repo == repo && s.PRs[i].Number == number {
			s.PRs[i].MergeableState = state
		}
	}
}

func (s *Server) Requests() []RequestLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]RequestLog, len(s.requests))
	for i, request := range s.requests {
		requests[i] = *request
	}
	return requests
}

func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = []*RequestLog{}
	s.PRs = []MockPR{}
}

func (s *Server) HasApproveRequest(repo string, number int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	for _, req := range s.requests {
		if req.Method == "POST" && req.Path == path {
			if event, ok := req.Body["event"].(string); ok && event == "APPROVE" {
				return true
			}
		}
	}
	return false
}

func (s *Server) HasMergeRequest(repo string, number int, method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number)
	for _, req := range s.requests {
		if req.Method == "PUT" && req.Path == path {
			if m, ok := req.Body["merge_method"].(string); ok && m == method {
				return true
			}
		}
	}
	return false
}
