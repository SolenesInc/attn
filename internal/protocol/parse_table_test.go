package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestEveryDecoderReturnsAPointerToItsMessage(t *testing.T) {
	for cmd := range messageDecoders {
		got, msg, err := ParseMessage([]byte(`{"cmd":"` + cmd + `"}`))
		if err != nil {
			if !strings.Contains(err.Error(), "unmarshal "+cmd) {
				t.Errorf("%s: err = %v, want it to name the command", cmd, err)
			}
			continue
		}
		if got != cmd {
			t.Errorf("%s: parsed as %q", cmd, got)
		}
		if reflect.ValueOf(msg).Kind() != reflect.Pointer || reflect.ValueOf(msg).IsNil() {
			t.Errorf("%s: decoded to %T, want a non-nil pointer", cmd, msg)
		}
	}
}

func TestAMalformedMessageNamesItsCommand(t *testing.T) {
	_, _, err := ParseMessage([]byte(`{"cmd":"desktop_create","setup_id":7}`))
	if err == nil || !strings.Contains(err.Error(), "unmarshal desktop_create") {
		t.Fatalf("err = %v, want it to name desktop_create", err)
	}
}
