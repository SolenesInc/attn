package main

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/victorarias/attn/internal/catalog"
)

const oneToolCatalog = `groups:
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
    tool: true
    doc: DOC
`

const oneServer = `server:
  name: attn
  description: attn's tools.
  intro: Reach for attn to track work.
  do: {title: Run commands, script: the script, text: Runs a script.}
  read: {title: Read, script: the script, text: Runs a script that reads.}
  record: {title: Record, script: the script, text: Runs a script that records.}
`

func TestTheGeneratorRefusesAToolDescriptionClaudeCodeWouldCut(t *testing.T) {
	c, err := catalog.Load(fstest.MapFS{
		catalog.TypesFile:  {Data: []byte("types: {}\n")},
		catalog.ServerFile: {Data: []byte(oneServer)},
		"seed.yaml":        {Data: []byte(strings.Replace(oneToolCatalog, "DOC", strings.Repeat("x", 4097), 1))},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = writeTools(c, t.TempDir())
	if want := "the description of seed_show is 4097 characters, over the limit of 4096"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("a 4,097-character description was generated with error %v, want %q", err, want)
	}
}
