package selfupdate

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type Kind string

const (
	ReleaseApp    Kind = "release_app"
	ReleaseBinary Kind = "release_binary"
	Source        Kind = "source"
	NamedInstance Kind = "named_instance"
	Outpost       Kind = "outpost"
	Unknown       Kind = "unknown"
)

func (k Kind) Label() string { return strings.ReplaceAll(string(k), "_", " ") }

type Facts struct {
	Channel     string
	Fingerprint string
	Repository  string
	Instance    string
	Enrolled    bool
	Executable  string
}

type Install struct {
	Kind  Kind
	Facts Facts
}

func Detect(facts Facts) Install {
	return Install{Kind: kindOf(facts), Facts: facts}
}

func kindOf(facts Facts) Kind {
	switch {
	case facts.Enrolled:
		return Outpost
	case facts.Instance != "":
		return NamedInstance
	case facts.Channel == "source":
		return Source
	case facts.Channel == "release" && strings.Contains(facts.Executable, ".app/Contents/MacOS/"):
		return ReleaseApp
	case facts.Channel == "release":
		return ReleaseBinary
	default:
		return Unknown
	}
}

func (i Install) GetsReleases() bool {
	return i.Kind == ReleaseApp || i.Kind == ReleaseBinary
}

func (i Install) RefusalNamingChannel() error {
	return fmt.Errorf("attn cannot update this install: its build channel is %q. Install a release, or build from source with make install", i.Facts.Channel)
}

type Release struct {
	Version string
	URL     string
}

var releaseTag = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

// FindLatest follows the /releases/latest redirect that sits beside the release
// source's /download path. Drafts and prereleases never get that redirect.
func FindLatest(ctx context.Context, releaseSource string) (Release, error) {
	latest := strings.TrimSuffix(strings.TrimRight(releaseSource, "/"), "/download") + "/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, latest, nil)
	if err != nil {
		return Release{}, fmt.Errorf("release source %q: %w", releaseSource, err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return Release{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("%s answered %s", latest, response.Status)
	}
	tag := path.Base(response.Request.URL.Path)
	if !releaseTag.MatchString(tag) {
		return Release{}, fmt.Errorf("%s led to %s, which names no release version", latest, response.Request.URL)
	}
	return Release{Version: strings.TrimPrefix(tag, "v"), URL: response.Request.URL.String()}, nil
}

func IsNewer(current, candidate string) bool {
	have, ok := parseVersion(current)
	if !ok {
		return false
	}
	want, ok := parseVersion(candidate)
	if !ok {
		return false
	}
	for part := range have {
		if want[part] != have[part] {
			return want[part] > have[part]
		}
	}
	return false
}

func parseVersion(version string) ([3]int, bool) {
	match := releaseTag.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return [3]int{}, false
	}
	var parts [3]int
	for i := range parts {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return [3]int{}, false
		}
		parts[i] = value
	}
	return parts, true
}
