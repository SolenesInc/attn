package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedArtifactFlagsChooseTheKind(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    *protocol.SeedArtifactReference
		refusal string
	}{
		{
			name: "a path is a markdown file",
			args: []string{"--path", "docs/plans/x.md"},
			want: &protocol.SeedArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: protocol.Ptr("docs/plans/x.md")},
		},
		{
			name: "a repository beside a path tells two worktrees apart",
			args: []string{"--path", "docs/plans/x.md", "--repo", "attn"},
			want: &protocol.SeedArtifactReference{
				Kind: garden.ArtifactMarkdownFile,
				Path: protocol.Ptr("docs/plans/x.md"), Repository: protocol.Ptr("attn"),
			},
		},
		{
			name: "a notebook id is a notebook document",
			args: []string{"--notebook", "nb-7"},
			want: &protocol.SeedArtifactReference{Kind: garden.ArtifactNotebook, NotebookDocumentID: protocol.Ptr("nb-7")},
		},
		{
			name: "a url is a url",
			args: []string{"--url", "https://example.test/pr/1"},
			want: &protocol.SeedArtifactReference{Kind: garden.ArtifactURL, URL: protocol.Ptr("https://example.test/pr/1")},
		},
		{
			name:    "no reference at all",
			args:    nil,
			refusal: "name the document",
		},
		{
			name:    "two documents in one call",
			args:    []string{"--path", "x.md", "--url", "https://example.test"},
			refusal: "--path and --url were all given",
		},
		{
			name:    "a repository with nothing to qualify",
			args:    []string{"--repo", "attn"},
			refusal: "name the document",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSeedFlags("attach")
			f.parse("attach", append([]string{"s-7k3f9m"}, tc.args...))
			got, err := f.artifact()
			if tc.refusal != "" {
				if err == nil {
					t.Fatalf("accepted %v; wanted a refusal naming %q", tc.args, tc.refusal)
				}
				if !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("refusal %q does not name %q", err, tc.refusal)
				}
				return
			}
			if err != nil {
				t.Fatalf("artifact(): %v", err)
			}
			if got.Kind != tc.want.Kind ||
				protocol.Deref(got.Path) != protocol.Deref(tc.want.Path) ||
				protocol.Deref(got.Repository) != protocol.Deref(tc.want.Repository) ||
				protocol.Deref(got.NotebookDocumentID) != protocol.Deref(tc.want.NotebookDocumentID) ||
				protocol.Deref(got.URL) != protocol.Deref(tc.want.URL) {
				t.Fatalf("reference = %+v, want %+v", got, tc.want)
			}
			if _, err := garden.ValidateArtifact(garden.ArtifactReference{
				Kind:               got.Kind,
				Path:               protocol.Deref(got.Path),
				Repository:         protocol.Deref(got.Repository),
				NotebookDocumentID: protocol.Deref(got.NotebookDocumentID),
				URL:                protocol.Deref(got.URL),
			}); err != nil {
				t.Fatalf("the daemon refuses what the CLI composed: %v", err)
			}
		})
	}
}

func TestSeedArtifactTransferFlags(t *testing.T) {
	cases := []struct {
		name        string
		verb        string
		args        []string
		handled     bool
		operation   string
		source      string
		filename    string
		destination string
		refusal     string
	}{
		{
			name: "move owns the local source",
			verb: "attach", args: []string{"--path", "work/plan.bin", "--move"}, handled: true,
			operation: "move", source: "work/plan.bin",
		},
		{
			name: "copy owns an independent snapshot",
			verb: "attach", args: []string{"--copy", "--path", "work/report.pdf"}, handled: true,
			operation: "copy", source: "work/report.pdf",
		},
		{
			name: "local attach needs one transfer choice",
			verb: "attach", args: []string{"--path", "work/plan.bin"}, handled: true,
			refusal: "exactly one of --move or --copy",
		},
		{
			name: "local attach refuses two transfer choices",
			verb: "attach", args: []string{"--path", "work/plan.bin", "--move", "--copy"}, handled: true,
			refusal: "exactly one of --move or --copy",
		},
		{
			name: "repository path remains a link",
			verb: "attach", args: []string{"--path", "docs/plan.md", "--repo", "attn"}, handled: false,
		},
		{
			name: "transfer flags do not apply to repository links",
			verb: "attach", args: []string{"--path", "docs/plan.md", "--repo", "attn", "--copy"}, handled: true,
			refusal: "apply only to a local --path without --repo",
		},
		{
			name: "detach names the file and destination",
			verb: "detach", args: []string{"--path", "report.pdf", "--to", "exports/report.pdf"}, handled: true,
			operation: "detach", filename: "report.pdf", destination: "exports/report.pdf",
		},
		{
			name: "managed detach needs a destination",
			verb: "detach", args: []string{"--path", "report.pdf"}, handled: true,
			refusal: "requires --to <destination>",
		},
		{
			name: "legacy path removal stays separate",
			verb: "detach", args: []string{"--path", "old/report.pdf", "--reference"}, handled: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSeedFlags(tc.verb)
			f.parse(tc.verb, append([]string{"s-7k3f9m"}, tc.args...))
			got, handled, err := f.artifactTransferPlan(tc.verb)
			if handled != tc.handled {
				t.Fatalf("handled = %t, want %t", handled, tc.handled)
			}
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("refusal = %v, want text %q", err, tc.refusal)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !handled {
				return
			}
			if got.operation != tc.operation || got.filename != tc.filename {
				t.Fatalf("plan = %+v", got)
			}
			if tc.source != "" {
				want, err := filepath.Abs(tc.source)
				if err != nil {
					t.Fatal(err)
				}
				if got.source != want {
					t.Fatalf("source = %q, want %q", got.source, want)
				}
			}
			if tc.destination != "" {
				want, err := filepath.Abs(tc.destination)
				if err != nil {
					t.Fatal(err)
				}
				if got.destination != want {
					t.Fatalf("destination = %q, want %q", got.destination, want)
				}
			}
		})
	}
}
