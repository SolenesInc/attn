package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"testing/fstest"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/prompts"
)

type editor struct {
	root        *os.Root
	assets      http.Handler
	host        string
	mu          sync.Mutex
	repo        string
	defaultBase string
	cachedBase  *baseRevision
}

func newEditor(root *os.Root, assets fs.FS, host string) *editor {
	return &editor{root: root, assets: http.FileServer(http.FS(assets)), host: host}
}

type source struct {
	Text     string `json:"text"`
	Revision string `json:"revision"`
}

type editRequest struct {
	Recipient  string            `json:"recipient"`
	Event      string            `json:"event"`
	Values     prompts.Values    `json:"values"`
	Drafts     map[string]string `json:"drafts"`
	Path       string            `json:"path"`
	Text       string            `json:"text"`
	Revision   string            `json:"revision"`
	Ref        string            `json:"ref"`
	Mode       string            `json:"mode"`
	BaseCommit string            `json:"base_commit"`
	DraftID    string            `json:"draft,omitempty"`
	ReviewID   string            `json:"review,omitempty"`
	Scenario   string            `json:"scenario,omitempty"`
}

func revision(text []byte) string { return fmt.Sprintf("%x", sha256.Sum256(text)) }

func (e *editor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if r.Host != e.host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+e.host) {
		problem(w, http.StatusForbidden, fmt.Errorf("open the editor at http://%s", e.host))
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		e.assets.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/api/identity" && r.Method == "GET" {
		respond(w, map[string]string{"repo": e.repo})
		return
	}
	if r.URL.Path == "/api/events" && r.Method == "GET" {
		e.events(w, r)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.URL.Path == "/api/catalog" && r.Method == "GET" {
		_, err := e.withState(func(*os.Root) (any, error) { e.catalog(w); return nil, nil })
		if err != nil {
			problem(w, 422, err)
		}
		return
	}
	if r.URL.Path == "/api/refs" && r.Method == "GET" {
		refs, err := e.refs(r.Context())
		if err != nil {
			problem(w, 422, err)
		} else {
			respond(w, refs)
		}
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("X-Prompt-Editor") != "1" {
		problem(w, http.StatusForbidden, fmt.Errorf("missing editor request header"))
		return
	}
	if r.URL.Path == "/api/operation" {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil {
			problem(w, 400, err)
			return
		}
		var q operationRequest
		if err := decodeJSON(data, &q); err != nil {
			problem(w, 400, err)
			return
		}
		result, err := e.operation(r.Context(), q)
		if err != nil {
			problem(w, 422, err)
			return
		}
		respond(w, result)
		return
	}
	var request editRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		problem(w, 400, err)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		problem(w, 400, fmt.Errorf("expected one JSON request"))
		return
	}
	switch r.URL.Path {
	case "/api/base":
		base, err := e.selectBase(r.Context(), request.Ref, request.Mode)
		if err != nil {
			problem(w, 422, err)
		} else {
			respond(w, base)
		}
	case "/api/compare":
		result, err := e.compare(r.Context(), request)
		if err != nil {
			problem(w, 422, err)
		} else {
			respond(w, result)
		}
	case "/api/preview":
		snapshot, _, err := e.dataset(request.DraftID, request.ReviewID)
		if err != nil {
			problem(w, 422, err)
			return
		}
		catalog, err := snapshot.load(request.Drafts)
		if err != nil {
			problem(w, 422, err)
			return
		}
		scenarios, err := e.selectedScenarios(snapshot, request.ReviewID)
		if err != nil {
			problem(w, 422, err)
			return
		}
		result, values := renderScenario(catalog, request, scenarios)
		if result.Error != "" || result.Result == nil {
			problem(w, 422, fmt.Errorf("preview: %s", result.Error))
			return
		}
		respond(w, struct {
			prompts.Result
			Values prompts.Values `json:"values"`
		}{*result.Result, values})
	case "/api/save":
		_, err := e.withState(func(*os.Root) (any, error) { e.save(w, request); return nil, nil })
		if err != nil {
			problem(w, 422, err)
		}
	default:
		http.NotFound(w, r)
	}
}

func (e *editor) catalog(w http.ResponseWriter) {
	snapshot, err := e.snapshot()
	if err != nil {
		problem(w, 422, err)
		return
	}
	catalog, err := snapshotView(snapshot)
	if err != nil {
		problem(w, 422, err)
		return
	}
	if err := e.freshness(snapshot); err != nil {
		catalog.Validation = err.Error()
	}
	respond(w, catalog)
}

type overlayFS struct {
	fs.FS
	drafts map[string]string
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if text, ok := o.drafts[name]; ok {
		return fstest.MapFS{name: &fstest.MapFile{Data: []byte(text), Mode: 0644}}.Open(name)
	}
	return o.FS.Open(name)
}

func (e *editor) load(drafts map[string]string) (*prompts.Catalog, error) {
	snapshot, err := e.snapshot()
	if err != nil {
		return nil, err
	}
	for name, text := range drafts {
		if _, ok := snapshot.Sources[name]; !ok {
			return nil, fmt.Errorf("unregistered source: %s", name)
		}
		if !utf8.ValidString(text) {
			return nil, fmt.Errorf("%s must be UTF-8", name)
		}
	}
	if err := e.freshness(snapshot); err != nil {
		return nil, err
	}
	return snapshot.load(drafts)
}

func (e *editor) save(w http.ResponseWriter, request editRequest) {
	snapshot, err := e.snapshot()
	if err != nil {
		problem(w, 422, err)
		return
	}
	if _, ok := snapshot.Sources[request.Path]; !ok {
		problem(w, 400, fmt.Errorf("unregistered source: %s", request.Path))
		return
	}
	if err := e.regularSource(request.Path); err != nil {
		problem(w, 400, err)
		return
	}
	e.saveSource(w, request)
}

func (e *editor) regularSource(sourcePath string) error {
	for name := sourcePath; name != "."; name = path.Dir(name) {
		info, err := e.root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("saving through symlinks is unsupported: %s", name)
		}
	}
	return nil
}

func (e *editor) saveSource(w http.ResponseWriter, request editRequest) {
	current, err := e.root.ReadFile(request.Path)
	if err != nil {
		problem(w, 500, err)
		return
	}
	if revision(current) != request.Revision {
		problem(w, 409, fmt.Errorf("file changed on disk; reload it before saving"))
		return
	}
	if _, err := e.load(map[string]string{request.Path: request.Text}); err != nil {
		problem(w, 422, err)
		return
	}
	if bytes.Equal(current, []byte(request.Text)) {
		respond(w, source{request.Text, revision(current)})
		return
	}
	info, err := e.root.Stat(request.Path)
	if err != nil {
		problem(w, 500, err)
		return
	}
	if err := atomicWrite(e.root, request.Path, []byte(request.Text), info.Mode().Perm()); err != nil {
		problem(w, 500, err)
		return
	}
	respond(w, source{request.Text, revision([]byte(request.Text))})
}

func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	var detail *operationError
	if errors.As(err, &detail) {
		if detail.Code == "revision_conflict" {
			status = 409
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(detail)
		return
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "code": "validation_failed"})
}
