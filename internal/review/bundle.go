package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

// MaxStopFileBytes is the largest file a stop shows: the size the file view
// reads and writes.
const MaxStopFileBytes = fsdoc.MaxFileSize

// MaxBundleBytes keeps a bundle inside one socket frame, with room for the
// document it travels beside.
const MaxBundleBytes = protocol.MaxSocketFrameBytes - 2<<20

var fileStatus = map[byte]string{'A': "added", 'D': "deleted", 'M': "modified", 'R': "renamed", 'C': "copied", 'T': "type_changed"}

// Resolve turns a review document into its bundle. Every problem names its
// document line and the fix; the parts that resolve are kept.
func Resolve(ctx context.Context, client *git.Client, document, text string) protocol.ReviewBundle {
	doc := parse(text)
	bundle := protocol.ReviewBundle{
		Frame:      FrameBrief,
		Stops:      []protocol.ReviewStop{},
		Files:      []protocol.ReviewFile{},
		Flows:      []protocol.ReviewFlow{},
		NotShown:   []protocol.ReviewNotShown{},
		Navigation: []protocol.ReviewNavigation{},
	}
	r := resolver{ctx: ctx, client: client, document: document, doc: &doc}
	r.readMeta(&bundle)
	if r.root != "" {
		r.loadChange()
		for _, s := range doc.stops {
			if resolved, ok := r.resolveStop(s); ok {
				bundle.Stops = append(bundle.Stops, resolved)
			}
		}
		bundle.Files = r.files()
		r.checkCoverage()
	}
	bundle.Flows = r.flows()
	for _, entry := range doc.notShown {
		bundle.NotShown = append(bundle.NotShown, protocol.ReviewNotShown{Line: entry.line, Path: entry.path, Reason: entry.reason})
	}
	sort.SliceStable(doc.problems, func(i, j int) bool { return doc.problems[i].Line < doc.problems[j].Line })
	bundle.Problems = append([]protocol.ReviewProblem{}, doc.problems...)
	if encoded, err := json.Marshal(bundle); err == nil && len(encoded) > MaxBundleBytes {
		bundle.Stops = []protocol.ReviewStop{}
		bundle.Problems = append(bundle.Problems, protocol.ReviewProblem{Message: fmt.Sprintf("the bundle is %d bytes; a review bundle travels in one frame of at most %d bytes, so no stop is shown. Split the review into several documents, or shorten its stops.", len(encoded), MaxBundleBytes)})
	}
	return bundle
}

// StopFiles are the files a document's stops show, absolute, so a watcher can
// tell when the bundle changes. A document that does not name its repository
// has none.
func StopFiles(text string) []string {
	doc := parse(text)
	root, ok := doc.meta.get("repository")
	if !ok || !filepath.IsAbs(root.value) {
		return nil
	}
	seen := map[string]bool{}
	var files []string
	for _, s := range doc.stops {
		file := filepath.Join(root.value, filepath.FromSlash(s.path))
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}
	return files
}

type resolver struct {
	ctx        context.Context
	client     *git.Client
	document   string
	doc        *document
	root       string
	base, head string
	headLine   int
	// Paths at head mapped to their path at base, for files renamed between.
	basePath map[string]string
	change   []git.FileChange
	changeOK bool
}

func (r *resolver) readMeta(bundle *protocol.ReviewBundle) {
	meta := r.doc.meta
	if attn, _ := meta.get("attn"); attn.value != "review" {
		r.doc.problem(1, "the document does not declare `attn: review` in its frontmatter. Run `attn review start <document>` to write it.")
	}
	if frame, ok := meta.get("frame"); ok {
		switch frame.value {
		case FrameBrief, FrameWalkthrough, FrameMap:
			bundle.Frame = frame.value
		default:
			r.doc.problem(frame.line, "frame: %s is not a frame. Use brief, walkthrough or map.", frame.value)
		}
	}
	repository, ok := meta.get("repository")
	switch {
	case !ok:
		r.doc.problem(1, "the frontmatter names no repository. Run `attn review start <document>` from the repository under review.")
		return
	case !filepath.IsAbs(repository.value):
		r.doc.problem(repository.line, "repository: %s must be an absolute path. Run `attn review start <document>` from the repository under review.", repository.value)
		return
	}
	bundle.Repository = repository.value
	head, ok := meta.get("head")
	if !ok {
		r.doc.problem(1, "the frontmatter has no head. Run `attn review start <document>` to take the snapshot.")
		return
	}
	resolvedHead, err := r.client.ResolveCommit(r.ctx, repository.value, head.value)
	if err != nil {
		r.doc.problem(head.line, "head: %s: %v. Run `attn review start <document>` again to take a fresh snapshot.", head.value, err)
		return
	}
	r.root, r.head, r.headLine = repository.value, resolvedHead, head.line
	bundle.Head = head.value
	if base, ok := meta.get("base"); ok && base.value != "" {
		resolvedBase, err := r.client.ResolveCommit(r.ctx, r.root, base.value)
		if err != nil {
			r.doc.problem(base.line, "base: %s: %v. Pass a commit with `attn review start <document> --base <commit>`.", base.value, err)
		} else {
			r.base = resolvedBase
			bundle.Base = protocol.Ptr(base.value)
		}
	}
}

func (r *resolver) loadChange() {
	r.basePath = map[string]string{}
	if r.base == "" {
		return
	}
	headChange, err := r.client.TreeChanges(r.ctx, r.root, r.base, r.head)
	if err != nil {
		r.doc.problem(r.headLine, "the change from base to head could not be read: %v", err)
		return
	}
	for _, change := range headChange {
		if change.Status == 'R' {
			r.basePath[change.Path] = change.OldPath
		}
	}
	change, err := r.client.WorktreeChanges(r.ctx, r.root, r.base, UntrackedLimits)
	if err != nil {
		r.doc.problem(r.headLine, "the change from base to now could not be read: %v", err)
		return
	}
	for _, file := range change {
		if filepath.Join(r.root, filepath.FromSlash(file.Path)) != r.document {
			r.change = append(r.change, file)
		}
	}
	r.changeOK = true
}

func (r *resolver) blobs(specs ...string) [][]string {
	blobs, err := r.client.ReadBlobs(r.ctx, r.root, specs)
	out := make([][]string, len(specs))
	if err != nil {
		return out
	}
	for i, blob := range blobs {
		if blob.Found {
			out[i] = splitLines(string(blob.Content))
			if out[i] == nil {
				out[i] = []string{}
			}
		}
	}
	return out
}

func (r *resolver) resolveStop(s stop) (protocol.ReviewStop, bool) {
	basePath := s.path
	if renamed, ok := r.basePath[s.path]; ok {
		basePath = renamed
	}
	specs := []string{r.head + ":" + s.path}
	if r.base != "" {
		specs = append(specs, r.base+":"+basePath)
	}
	texts := r.blobs(specs...)
	head := texts[0]
	var base []string
	if r.base != "" {
		base = texts[1]
	}
	now, nowHash, gone, err := readNow(filepath.Join(r.root, filepath.FromSlash(s.path)))
	if err != nil {
		r.doc.problem(s.line, "%s: %v", s.path, err)
		return protocol.ReviewStop{}, false
	}
	resolved := protocol.ReviewStop{HeadingLine: s.line, Path: s.path, From: s.from, To: s.to, FileHash: nowHash, Rows: []protocol.ReviewRow{}, Notes: []protocol.ReviewNote{}}
	presented := head
	if presented == nil {
		if base == nil {
			r.doc.problem(s.line, "%s is in neither head nor base. Check the path, or run `attn review start <document>` again if the file is new.", s.path)
			return protocol.ReviewStop{}, false
		}
		presented = base
	}
	if len(presented) == 0 {
		r.doc.problem(s.line, "%s is empty at head, so it has no lines to show.", s.path)
		return protocol.ReviewStop{}, false
	}
	if s.to > len(presented) {
		at := "head"
		if head == nil {
			at = "base, where it was deleted"
		}
		if s.from > len(presented) {
			r.doc.problem(s.line, "the stop %s:%d-%d starts past the end of the file (%d lines at %s). Use a range inside 1-%d.", s.path, s.from, s.to, len(presented), at, len(presented))
			return protocol.ReviewStop{}, false
		}
		r.doc.problem(s.line, "the stop %s:%d-%d ends past the file (%d lines at %s). Use %d-%d.", s.path, s.from, s.to, len(presented), at, s.from, len(presented))
		s.to = len(presented)
		resolved.To = s.to
	}
	if gone {
		from, to := s.from, s.to
		inBase := base != nil
		if head != nil && inBase {
			from, to, inBase = newLineMap(head, base).span(s.from, s.to)
		}
		resolved.ChangedSincePresented = head != nil
		if inBase {
			for line := from; line <= to; line++ {
				resolved.Rows = append(resolved.Rows, protocol.ReviewRow{Kind: "-", Old: protocol.Ptr(line), Text: base[line-1]})
			}
		}
		r.resolveNotes(s, &resolved, nil)
		return resolved, true
	}
	if head == nil {
		r.doc.problem(s.line, "%s was deleted at head but exists now. Run `attn review start <document>` again to take a fresh snapshot.", s.path)
		return protocol.ReviewStop{}, false
	}
	headToNow := newLineMap(head, now)
	nowFrom, nowTo, inNow := headToNow.span(s.from, s.to)
	if !inNow {
		resolved.ChangedSincePresented = true
		for line, text := range base {
			resolved.Rows = append(resolved.Rows, protocol.ReviewRow{Kind: "-", Old: protocol.Ptr(line + 1), Text: text})
		}
		r.resolveNotes(s, &resolved, &headToNow)
		return resolved, true
	}
	resolved.NowFrom, resolved.NowTo = nowFrom, nowTo
	resolved.ChangedSincePresented = !equalLines(head[s.from-1:s.to], now[nowFrom-1:nowTo])
	switch {
	case r.base == "":
		for line := nowFrom; line <= nowTo; line++ {
			resolved.Rows = append(resolved.Rows, protocol.ReviewRow{Kind: " ", Now: protocol.Ptr(line), Text: now[line-1]})
		}
	default:
		if base == nil {
			base = []string{}
		}
		var complete bool
		resolved.Rows, complete = rows(base, now, nowFrom, nowTo)
		if !complete {
			r.overBudget(s, "base")
		}
	}
	if !headToNow.complete {
		r.overBudget(s, "head")
	}
	r.resolveNotes(s, &resolved, &headToNow)
	return resolved, true
}

func (r *resolver) overBudget(s stop, from string) {
	r.doc.problem(s.line, "%s changed too much from %s to now to diff line by line within %d search steps, so part of the stop shows as one replaced block. List the file under `## Not shown` if its rows do not help.", s.path, from, diffBudget)
}

func (r *resolver) resolveNotes(s stop, resolved *protocol.ReviewStop, headToNow *lineMap) {
	for _, n := range s.notes {
		if n.from < s.from || n.to > s.to {
			r.doc.problem(n.line, "the note L%s is outside the stop %s:%d-%d. Move it under a stop that shows those lines, or widen the range.", noteRange(n), s.path, s.from, s.to)
			continue
		}
		wire := protocol.ReviewNote{Line: n.line, From: n.from, To: n.to, Look: n.look, Text: n.text}
		if headToNow != nil {
			if from, to, ok := headToNow.span(n.from, n.to); ok {
				wire.NowFrom, wire.NowTo = from, to
			}
		}
		resolved.Notes = append(resolved.Notes, wire)
	}
}

func noteRange(n note) string {
	if n.from == n.to {
		return fmt.Sprint(n.from)
	}
	return fmt.Sprintf("%d-%d", n.from, n.to)
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readNow(path string) (lines []string, hash string, gone bool, err error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", true, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	if !info.Mode().IsRegular() {
		return nil, "", false, fmt.Errorf("not a regular file")
	}
	if info.Size() > MaxStopFileBytes {
		return nil, "", false, fmt.Errorf("the file is %d bytes; a stop shows files up to %d bytes. Leave it out and list it under `## Not shown`", info.Size(), MaxStopFileBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", false, err
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, "", false, fmt.Errorf("binary file; a stop shows text. List it under `## Not shown`")
	}
	sum := sha256.Sum256(content)
	lines = splitLines(string(content))
	if lines == nil {
		lines = []string{}
	}
	return lines, hex.EncodeToString(sum[:]), false, nil
}

func (r *resolver) files() []protocol.ReviewFile {
	files := []protocol.ReviewFile{}
	for _, change := range r.change {
		file := protocol.ReviewFile{Path: change.Path, Status: fileStatus[change.Status], Added: change.Added, Removed: change.Removed, Binary: change.Binary}
		if file.Status == "" {
			file.Status = "modified"
		}
		if change.OldPath != change.Path {
			file.OldPath = protocol.Ptr(change.OldPath)
		}
		files = append(files, file)
	}
	return files
}

// checkCoverage asks that every changed file is shown by a stop or listed
// under Not shown, so the reader knows the review covers the whole change.
func (r *resolver) checkCoverage() {
	if !r.changeOK {
		return
	}
	accounted := map[string]bool{}
	for _, s := range r.doc.stops {
		accounted[s.path] = true
	}
	changed := map[string]bool{}
	for _, change := range r.change {
		changed[change.Path] = true
	}
	for _, entry := range r.doc.notShown {
		path := strings.TrimSuffix(entry.path, "/")
		if changed[path] || r.coversDirectory(path) {
			accounted[path] = true
			continue
		}
		r.doc.problem(entry.line, "%s is listed under Not shown but did not change since base. Remove it.", entry.path)
	}
	var missing []string
	for _, change := range r.change {
		if !accounted[change.Path] && !r.underListedDirectory(change.Path) {
			missing = append(missing, change.Path)
		}
	}
	if len(missing) > 0 {
		r.doc.problem(0, "%d changed %s neither shown by a stop nor listed under `## Not shown`: %s. Add a stop, or list each as `- path: why it is left out`.", len(missing), plural(len(missing), "file is", "files are"), strings.Join(missing, ", "))
	}
}

func (r *resolver) coversDirectory(dir string) bool {
	for _, change := range r.change {
		if strings.HasPrefix(change.Path, dir+"/") {
			return true
		}
	}
	return false
}

func (r *resolver) underListedDirectory(path string) bool {
	for _, entry := range r.doc.notShown {
		if dir := strings.TrimSuffix(entry.path, "/"); strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (r *resolver) flows() []protocol.ReviewFlow {
	flows := []protocol.ReviewFlow{}
	for _, f := range r.doc.flows {
		wire := protocol.ReviewFlow{Line: f.line, Messages: []protocol.ReviewFlowMessage{}}
		for _, message := range f.messages {
			entry := protocol.ReviewFlowMessage{Line: message.line, Text: message.text}
			if ref := message.ref; ref != nil {
				if s, ok := r.stopShowing(*ref); ok {
					entry.StopLine = protocol.Ptr(s.line)
				} else {
					r.doc.problem(ref.line, "`%%%% stop: %s:%d-%d` names lines no stop shows. Add a stop `### %s:%d-%d`, or name a range inside an existing stop of %s.", ref.path, ref.from, ref.to, ref.path, ref.from, ref.to, ref.path)
				}
			}
			wire.Messages = append(wire.Messages, entry)
		}
		flows = append(flows, wire)
	}
	return flows
}

func (r *resolver) stopShowing(ref stopRef) (stop, bool) {
	for _, s := range r.doc.stops {
		if s.path == ref.path && s.from <= ref.from && ref.to <= s.to {
			return s, true
		}
	}
	return stop{}, false
}
