package prompts

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

func TestEverySourceIsRegistered(t *testing.T) {
	sources := Builtin().Sources()
	err := fs.WalkDir(Files(), "content", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".md") {
			if _, ok := sources[path]; !ok {
				t.Errorf("unregistered prompt source: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEveryEventRendersAcrossItsConditions(t *testing.T) {
	catalog := Builtin()
	data, err := json.Marshal(catalog.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := LoadManifest(data, Files())
	if err != nil {
		t.Fatal(err)
	}
	for _, recipient := range catalog.Recipients() {
		for _, event := range recipient.Events {
			t.Run(recipient.ID+"/"+event.ID, func(t *testing.T) {
				fields, err := catalog.Fields(recipient.ID, event.ID)
				if err != nil {
					t.Fatal(err)
				}
				conditions := map[string]bool{}
				var visit func(Node)
				visit = func(n Node) {
					if n.Condition != nil {
						conditions[n.Condition.Field.Name] = true
					}
					for _, child := range n.Children {
						visit(child)
					}
					for _, binding := range n.Bindings {
						visit(binding.Node)
					}
				}
				visit(event.Body)
				var variables []Field
				base := Values{}
				for _, f := range fields {
					if f.Kind == "flag" {
						base[f.Name] = "false"
					} else {
						base[f.Name] = " literal {{token}} / λ / \"quote\"\nsecond line "
					}
					if conditions[f.Name] {
						variables = append(variables, f)
					}
				}
				for mask := 0; mask < 1<<len(variables); mask++ {
					values := Values{}
					for k, v := range base {
						values[k] = v
					}
					for i, f := range variables {
						if f.Kind == "flag" {
							values[f.Name] = fmt.Sprint(mask&(1<<i) != 0)
						} else if mask&(1<<i) == 0 {
							values[f.Name] = " \n\t"
						}
					}
					result, err := catalog.Render(recipient.ID, event.ID, values)
					if err != nil {
						t.Fatalf("case %d: %v", mask, err)
					}
					fromManifest, err := restored.Render(recipient.ID, event.ID, values)
					if err != nil || !reflect.DeepEqual(result, fromManifest) {
						t.Fatalf("manifest changed case %d: %v", mask, err)
					}
				}
			})
		}
	}
}
