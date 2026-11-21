package catalog

import (
	"strings"
	"testing"
	"testing/fstest"
)

const goodTypes = `types:
  seed:
    json: {type: string, minLength: 1}
    placeholder: "<id>"
`

const goodServer = `server:
  name: attn
  instructions: Reach for attn to track work.
`

const goodNotOffered = `commands:
  - {name: daemon, usage: "[ensure|stop]", summary: run the daemon, offered: false, reason: Stops the process that serves the call.}
`

const goodFamily = `groups:
  - {name: seed, usage: "<command>", summary: track work}
commands:
  - name: seed show
    summary: one seed
    runs: home
    grant: own
    doc: Reads one seed.
    args:
      - {name: seed, type: seed, required: true, cli: pos}
`

func catalogFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		TypesFile:   {Data: []byte(goodTypes)},
		ServerFile:  {Data: []byte(goodServer)},
		"seed.yaml": {Data: []byte(goodFamily)},
	}
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

func TestLoadAcceptsAFamilyAndACommandNotOffered(t *testing.T) {
	c, err := Load(catalogFS(map[string]string{"not-offered.yaml": goodNotOffered}))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, top := range c.TopLevel() {
		names = append(names, top.Name)
	}
	if strings.Join(names, " ") != "daemon seed" {
		t.Fatalf("top-level names %v, want daemon and seed", names)
	}
}

func TestLoadRefusesWhatWouldBreakAHarnessOrAnUnmarkedCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "a null schema keyword",
			files: map[string]string{TypesFile: strings.Replace(goodTypes, "minLength: 1}", "minLength: 1, required: null}", 1)},
			want:  []string{"types.yaml", `type "seed", used by seed show`, `schema keyword "required" is null`},
		},
		{
			name: "an empty-object union",
			files: map[string]string{TypesFile: goodTypes + `  either:
    json: {anyOf: [{type: string}, {}]}
`},
			want: []string{"types.yaml", `type "either"`, `"anyOf" holds an empty object at 1`},
		},
		{
			name:  "an intent map over Claude Code's cut",
			files: map[string]string{ServerFile: "server:\n  name: attn\n  instructions: " + strings.Repeat("x", 4097) + "\n"},
			want:  []string{"server.yaml", "4097 characters", "limit of 4096"},
		},
		{
			name:  "a command without runs",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "    runs: home\n", "", 1)},
			want:  []string{"seed.yaml: seed show: runs is missing", "home or here"},
		},
		{
			name:  "a command that runs nowhere attn knows",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "runs: home", "runs: outpost", 1)},
			want:  []string{"seed.yaml: seed show", `runs is "outpost"`, "home and here"},
		},
		{
			name:  "a command without a grant",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "    grant: own\n", "", 1)},
			want:  []string{"seed.yaml: seed show: grant is missing", "any, own, full, read_repo"},
		},
		{
			name:  "a grant that is no gate and no grant name",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "grant: own", "grant: seed", 1)},
			want:  []string{"seed.yaml: seed show", `grant is "seed"`, "push_default_branch"},
		},
		{
			name:  "a requirement waived by an arg the command lacks",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "cli: pos}", "cli: pos, unless: clear}", 1)},
			want:  []string{"seed.yaml: seed show", `arg "seed": unless names "clear"`},
		},
		{
			name:  "an error code without its fix",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "grant: own", "grant: own\n    errors: [{code: no_such_check}]", 1)},
			want:  []string{"seed.yaml: seed show", `error "no_such_check" has no fix`},
		},
		{
			name:  "a may_need that is no grant name",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "grant: own", "grant: own\n    may_need: [full]", 1)},
			want:  []string{"seed.yaml: seed show", `may_need names "full"`},
		},
		{
			name:  "a command whose family has no group",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "  - {name: seed, usage: \"<command>\", summary: track work}\n", "", 1)},
			want:  []string{"seed.yaml: seed show", `no group is named "seed"`},
		},
		{
			name:  "a group summary of more than one line",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "summary: track work}", "summary: \"track\\nwork\"}", 1)},
			want:  []string{"seed.yaml", `group "seed": summary is more than one line`},
		},
		{
			name:  "a command not offered without its reason",
			files: map[string]string{"not-offered.yaml": strings.Replace(goodNotOffered, ", reason: Stops the process that serves the call.", "", 1)},
			want:  []string{"not-offered.yaml: daemon: offered is false without a reason"},
		},
		{
			name:  "an offered command outside the command layer",
			files: map[string]string{"not-offered.yaml": strings.Replace(goodNotOffered, "offered: false, ", "", 1)},
			want:  []string{"not-offered.yaml: daemon: runs is missing"},
		},
		{
			name:  "a command outside the command layer that names a grant",
			files: map[string]string{"not-offered.yaml": strings.Replace(goodNotOffered, "offered: false,", "offered: false, grant: own,", 1)},
			want:  []string{"not-offered.yaml: daemon", "belong to a command the command layer runs"},
		},
		{
			name:  "a written usage on a command the layer runs",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "grant: own", "grant: own\n    usage: \"<id>\"", 1)},
			want:  []string{"seed.yaml: seed show", "usage is generated from the args"},
		},
		{
			name:  "a hidden command that is offered",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "grant: own", "grant: own\n    hidden: true", 1)},
			want:  []string{"seed.yaml: seed show", "hidden is for a command that is not offered"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(catalogFS(tc.files))
			if err == nil {
				t.Fatalf("Load accepted the catalog")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}
