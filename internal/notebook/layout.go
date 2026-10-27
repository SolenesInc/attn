package notebook

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

const (
	FileIndex = "index.md"

	DirJournal   = "journal"
	DirKnowledge = "knowledge"

	machineDir = ".attn"
)

var paraSubdirs = []string{"projects", "areas", "resources", "archive"}

func DefaultRoot(home, instance string) string {
	base := filepath.Join(home, "attn-notebook")
	p := strings.ToLower(strings.TrimSpace(instance))
	if p == "" || p == "default" {
		return base
	}
	return base + "-" + p
}

func ProfileDefaultRoot(base, profileName string, taken func(string) (bool, error)) (string, error) {
	var slug strings.Builder
	for _, r := range strings.ToLower(profileName) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			slug.WriteRune(r)
		} else if slug.Len() > 0 && !strings.HasSuffix(slug.String(), "-") {
			slug.WriteByte('-')
		}
	}
	name := strings.Trim(slug.String(), "-")
	if name == "" {
		name = "profile"
	}
	root := base + "-" + name
	candidate := root
	for n := 2; ; n++ {
		exists, err := taken(candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", root, n)
	}
}

func TicketsDir(root string) string {
	return filepath.Join(root, machineDir, "tickets")
}

func TicketAttachmentsDir(root, ticketID string) string {
	return filepath.Join(TicketsDir(root), ticketID)
}

func TicketArtifactsDir(root, ticketID string) string {
	return filepath.Join(root, "tickets", ticketID)
}

func SeedArtifactsDir(root, seedID string) string {
	return filepath.Join(root, "seeds", seedID)
}

func SeedArtifactTransfersDir(root string) string {
	return filepath.Join(root, machineDir, "seed-artifact-transfers")
}

func CleanPath(p string) (string, error) {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return "", fmt.Errorf("notebook: empty path")
	}
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

Your Notebook is an [LLM Wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f),
Andrej Karpathy's pattern: a folder of markdown pages that your agents build and
keep current, so knowledge compounds instead of being rediscovered. You can
read, edit and sync it like any other folder.

- ` + "`journal/`" + `: the record of what happened, one file per day. Knowledge base
  pages cite it as their source.
- ` + "`knowledge/`" + `: the knowledge base, which is the wiki itself.
- ` + "`seeds/`" + `: files attached to seeds in the Garden.
- ` + "`crew/`" + `: one folder per crew member, with their charter and letters.

Every note has a ` + "`type`" + ` in its frontmatter. Link notes with root-absolute
markdown links, such as [an area note](/knowledge/areas/example.md).
`

const knowledgeIndexTemplate = `# Knowledge base

The wiki in your Notebook: pages your agents keep current as they learn. It
starts organized by PARA, and you can reshape it:

- ` + "`projects/`" + `: efforts with an end, one folder per project.
- ` + "`areas/`" + `: ongoing responsibilities, with no end.
- ` + "`resources/`" + `: reference material worth keeping.
- ` + "`archive/`" + `: finished or inactive material. Move a project's folder here when
  the project ends.

Each note's ` + "`sources:`" + ` lists where its facts came from: journal paths or URLs.
`

var paraIndexTemplates = map[string]string{
	"projects":  "# Projects\n\nBounded efforts with an end. One folder per project or epic; a\nproject's `index.md` links the plot that tracks it with\n`resource: attn:seed/<plot-id>`.\n",
	"areas":     "# Areas\n\nOngoing responsibilities and subsystems, with no end. Durable knowledge\npromoted out of finished projects lands here.\n",
	"resources": "# Resources\n\nReference material worth keeping across projects and areas.\n",
	"archive":   "# Archive\n\nFinished or inactive items. A project folder is moved here when its\nplot is harvested.\n",
}
