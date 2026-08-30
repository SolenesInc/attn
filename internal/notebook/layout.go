package notebook

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

const (
	FileIndex = "index.md"
	FileLog   = "log.md"
	FileInbox = "inbox.md"

	DirJournal   = "journal"
	DirKnowledge = "knowledge"

	machineDir = ".attn"
)

var paraSubdirs = []string{"projects", "areas", "resources", "archive"}

func DefaultRoot(home, profile string) string {
	base := filepath.Join(home, "attn-notebook")
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "default" {
		return base
	}
	return base + "-" + p
}

// Lives under .attn/, which CleanPath rejects, so it is written with direct
// filesystem I/O, not the Store APIs.
func TicketsDir(root string) string {
	return filepath.Join(root, machineDir, "tickets")
}

func TicketAttachmentsDir(root, ticketID string) string {
	return filepath.Join(TicketsDir(root), ticketID)
}

func TicketArtifactsDir(root, ticketID string) string {
	return filepath.Join(root, "tickets", ticketID)
}

// SeedArtifactsDir is filesystem-canonical storage, intentionally outside the
// Markdown-only Store APIs. Direct visible regular files are the membership.
func SeedArtifactsDir(root, seedID string) string {
	return filepath.Join(root, "seeds", seedID)
}

// SeedArtifactTransfersDir holds recovery receipts, never artifact membership.
func SeedArtifactTransfersDir(root string) string {
	return filepath.Join(root, machineDir, "seed-artifact-transfers")
}

func CleanPath(p string) (string, error) {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return "", fmt.Errorf("notebook: empty path")
	}
	// Anchor at "/" and Clean so any ".." is neutralized to within the root.
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(trimmed, "/")), "/")
	if rel == "" || rel == "." {
		return "", fmt.Errorf("notebook: %q is the root, not a file", p)
	}
	if !strings.HasSuffix(rel, ".md") {
		return "", fmt.Errorf("notebook: %q must be a .md file", p)
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" {
			return "", fmt.Errorf("notebook: %q has an empty path segment", p)
		}
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("notebook: %q has a dotfile/dotdir segment", p)
		}
	}
	return rel, nil
}

type scaffoldFile struct {
	relPath string
	content string
}

func scaffoldDirs() []string {
	dirs := []string{DirJournal, DirKnowledge}
	for _, sub := range paraSubdirs {
		dirs = append(dirs, path.Join(DirKnowledge, sub))
	}
	return dirs
}

func scaffoldFiles() []scaffoldFile {
	files := []scaffoldFile{
		{FileIndex, indexTemplate},
		{FileLog, logTemplate},
		{path.Join(DirKnowledge, FileIndex), knowledgeIndexTemplate},
	}
	for _, sub := range paraSubdirs {
		files = append(files, scaffoldFile{
			relPath: path.Join(DirKnowledge, sub, FileIndex),
			content: paraIndexTemplates[sub],
		})
	}
	return files
}

func ScaffoldPaths() []string {
	files := scaffoldFiles()
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.relPath
	}
	return out
}

const indexTemplate = `# Notebook

A durable, profile-wide markdown bundle — the journal attn writes on your behalf
and the knowledge base the chief of staff maintains. It outlives any single
workspace and is yours to read, edit, and sync.

- ` + "`journal/`" + ` — dated narrative of what was done, newest entries appended per day.
- ` + "`knowledge/`" + ` — the PARA-organized knowledge base (projects, areas, resources, archive).
- ` + "`log.md`" + ` — global change history, newest first.

This is one OKF bundle (Open Knowledge Format): every note carries a ` + "`type`" + `
frontmatter field, and links are root-absolute markdown, e.g.
[an area note](/knowledge/areas/example.md) — not wikilinks.

<!-- okf_version: 0.1 -->
`

const logTemplate = `# Log

Change history, newest first.
`

const inboxTemplate = `# Chief inbox

Selections sent to the chief of staff from the Notebook, oldest first.
`

const knowledgeIndexTemplate = `# Knowledge base

The chief of staff's durable knowledge, organized by the PARA method (the
directory axis) with an OKF ` + "`type`" + ` on every note (the frontmatter axis).

- ` + "`projects/`" + ` — bounded efforts with an end (one folder per project/epic).
- ` + "`areas/`" + ` — ongoing responsibilities and subsystems, with no end.
- ` + "`resources/`" + ` — reference material worth keeping.
- ` + "`archive/`" + ` — finished or inactive items, moved here when a project closes.

Every note is grounded — carry resolvable ` + "`sources:`" + ` (journal anchors
or URLs), never paraphrase alone.
`

var paraIndexTemplates = map[string]string{
	"projects":  "# Projects\n\nBounded efforts with an end. One folder per project or epic; a\nproject's `index.md` links the workspace that produced it with\n`resource: attn:workspace/<id>`.\n",
	"areas":     "# Areas\n\nOngoing responsibilities and subsystems, with no end. Durable knowledge\npromoted out of finished projects lands here.\n",
	"resources": "# Resources\n\nReference material worth keeping across projects and areas.\n",
	"archive":   "# Archive\n\nFinished or inactive items. A project folder is moved here when its\nworkspace closes.\n",
}
