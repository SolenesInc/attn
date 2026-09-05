package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing/fstest"
	"time"

	"github.com/victorarias/attn/internal/prompts"
)

type catalogView struct {
	Recipients []prompts.Recipient        `json:"recipients"`
	Sources    map[string]source          `json:"sources"`
	Fields     map[string][]prompts.Field `json:"fields"`
	Validation string                     `json:"validation,omitempty"`
	Revision   string                     `json:"revision,omitempty"`
}

func view(c *prompts.Catalog) catalogView {
	v := catalogView{Recipients: c.Recipients(), Sources: map[string]source{}, Fields: map[string][]prompts.Field{}}
	for name, text := range c.Sources() {
		v.Sources[name] = source{text, revision([]byte(text))}
	}
	for _, r := range v.Recipients {
		for _, event := range r.Events {
			v.Fields[r.ID+"/"+event.ID], _ = c.Fields(r.ID, event.ID)
		}
	}
	return v
}

type baseRevision struct {
	catalogView
	Commit      string `json:"commit"`
	Unavailable string `json:"unavailable,omitempty"`
	catalog     *prompts.Catalog
}

type baseSelection struct {
	*baseRevision
	Ref  string `json:"ref"`
	Mode string `json:"mode"`
	Head string `json:"head"`
	Tip  string `json:"tip"`
}

func gitCommand(ctx context.Context, repo string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	return cmd
}

func gitRead(ctx context.Context, repo string, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := gitCommand(ctx, repo, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s (%w)", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return data, nil
}

func (e *editor) refs(ctx context.Context) (any, error) {
	data, err := gitRead(ctx, e.repo, nil, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, err
	}
	refs := strings.Fields(string(data))
	selected := e.defaultBase
	if selected == "" {
		for _, candidate := range []string{"refs/heads/next", "refs/remotes/origin/next", "refs/heads/main", "refs/remotes/origin/main", "refs/heads/master"} {
			for _, ref := range refs {
				if ref == candidate {
					selected = ref
					break
				}
			}
			if selected != "" {
				break
			}
		}
	}
	return struct {
		Refs    []string `json:"refs"`
		Default string   `json:"default"`
	}{refs, selected}, nil
}

func (e *editor) selectBase(ctx context.Context, ref, mode string) (*baseSelection, error) {
	if strings.TrimSpace(ref) == "" || strings.HasPrefix(ref, "-") {
		return nil, fmt.Errorf("choose a branch, tag, or commit")
	}
	if mode != "merge-base" && mode != "tip" {
		return nil, fmt.Errorf("comparison mode must be merge-base or tip")
	}
	tip, err := gitRead(ctx, e.repo, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return nil, err
	}
	head, err := gitRead(ctx, e.repo, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	selection := &baseSelection{Ref: ref, Mode: mode, Head: strings.TrimSpace(string(head)), Tip: strings.TrimSpace(string(tip))}
	commit := selection.Tip
	if mode == "merge-base" {
		base, err := gitRead(ctx, e.repo, nil, "merge-base", "--all", selection.Head, selection.Tip)
		if err != nil {
			return nil, fmt.Errorf("cannot find a merge base; try Branch tip: %w", err)
		}
		commits := strings.Fields(string(base))
		if len(commits) != 1 {
			return nil, fmt.Errorf("history has multiple merge bases; choose a commit or Branch tip")
		}
		commit = commits[0]
	}
	selection.baseRevision, err = e.readBase(ctx, commit)
	return selection, err
}

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func (e *editor) readBase(ctx context.Context, commit string) (*baseRevision, error) {
	if !objectID.MatchString(commit) {
		return nil, fmt.Errorf("invalid base commit; select the base again")
	}
	if e.cachedBase != nil && e.cachedBase.Commit == commit {
		return e.cachedBase, nil
	}
	files, err := readGitPrompts(ctx, e.repo, commit)
	if err != nil {
		return nil, err
	}
	base := &baseRevision{Commit: commit, catalogView: catalogView{Sources: map[string]source{}, Fields: map[string][]prompts.Field{}}}
	for name, file := range files {
		if strings.HasPrefix(name, "content/") && strings.HasSuffix(name, ".md") {
			base.Sources[name] = source{string(file.Data), revision(file.Data)}
		}
	}
	manifest, ok := files[prompts.ManifestPath]
	if !ok {
		base.Unavailable = "Composed comparison unavailable: this revision has no catalog manifest."
	} else {
		base.catalog, err = prompts.LoadManifest(manifest.Data, files)
		if err != nil {
			base.Unavailable = "Composed comparison unavailable: " + err.Error()
		} else {
			v := view(base.catalog)
			base.Recipients, base.Fields = v.Recipients, v.Fields
		}
	}
	e.cachedBase = base
	return base, nil
}

func readGitPrompts(ctx context.Context, repo, commit string) (fstest.MapFS, error) {
	data, err := gitRead(ctx, repo, nil, "ls-tree", "-r", "-z", commit, "--", "internal/prompts/")
	if err != nil {
		return nil, err
	}
	var names, ids []string
	for _, entry := range bytes.Split(data, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, path, ok := strings.Cut(string(entry), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("invalid Git tree entry")
		}
		name := strings.TrimPrefix(path, "internal/prompts/")
		if name != prompts.ManifestPath && !(strings.HasPrefix(name, "content/") && strings.HasSuffix(name, ".md")) {
			continue
		}
		if !fs.ValidPath(name) || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return nil, fmt.Errorf("base source is not a regular file: %s", path)
		}
		names, ids = append(names, name), append(ids, fields[2])
	}
	files := fstest.MapFS{}
	if len(ids) == 0 {
		return files, nil
	}
	data, err = gitRead(ctx, repo, []byte(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(bytes.NewReader(data))
	for i, name := range names {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != ids[i] || fields[1] != "blob" {
			return nil, fmt.Errorf("invalid Git blob: %s", name)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 || size > len(data) {
			return nil, fmt.Errorf("invalid Git blob size: %s", name)
		}
		contents := make([]byte, size)
		if _, err := io.ReadFull(reader, contents); err != nil {
			return nil, err
		}
		if newline, err := reader.ReadByte(); err != nil || newline != '\n' {
			return nil, fmt.Errorf("invalid Git blob boundary: %s", name)
		}
		files[name] = &fstest.MapFile{Data: contents, Mode: 0644}
	}
	return files, nil
}

type previewSide struct {
	Status string          `json:"status"`
	Result *prompts.Result `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func renderSide(c *prompts.Catalog, recipient, event string, values prompts.Values) previewSide {
	fields, err := c.Fields(recipient, event)
	if err != nil {
		return previewSide{Status: "absent"}
	}
	filtered := prompts.Values{}
	for _, field := range fields {
		if value, ok := values[field.Name]; ok {
			filtered[field.Name] = value
		}
	}
	result, err := c.Render(recipient, event, filtered)
	if err != nil {
		return previewSide{Status: "invalid", Error: err.Error()}
	}
	return previewSide{Status: "present", Result: &result}
}

type comparison struct {
	Base       previewSide    `json:"base"`
	Current    previewSide    `json:"current"`
	SourceDiff string         `json:"source_diff"`
	PromptDiff string         `json:"prompt_diff"`
	Values     prompts.Values `json:"values"`
}

func (e *editor) compare(ctx context.Context, request editRequest) (*comparison, error) {
	base, err := e.readBase(ctx, request.BaseCommit)
	if err != nil {
		return nil, err
	}
	result := &comparison{Base: previewSide{Status: "unavailable", Error: base.Unavailable}}
	snapshot, _, err := e.dataset(request.DraftID, request.ReviewID)
	if err != nil {
		return nil, err
	}
	scenarios, err := e.selectedScenarios(snapshot, request.ReviewID)
	if err != nil {
		return nil, err
	}
	if base.catalog != nil {
		result.Base, _ = renderScenario(base.catalog, request, scenarios)
	}
	current, err := snapshot.load(request.Drafts)
	if err != nil {
		result.Current = previewSide{Status: "invalid", Error: err.Error()}
	} else {
		result.Current, result.Values = renderScenario(current, request, scenarios)
	}
	if request.Path != "" {
		before, was := base.Sources[request.Path]
		_, now := snapshot.Sources[request.Path]
		if !was && !now {
			return nil, fmt.Errorf("unregistered source: %s", request.Path)
		}
		after := ""
		if now {
			if draft, ok := request.Drafts[request.Path]; ok {
				after = draft
			} else {
				after = snapshot.Sources[request.Path].Text
			}
		}
		result.SourceDiff, err = unifiedDiff(ctx, before.Text, after)
		if err != nil {
			return nil, err
		}
	}
	if result.Base.Status != "unavailable" && result.Base.Status != "invalid" && result.Current.Status != "invalid" {
		before, after := "", ""
		if result.Base.Result != nil {
			before = result.Base.Result.Text
		}
		if result.Current.Result != nil {
			after = result.Current.Result.Text
		}
		result.PromptDiff, err = unifiedDiff(ctx, before, after)
	}
	return result, err
}

func unifiedDiff(ctx context.Context, before, after string) (string, error) {
	if before == after {
		return "", nil
	}
	dir, err := os.MkdirTemp("", "prompt-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	for name, text := range map[string]string{"base": before, "working": after} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			return "", err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := gitCommand(ctx, dir, "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", "--text", "--diff-algorithm=myers", "--", "base", "working")
	output, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		err = nil
	}
	return string(output), err
}
