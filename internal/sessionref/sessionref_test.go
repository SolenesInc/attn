package sessionref_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionref"
)

func TestSessionReferencesReduceToTheKeysAndShortIDsOfTheSharedCorpus(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_ids.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []struct {
		Ref   string
		Key   *sessionref.Key
		Short string
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, line := range corpus {
		key, ok := sessionref.KeyOf(line.Ref)
		if ok != (line.Key != nil) || ok && key != *line.Key {
			t.Errorf("KeyOf(%q) = %d, %t, want %v", line.Ref, key, ok, line.Key)
		}
		if short := sessionref.Short(protocol.SessionID(line.Ref)); short != line.Short {
			t.Errorf("Short(%q) = %q, want %q", line.Ref, short, line.Short)
		}
	}
}
