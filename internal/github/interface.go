package github

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type GitHubClient interface {
	Host() string
	IsAvailable() bool
	FetchAll() ([]*protocol.PR, error)
	FetchPRDetails(repo string, number int) (*PRDetails, error)
	ApprovePR(repo string, number int) error
	MergePR(repo string, number int, method string) error
	IsRateLimited(resource string) (bool, time.Time)
	GetRateLimit(resource string) *RateLimitInfo
}

var _ GitHubClient = (*Client)(nil)
