package prompts

import (
	"io"
	"io/fs"
	"strings"
)

type skillFS struct {
	fs.FS
	source string
	root   string
}

func (s skillFS) Open(name string) (fs.File, error) {
	if name == s.root {
		return s.FS.Open(s.source)
	}
	return s.FS.Open(s.source + "/" + strings.TrimPrefix(name, s.root+"/"))
}

func (s skillFS) ReadFile(name string) ([]byte, error) {
	f, err := s.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func AttnSkillFiles() fs.ReadFileFS {
	return skillFS{FS: content, source: "content/skills/attn", root: "attn_skill"}
}

func AttnWorkflowSkillFiles() fs.ReadFileFS {
	return skillFS{FS: content, source: "content/skills/attn-workflow", root: "attn_workflow_skill"}
}

func skillRecipient() Recipient {
	r := Recipient{ID: "attn-skill", Description: "Installed skill and references. Availability does not establish that the harness loaded them."}
	entries, err := fs.Glob(content, "content/skills/attn/references/*.md")
	if err != nil {
		panic(err)
	}
	r.Events = append(r.Events, On("available", "available_skill", "Installed for supported harnesses by internal/agent; the harness decides when to load it.", Document("attn-skill", "content/skills/attn/SKILL.md")))
	for _, source := range entries {
		name := strings.TrimSuffix(strings.TrimPrefix(source, "content/skills/attn/references/"), ".md")
		r.Events = append(r.Events, On(name, "reference", "Loaded on demand through attn skill show or the installed skill directory.", Document("attn-skill."+name, source)))
	}
	return r
}

func workflowSkillRecipient() Recipient {
	r := Recipient{ID: "attn-workflow-skill", Description: "Installed workflow skill and process references. Availability does not establish that the harness loaded them."}
	entries, err := fs.Glob(content, "content/skills/attn-workflow/references/*.md")
	if err != nil {
		panic(err)
	}
	r.Events = append(r.Events, On("available", "available_skill", "Installed for supported harnesses after explicit opt-in; the harness decides when to load it.", Document("attn-workflow-skill", "content/skills/attn-workflow/SKILL.md")))
	for _, source := range entries {
		name := strings.TrimSuffix(strings.TrimPrefix(source, "content/skills/attn-workflow/references/"), ".md")
		r.Events = append(r.Events, On(name, "reference", "Loaded on demand through the installed attn-workflow skill.", Document("attn-workflow-skill."+name, source)))
	}
	return r
}
