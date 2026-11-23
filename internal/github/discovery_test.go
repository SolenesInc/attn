package github

import "testing"

func TestParseAuthStatusHosts(t *testing.T) {
	jsonInput := []byte(`{
  "hosts": {
    "github.com": [
      {"state": "success", "active": true, "user": "octocat"}
    ],
    "ghe.corp.com": [
      {"state": "failed", "active": false, "user": "bad"},
      {"state": "success", "active": false, "user": {"login": "enterprise"}}
    ]
  }
}`)

	hosts, err := parseAuthStatusHosts(jsonInput)
	if err != nil {
		t.Fatalf("parseAuthStatusHosts error: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}

	found := make(map[string]HostInfo)
	for _, host := range hosts {
		found[host.Host] = host
	}

	gh, ok := found["github.com"]
	if !ok {
		t.Fatalf("missing github.com host")
	}
	if gh.APIURL != "https://api.github.com" {
		t.Fatalf("github.com api url = %s", gh.APIURL)
	}
	if gh.Login != "octocat" {
		t.Fatalf("github.com login = %s", gh.Login)
	}
	if !gh.Active {
		t.Fatalf("github.com active should be true")
	}

	ghe, ok := found["ghe.corp.com"]
	if !ok {
		t.Fatalf("missing ghe.corp.com host")
	}
	if ghe.APIURL != "https://ghe.corp.com/api/v3" {
		t.Fatalf("ghe api url = %s", ghe.APIURL)
	}
	if ghe.Login != "enterprise" {
		t.Fatalf("ghe login = %s", ghe.Login)
	}
}

func TestParseAuthStatusHostsEmpty(t *testing.T) {
	hosts, err := parseAuthStatusHosts([]byte(`{"hosts": {}}`))
	if err != nil {
		t.Fatalf("parseAuthStatusHosts error: %v", err)
	}
	if len(hosts) != 0 {
		t.Fatalf("expected no hosts, got %d", len(hosts))
	}
}

func TestMapHostToAPIURL(t *testing.T) {
	for _, tc := range []struct {
		host    string
		apiHost string
		want    string
	}{
		{"github.com", "", "https://api.github.com"},
		{"GITHUB.COM", "", "https://api.github.com"},
		{"github.com", "gateway.example.net", "https://gateway.example.net"},
		{"spotify.ghe.com", "", "https://api.spotify.ghe.com"},
		{"spotify.ghe.com", "ghec-dr-api.spotify.net", "https://ghec-dr-api.spotify.net"},
		{"spotify.ghe.com", "ghec-dr-api.spotify.net/", "https://ghec-dr-api.spotify.net"},
		{"ghe.corp.com", "", "https://ghe.corp.com/api/v3"},
		{"ghe.corp.com", "api-gw.corp.com", "https://api-gw.corp.com/api/v3"},
	} {
		if got := mapHostToAPIURL(tc.host, tc.apiHost); got != tc.want {
			t.Errorf("mapHostToAPIURL(%q, %q) = %q, want %q", tc.host, tc.apiHost, got, tc.want)
		}
	}
}
