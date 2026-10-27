package analytics_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/analytics"
)

func TestRedactKeepsTheShapeOfEveryRealPayloadAndOnlyAllowlistedStrings(t *testing.T) {
	shapes := loadPayloadShapes(t)
	for name, payload := range shapes {
		t.Run(name, func(t *testing.T) {
			allow := analytics.SafeValues[name]
			redacted := analytics.Redact(payload, allow)
			if payload == nil {
				if redacted != nil {
					t.Fatalf("an empty payload redacted to %s, want nothing", redacted)
				}
				return
			}
			var in, out any
			decode(t, payload, &in)
			decode(t, redacted, &out)
			compareRedacted(t, "", in, out, allow)
		})
	}
}

func TestEveryAllowlistedFieldIsInARealPayloadShape(t *testing.T) {
	shapes := loadPayloadShapes(t)
	for name, allow := range analytics.SafeValues {
		payload, ok := shapes[name]
		if !ok || payload == nil {
			t.Errorf("SafeValues lists %s, which no recorded payload carries", name)
			continue
		}
		var shape any
		decode(t, payload, &shape)
		strings := map[string]bool{}
		collectStringPaths("", shape, strings)
		for path := range allow {
			if !strings[path] {
				t.Errorf("SafeValues[%s] allows %q, which is not a string field of its payload", name, path)
			}
		}
	}
}

func TestNoRealPayloadUsesAKeyTheDerivedTraitsTakeInProps(t *testing.T) {
	var reserved map[string]json.RawMessage
	derived, err := json.Marshal(analytics.Derived{Session: &analytics.SessionTraits{}, Repo: &analytics.RepoTraits{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(derived, &reserved); err != nil {
		t.Fatal(err)
	}
	for name, payload := range loadPayloadShapes(t) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(payload, &fields) != nil {
			continue
		}
		for key := range reserved {
			if _, taken := fields[key]; taken {
				t.Errorf("%s has a top-level %q field, which Props overwrites with derived traits", name, key)
			}
		}
	}
}

func TestRedactShipsNoTextOutsideTheAllowlist(t *testing.T) {
	for _, tc := range []struct {
		payload, want string
		allow         analytics.Allowlist
	}{
		{`{"path":"/Users/someone/repo","n":3,"ok":true,"none":null}`, `{"n":3,"none":null,"ok":true,"path":{"s":19}}`, nil},
		{`{"kind":"refresh_repository","path":"/a"}`, `{"kind":"refresh_repository","path":{"s":2}}`, analytics.Allowlist{"kind": true}},
		{`{"items":[{"kind":"x","title":"secret"}]}`, `{"items":[{"kind":"x","title":{"s":6}}]}`, analytics.Allowlist{"items.kind": true}},
		{`{"kind":{"kind":"nested"}}`, `{"kind":{"kind":{"s":6}}}`, analytics.Allowlist{"kind": true}},
		{`{"by_path":{"/Users/someone/repo":"dirty","0095c903-0123-4029":"idle"},"n":2}`, `{"by_path":{"n":2},"n":2}`, nil},
		{`{"empty":{}}`, `{"empty":{}}`, nil},
		{`"bare"`, `{"s":4}`, nil},
		{`{"big":12345678901234567890}`, `{"big":12345678901234567890}`, nil},
	} {
		if got := analytics.Redact(json.RawMessage(tc.payload), tc.allow); string(got) != tc.want {
			t.Errorf("Redact(%s, %v) = %s, want %s", tc.payload, tc.allow, got, tc.want)
		}
	}
}

func loadPayloadShapes(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/payload-shapes.json")
	if err != nil {
		t.Fatal(err)
	}
	var shapes map[string]json.RawMessage
	if err := json.Unmarshal(data, &shapes); err != nil {
		t.Fatal(err)
	}
	for name, payload := range shapes {
		if bytes.Equal(payload, []byte("null")) {
			shapes[name] = nil
		}
	}
	return shapes
}

func decode(t *testing.T, raw json.RawMessage, into *any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
}

func compareRedacted(t *testing.T, path string, in, out any, allow analytics.Allowlist) {
	t.Helper()
	switch v := in.(type) {
	case string:
		if allow[path] {
			if out != v {
				t.Errorf("%s: allowlisted string became %v", path, out)
			}
			return
		}
		redacted, ok := out.(map[string]any)
		if !ok || len(redacted) != 1 || redacted["s"] != json.Number(strconv.Itoa(len(v))) {
			t.Errorf("%s: string became %v, want {\"s\": %d}", path, out, len(v))
		}
	case map[string]any:
		got, ok := out.(map[string]any)
		if !ok || len(got) != len(v) {
			t.Fatalf("%s: object became %v", path, out)
		}
		for key, field := range v {
			child := key
			if path != "" {
				child = path + "." + key
			}
			compareRedacted(t, child, field, got[key], allow)
		}
	case []any:
		got, ok := out.([]any)
		if !ok || len(got) != len(v) {
			t.Fatalf("%s: array became %v", path, out)
		}
		for i := range v {
			compareRedacted(t, path, v[i], got[i], allow)
		}
	default:
		if out != in {
			t.Errorf("%s: %v became %v, want it unchanged", path, in, out)
		}
	}
}

func collectStringPaths(path string, value any, into map[string]bool) {
	switch v := value.(type) {
	case string:
		into[path] = true
	case map[string]any:
		for key, field := range v {
			child := key
			if path != "" {
				child = path + "." + key
			}
			collectStringPaths(child, field, into)
		}
	case []any:
		for _, item := range v {
			collectStringPaths(path, item, into)
		}
	}
}
