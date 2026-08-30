package daemon

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
)

const appBundleRoutePrefix = "/apps/bundle/"

// A year in seconds, the conventional ceiling for `immutable` content: the hash
// in the path is a digest of the bytes, so a URL cannot outlive what it names.
const appBundleMaxAge = 31536000

// Checked before the path is touched, so a hash off the wire can never become a
// directory traversal.
var contentHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func AppBundleURLPath(app, contentHash, view string) string {
	return appBundleRoutePrefix + app + "/" + contentHash + "/" + view + ".js"
}

// The artifact path comes from appbuild rather than being joined here: builder
// and handler must agree, or a rollback serves the wrong module.
func (d *Daemon) handleAppBundle(w http.ResponseWriter, r *http.Request) {
	// A module script is fetched in CORS mode from tauri://localhost. `*` is safe:
	// the route serves immutable public artifacts and reads no credentials.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "the app bundle route serves GET and HEAD", http.StatusMethodNotAllowed)
		return
	}

	app, hash, view, err := parseAppBundlePath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	path := appbuild.ViewArtifactPath(d.appsDir, app, hash, view)
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, fmt.Sprintf(
			"no built view %q of app %q at version %s (looked for %s); `attn app status %s` shows the version it serves now",
			view, app, appbuild.ShortHash(hash), path, app), http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, fmt.Sprintf("the built view %q of app %q is not a readable file at %s", view, app, path), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, immutable", appBundleMaxAge))
	http.ServeContent(w, r, view+".js", info.ModTime(), file)
}

func parseAppBundlePath(urlPath string) (app, hash, view string, err error) {
	rest := strings.TrimPrefix(urlPath, appBundleRoutePrefix)
	if rest == urlPath {
		return "", "", "", fmt.Errorf("an app bundle path starts with %s", appBundleRoutePrefix)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf(
			"an app bundle path is %s<app>/<content-hash>/<view>.js; %q has %d segments after the prefix, not 3",
			appBundleRoutePrefix, urlPath, len(parts))
	}
	app, hash, file := parts[0], parts[1], parts[2]
	if err := apps.ValidateName(app); err != nil {
		return "", "", "", err
	}
	if !contentHashRe.MatchString(hash) {
		return "", "", "", fmt.Errorf("%q is not a version content hash (64 hex characters)", hash)
	}
	view, ok := strings.CutSuffix(file, ".js")
	if !ok {
		return "", "", "", fmt.Errorf("a view is served as <view>.js; %q has no .js suffix", file)
	}
	if err := apps.ValidateViewName(view); err != nil {
		return "", "", "", err
	}
	return app, hash, view, nil
}
