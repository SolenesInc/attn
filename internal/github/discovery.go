package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type HostInfo struct {
	Host   string
	APIURL string
	Login  string
	Active bool
	Usable bool
}

func DiscoverHosts(ctx context.Context) ([]HostInfo, error) {
	output, err := ghAuthStatusHosts(ctx)
	if err != nil {
		return nil, err
	}
	hosts, err := parseAuthStatusHosts(output)
	if err != nil {
		return nil, err
	}
	for i := range hosts {
		if !hosts[i].Usable || hosts[i].Host == "" {
			continue
		}
		apiHost, err := getAPIHostForHost(ctx, hosts[i].Host)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if apiHost != "" {
			hosts[i].APIURL = mapHostToAPIURL(hosts[i].Host, apiHost)
		}
	}
	return hosts, nil
}

func getAPIHostForHost(ctx context.Context, host string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "config", "get", "api_host", "-h", host)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func GetTokenForHost(ctx context.Context, host string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "-h", host)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token -h %s failed: %w", host, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func ghAuthStatusHosts(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status", "--json", "hosts")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh auth status --json hosts failed: %w", err)
	}
	return output, nil
}

func parseAuthStatusHosts(output []byte) ([]HostInfo, error) {
	var payload struct {
		Hosts map[string][]map[string]interface{} `json:"hosts"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("parse gh auth status output: %w", err)
	}

	if len(payload.Hosts) == 0 {
		return nil, nil
	}

	var results []HostInfo
	for host, entries := range payload.Hosts {
		selected := &HostInfo{Host: host, APIURL: mapHostToAPIURL(host, "")}
		for _, entry := range entries {
			state, _ := entry["state"].(string)
			if state != "success" {
				continue
			}
			active := false
			if rawActive, ok := entry["active"].(bool); ok {
				active = rawActive
			}

			login := ""
			if user, ok := entry["user"].(string); ok {
				login = user
			} else if loginVal, ok := entry["login"].(string); ok {
				login = loginVal
			} else if userObj, ok := entry["user"].(map[string]interface{}); ok {
				if loginVal, ok := userObj["login"].(string); ok {
					login = loginVal
				}
			}

			info := &HostInfo{
				Host:   host,
				APIURL: mapHostToAPIURL(host, ""),
				Login:  login,
				Active: active,
				Usable: true,
			}

			if !selected.Usable || (active && !selected.Active) {
				selected = info
			}
		}
		results = append(results, *selected)
	}

	return results, nil
}

func mapHostToAPIURL(host, apiHost string) string {
	host = strings.TrimSpace(host)
	apiHost = strings.TrimRight(strings.TrimSpace(apiHost), "/")
	if isSubdomainAPIHost(host) {
		if apiHost != "" {
			return "https://" + apiHost
		}
		return "https://api." + strings.ToLower(host)
	}
	if apiHost != "" {
		return "https://" + apiHost + "/api/v3"
	}
	return "https://" + host + "/api/v3"
}

func isSubdomainAPIHost(host string) bool {
	lower := strings.ToLower(host)
	return lower == "github.com" || (strings.HasSuffix(lower, ".ghe.com") && len(lower) > len(".ghe.com"))
}
