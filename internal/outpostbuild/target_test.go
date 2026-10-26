package outpostbuild_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/outpostbuild"
)

func TestNamedInstancesRefuseTheDefaultInstallTarget(t *testing.T) {
	for _, test := range []struct {
		instance, path string
		refused        bool
	}{
		{"default", "/home/user/.local/bin/attn", false},
		{"check", "/home/user/.local/bin/attn", true},
		{"check", "/home/user/.local/bin/./attn", true},
		{"check", "/home/user/.local/bin/attn-check", false},
		{"check", "/home/user/check/attn", false},
	} {
		err := outpostbuild.CheckInstallTarget(test.instance, test.path, "/home/user")
		if (err != nil) != test.refused {
			t.Errorf("%s at %s: %v", test.instance, test.path, err)
		}
		if err != nil && (!strings.Contains(err.Error(), test.instance) || !strings.Contains(err.Error(), "/home/user/.local/bin/attn")) {
			t.Errorf("refusal omits instance or target: %v", err)
		}
	}
}
