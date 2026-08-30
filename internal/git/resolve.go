package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolveRepoDir(repoDir string) (string, error) {
	expanded := ExpandPath(repoDir)
	if isGitRepo(expanded) {
		return expanded, nil
	}

	parent := filepath.Dir(expanded)
	base := filepath.Base(expanded)
	if parent == expanded || base == "" {
		return "", fmt.Errorf("repo path not found: %s", expanded)
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", fmt.Errorf("repo path not found: %s: %w", expanded, err)
	}

	var originMatches []string
	var noOriginMatches []string
	var originMismatches []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(parent, entry.Name(), base)
		if !isGitRepo(candidate) {
			continue
		}
		originName := originRepoName(candidate)
		switch {
		case originName == base:
			originMatches = append(originMatches, candidate)
		case originName == "":
			noOriginMatches = append(noOriginMatches, candidate)
		default:
			originMismatches = append(originMismatches, candidate)
		}
	}

	switch len(originMatches) {
	case 1:
		return originMatches[0], nil
	case 0:
		switch len(noOriginMatches) {
		case 1:
			return noOriginMatches[0], nil
		case 0:
			if len(originMismatches) > 0 {
				return "", fmt.Errorf("repo path not found: %s (origin mismatch: %s)", expanded, strings.Join(originMismatches, ", "))
			}
			return "", fmt.Errorf("repo path not found: %s", expanded)
		default:
			return "", fmt.Errorf("repo path not found: %s (multiple matches without origin: %s)", expanded, strings.Join(noOriginMatches, ", "))
		}
	default:
		return "", fmt.Errorf("repo path not found: %s (multiple matches: %s)", expanded, strings.Join(originMatches, ", "))
	}
}

func originRepoName(path string) string {
	out, err := runGitOutput(OpMetadata, path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return repoNameFromRemote(strings.TrimSpace(string(out)))
}

func OriginOwnerRepo(path string) string {
	_, slug := OriginHostOwnerRepo(path)
	return slug
}

func OriginHostOwnerRepo(path string) (host, ownerRepo string) {
	out, err := runGitOutput(OpMetadata, path, "remote", "get-url", "origin")
	if err != nil {
		return "", ""
	}
	return hostOwnerRepoFromRemote(strings.TrimSpace(string(out)))
}

func hostOwnerRepoFromRemote(remote string) (host, ownerRepo string) {
	if remote == "" {
		return "", ""
	}
	remote = strings.TrimSuffix(remote, ".git")

	if idx := strings.Index(remote, "://"); idx >= 0 {
		rest := remote[idx+3:]
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			return "", ""
		}
		host = rest[:slashIdx]
		remote = rest[slashIdx+1:]
	} else if colonIdx := strings.Index(remote, ":"); colonIdx > 0 {
		// scp-like form: [user@]host:owner/name
		host = remote[:colonIdx]
		remote = remote[colonIdx+1:]
	}

	if atIdx := strings.LastIndex(host, "@"); atIdx >= 0 {
		host = host[atIdx+1:]
	}
	if portIdx := strings.Index(host, ":"); portIdx >= 0 {
		host = host[:portIdx]
	}

	remote = strings.Trim(remote, "/")
	parts := strings.Split(remote, "/")
	if len(parts) < 2 {
		return "", ""
	}
	owner := parts[len(parts)-2]
	name := parts[len(parts)-1]
	if owner == "" || name == "" || host == "" {
		return "", ""
	}
	return host, owner + "/" + name
}

func repoNameFromRemote(remote string) string {
	if remote == "" {
		return ""
	}
	remote = strings.TrimSuffix(remote, ".git")
	remote = strings.ReplaceAll(remote, ":", "/")
	parts := strings.Split(remote, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// RemoteHostOwnerRepos returns "host/owner/name" for every remote in dir, origin
func RemoteHostOwnerRepos(dir string) []string {
	out, err := runGitOutput(OpMetadata, dir, "remote", "-v")
	if err != nil {
		return nil
	}
	identities := []string{}
	seen := map[string]bool{}
	origin := ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		host, ownerRepo := hostOwnerRepoFromRemote(fields[1])
		if host == "" || ownerRepo == "" {
			continue
		}
		identity := host + "/" + ownerRepo
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if fields[0] == "origin" && origin == "" {
			origin = identity
			continue
		}
		identities = append(identities, identity)
	}
	if origin != "" {
		identities = append([]string{origin}, identities...)
	}
	return identities
}
