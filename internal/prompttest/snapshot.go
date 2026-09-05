// Package prompttest compares runtime output with independently captured legacy prompts.
package prompttest

import (
	"embed"
	"encoding/json"
	"reflect"
	"testing"
)

//go:embed testdata/*.json
var fixtures embed.FS

func Equal(t *testing.T, name string, got map[string]string) {
	t.Helper()
	raw, err := fixtures.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for key, text := range want {
		if actual, ok := got[key]; !ok || actual != text {
			t.Errorf("%s/%s differs from the original builder:\nwant %q\n got %q", name, key, text, actual)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s: got %d cases, want %d", name, len(got), len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fail()
	}
}
