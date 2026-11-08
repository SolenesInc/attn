package git

import (
	"context"
	"strings"
)

func (c *Client) RepositoryOrigin(ctx context.Context, repository string) string {
	remote, err := c.Output(ctx, OpMetadata, repository, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return NormalizedOrigin(string(remote))
}

func NormalizedOrigin(remote string) string {
	remote = strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(remote), "/"), ".git")
	if scheme, rest, ok := strings.Cut(remote, "://"); ok {
		if strings.EqualFold(scheme, "file") {
			return "file://" + rest
		}
		authority, path, _ := strings.Cut(rest, "/")
		return hostedOrigin(authority, path, strings.HasPrefix(path, "~"))
	}
	if authority, path, ok := strings.Cut(remote, ":"); ok && !strings.Contains(authority, "/") {
		return hostedOrigin(authority, path, !strings.HasPrefix(path, "/"))
	}
	return "file://" + remote
}

func hostedOrigin(authority, path string, relativeToUserHome bool) string {
	user, host := "", authority
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		user, host = authority[:at], authority[at+1:]
	}
	path = strings.Trim(path, "/")
	if host == "" || path == "" {
		return ""
	}
	origin := strings.ToLower(host + "/" + path)
	if relativeToUserHome && user != "" && user != "git" {
		return user + "@" + origin
	}
	return origin
}
