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

const goodFamily = `commands:
  - name: seed show
    summary: one seed
    runs: home
    grant: own
    wrong_args: 'needs exactly one seed id, got {count}'
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
			name:  "positionals without the CLI's text for a wrong count",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "    wrong_args: 'needs exactly one seed id, got {count}'\n", "", 1)},
			want:  []string{"seed.yaml: seed show: wrong_args is missing"},
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
