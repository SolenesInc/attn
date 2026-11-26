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
  description: attn's tools.
  intro: Reach for attn to track work.
  do: {title: Run commands, script: the script, text: Runs a script.}
  record: {title: Record, script: the script, text: Runs a script that records.}
`

const goodNotOffered = `commands:
  - {name: daemon, usage: "[ensure|stop]", summary: run the daemon, offered: false, reason: Stops the process that serves the call.}
`

const goodFamily = `groups:
  - name: seed
    usage: "<command>"
    summary: track work
    map:
      rows:
        - {need: Read a seed, tools: [seed_show]}
commands:
  - name: seed show
    summary: one seed
    runs: home
    grant: own
    effect: read
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

func TestABareCommandCarriesItsFamilysMap(t *testing.T) {
	c, err := Load(catalogFS(map[string]string{"list.yaml": "commands:\n  - {name: list, summary: list sessions, runs: home, grant: full, effect: read, doc: Lists sessions., map: {rows: [{need: See every session, tools: [list]}]}}\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Instructions(); !strings.Contains(got, "- See every session: list\n- Read a seed: seed_show") {
		t.Fatalf("instructions do not hold both families' rows in name order:\n%s", got)
	}
}

func TestLoadRefusesWhatWouldBreakAHarnessOrAnUnmarkedCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "an arg the CLI fills that a tool caller would have to pass",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "cli: pos}", "cli: internal}", 1)},
			want:  []string{"seed.yaml: seed show", `arg "seed"`, "cli internal on an arg that is required"},
		},
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
			files: map[string]string{ServerFile: strings.Replace(goodServer, "Reach for attn to track work.", strings.Repeat("x", 4097), 1)},
			want:  []string{"server.yaml", "characters, over the limit of 4096", "server.yaml 4097, seed.yaml 20"},
		},
		{
			name: "an attn_record description over Claude Code's cut once its signatures are in it",
			files: map[string]string{
				ServerFile:  strings.Replace(goodServer, "text: Runs a script that records.}", "text: "+strings.Repeat("x", 4080)+"}", 1),
				"seed.yaml": strings.Replace(goodFamily, "effect: read", "effect: read\n    upfront: true", 1),
			},
			want: []string{"server.yaml", "the description of attn_record is 4098 characters, over the limit of 4096"},
		},
		{
			name:  "a command that records which attn_record's description does not name",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "effect: read", "effect: record", 1)},
			want:  []string{"seed.yaml: seed show", "seed_show records, so server.yaml's record.text must name it"},
		},
		{
			name:  "an attn_record description over Claude Code's cut",
			files: map[string]string{ServerFile: strings.Replace(goodServer, "text: Runs a script that records.}", "text: "+strings.Repeat("x", 4097)+"}", 1)},
			want:  []string{"server.yaml", "the description of attn_record is 4097 characters, over the limit of 4096"},
		},
		{
			name:  "an arg only a script can pass that the CLI would need",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "cli: pos}", "cli: none}", 1)},
			want:  []string{"seed.yaml: seed show", `arg "seed"`, "cli none on an arg that is required"},
		},
		{
			name:  "a family that offers tools without a map",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "    map:\n      rows:\n        - {need: Read a seed, tools: [seed_show]}\n", "", 1)},
			want:  []string{"seed.yaml: family \"seed\"", "offers tools but has no map"},
		},
		{
			name:  "a map row that names a tool the family does not offer",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "tools: [seed_show]", "tools: [seed_show, ask]", 1)},
			want:  []string{"seed.yaml: family \"seed\"", `names "ask", which is not a tool of this family`},
		},
		{
			name:  "a map on a command that is not its whole family",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "    doc: Reads one seed.\n", "    doc: Reads one seed.\n    map: {rows: [{need: Read a seed, tools: [seed_show]}]}\n", 1)},
			want:  []string{"seed.yaml: seed show", "map belongs to the family"},
		},
		{
			name:  "a bare command that offers a tool without a map",
			files: map[string]string{"list.yaml": "commands:\n  - {name: list, summary: list sessions, runs: home, grant: full, effect: read, doc: Lists sessions.}\n"},
			want:  []string{"list.yaml: family \"list\"", "offers tools but has no map"},
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
			name:  "an offered command without an effect",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "    effect: read\n", "", 1)},
			want:  []string{"seed.yaml: seed show: effect is missing", "read, record or act"},
		},
		{
			name:  "an effect attn does not know",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "effect: read", "effect: write", 1)},
			want:  []string{"seed.yaml: seed show", `effect is "write"`, "read, record and act"},
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
			files: map[string]string{"seed.yaml": "commands:" + strings.SplitN(goodFamily, "commands:", 2)[1]},
			want:  []string{"seed.yaml: seed show", `no group is named "seed"`},
		},
		{
			name:  "a group summary of more than one line",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "summary: track work\n", "summary: \"track\\nwork\"\n", 1)},
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
			name:  "a receipt on a command that only reads",
			files: map[string]string{"seed.yaml": strings.Replace(goodFamily, "grant: own", "grant: own\n    result: SeedShowResult\n    receipt: [seed.id]", 1)},
			want:  []string{"seed.yaml: seed show", "a read answers whole"},
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
