package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
)

func main() {
	check := flag.Bool("check", false, "Fail if the generated prompt catalogs is stale")
	root := flag.String("repo", ".", "Repository root")
	flag.Parse()
	if err := generate(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(root string, check bool) error {
	catalog, err := prompts.Load(os.DirFS(filepath.Join(root, "internal/prompts")))
	if err != nil {
		return err
	}
	definition := catalog.Manifest()
	definition.DefinitionsHash, err = prompts.DefinitionsHash(os.DirFS(filepath.Join(root, "internal/prompts")))
	if err != nil {
		return err
	}
	manifest, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return err
	}
	if err := generateCatalog(root, check, "plugins/attn-pi/automode/prompts.generated.json", func(id string) bool { return strings.HasPrefix(id, "pi-") }); err != nil {
		return err
	}
	if err := generateCatalog(root, check, "app/src/prompts/catalog.generated.json", func(id string) bool { return id == "annotation-terminal" || id == "annotation-label" }); err != nil {
		return err
	}
	return writeGenerated(root, check, "internal/prompts/"+prompts.ManifestPath, manifest)
}

func generateCatalog(root string, check bool, output string, include func(string) bool) error {
	catalog, err := prompts.Load(os.DirFS(filepath.Join(root, "internal/prompts")))
	if err != nil {
		return err
	}
	var recipients []prompts.Recipient
	for _, recipient := range catalog.Recipients() {
		if include(recipient.ID) {
			recipients = append(recipients, recipient)
		}
	}
	exportedCatalog, err := prompts.New(os.DirFS(filepath.Join(root, "internal/prompts")), recipients...)
	if err != nil {
		return err
	}
	type parityCase struct {
		Recipient string         `json:"recipient"`
		Event     string         `json:"event"`
		Values    prompts.Values `json:"values"`
		SHA256    string         `json:"sha256"`
	}
	var parity []parityCase
	for _, recipient := range recipients {
		for _, event := range recipient.Events {
			if err := checkTypeScriptNode(event.Body); err != nil {
				return err
			}
			fields, _ := exportedCatalog.Fields(recipient.ID, event.ID)
			conditions := conditionalFields(event.Body)
			var variables []prompts.Field
			values := prompts.Values{}
			for _, field := range fields {
				if field.Kind == "flag" {
					values[field.Name] = "false"
				} else {
					values[field.Name] = field.Name + ": literal {{token}} / λ / \"quoted\"\nsecond line"
				}
				if conditions[field.Name] {
					variables = append(variables, field)
				}
			}
			for mask := 0; mask < 1<<len(variables); mask++ {
				sample := prompts.Values{}
				for name, value := range values {
					sample[name] = value
				}
				for i, field := range variables {
					if field.Kind == "flag" {
						sample[field.Name] = strconv.FormatBool(mask&(1<<i) != 0)
					} else if mask&(1<<i) == 0 {
						sample[field.Name] = " \n\t\u0085"
					}
				}
				result, err := exportedCatalog.Render(recipient.ID, event.ID, sample)
				if err != nil {
					return err
				}
				parity = append(parity, parityCase{recipient.ID, event.ID, sample, fmt.Sprintf("%x", sha256.Sum256([]byte(result.Text)))})
			}
		}
	}
	data, err := json.MarshalIndent(struct {
		Version    int                 `json:"version"`
		Recipients []prompts.Recipient `json:"recipients"`
		Sources    map[string]string   `json:"sources"`
		Parity     []parityCase        `json:"parity"`
	}{1, recipients, exportedCatalog.Sources(), parity}, "", "  ")
	if err != nil {
		return err
	}
	return writeGenerated(root, check, output, data)
}

func writeGenerated(root string, check bool, output string, data []byte) error {
	data = append(data, '\n')
	path := filepath.Join(root, output)
	existing, _ := os.ReadFile(path)
	if bytes.Equal(existing, data) {
		return nil
	}
	if check {
		return fmt.Errorf("%s is stale; run make generate-prompts", output)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".promptgen-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func checkTypeScriptNode(n prompts.Node) error {
	if (n.Kind != "text" && n.Kind != "input" && n.Kind != "when" && n.Kind != "choose" && n.Kind != "join" && n.Kind != "compose" && n.Kind != "trim") || n.Verbatim || n.Quote || n.Part != nil || (n.Field != nil && n.Field.Kind != "text") || (n.Condition != nil && n.Condition.Test != "enabled" && n.Condition.Test != "present") {
		return fmt.Errorf("TypeScript renderer does not support node %s (%s); extend it and the parity tests first", n.ID, n.Kind)
	}
	for _, binding := range n.Bindings {
		if err := checkTypeScriptNode(binding.Node); err != nil {
			return err
		}
	}
	for _, child := range n.Children {
		if err := checkTypeScriptNode(child); err != nil {
			return err
		}
	}
	return nil
}

func conditionalFields(node prompts.Node) map[string]bool {
	result := map[string]bool{}
	var visit func(prompts.Node)
	visit = func(n prompts.Node) {
		if n.Condition != nil {
			result[n.Condition.Field.Name] = true
		}
		for _, child := range n.Children {
			visit(child)
		}
		for _, binding := range n.Bindings {
			visit(binding.Node)
		}
	}
	visit(node)
	return result
}
