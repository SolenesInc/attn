package daemon_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
)

func TestAnAppliedViewIsServedOverHTTP(t *testing.T) {
	w := newWorld(t)
	declaration := appManifestDeclaration(t, appbuild.Manifest{Name: "reviewer", Views: []appbuild.View{appTileView("approvals", "Pending")}})
	applied := applyAppVersion(t, w.Client(), "reviewer", declaration, "export default {}\n",
		appbuild.ViewArtifact{Name: "approvals", Content: []byte("export default function View() {}\n")})
	hash := applied.ContentHash
	served := "/apps/bundle/reviewer/" + hash + "/approvals.js"

	status, header, body := appBundleRequest(t, w, http.MethodGet, served)
	if status != http.StatusOK || body != "export default function View() {}\n" {
		t.Fatalf("GET the applied view = %d %q, want the built module", status, body)
	}
	if got := header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("Content-Type is %q; a module script has to be served as JavaScript", got)
	}
	if got := header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin is %q, so a tauri://localhost import cannot read it", got)
	}
	if cache := header.Get("Cache-Control"); !strings.Contains(cache, "immutable") || !strings.Contains(cache, "max-age=31536000") {
		t.Errorf("Cache-Control is %q; the path is content-addressed and must be cacheable forever", cache)
	}

	if status, header, _ := appBundleRequest(t, w, http.MethodOptions, served); status != http.StatusNoContent || header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("preflight = %d with origin %q, want 204 allowing any origin", status, header.Get("Access-Control-Allow-Origin"))
	}
	if status, _, _ := appBundleRequest(t, w, http.MethodPost, served); status != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", status)
	}

	for _, tc := range []struct{ name, path string }{
		{"a hash that is not a digest", "/apps/bundle/reviewer/latest/approvals.js"},
		{"an app name the rule refuses", "/apps/bundle/Reviewer/" + hash + "/approvals.js"},
		{"a view name the rule refuses", "/apps/bundle/reviewer/" + hash + "/Approvals.js"},
		{"no .js suffix", "/apps/bundle/reviewer/" + hash + "/approvals"},
		{"a fourth segment", "/apps/bundle/reviewer/" + hash + "/views/approvals.js"},
		{"an escaped traversal", "/apps/bundle/reviewer/%2e%2e%2f%2e%2e%2fetc/approvals.js"},
	} {
		if status, _, body := appBundleRequest(t, w, http.MethodGet, tc.path); status != http.StatusBadRequest {
			t.Errorf("%s (%s) = %d %q, want 400", tc.name, tc.path, status, body)
		}
	}

	unapplied := strings.Repeat("0123456789abcdef", 4)
	status, _, body = appBundleRequest(t, w, http.MethodGet, "/apps/bundle/neverapplied/"+unapplied+"/approvals.js")
	if status != http.StatusNotFound {
		t.Fatalf("a view of a version never applied = %d, want 404", status)
	}
	for _, want := range []string{"approvals", "neverapplied", appbuild.ShortHash(unapplied), "attn app status neverapplied"} {
		if !strings.Contains(body, want) {
			t.Errorf("the 404 does not name %q: %s", want, body)
		}
	}
}

func appBundleRequest(t *testing.T, w *world, method, path string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+w.WSAddr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the answer to %s %s: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, string(body)
}
