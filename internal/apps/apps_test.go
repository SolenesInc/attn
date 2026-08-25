package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/docstore"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"approval-gate", "a", "app2", "standup-digest-v2", "9lives"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "-leading", "Approval", "with_underscore", "with space", "app/name", "app:name", strings.Repeat("a", MaxNameLength+1)} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", bad)
		}
	}
}

func TestAcceptedNamesMakeValidNamespaces(t *testing.T) {
	for _, name := range []string{"approval-gate", "a", "9lives", strings.Repeat("a", MaxNameLength)} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q): %v", name, err)
		}
		if err := docstore.ValidateNamespace(Namespace(name)); err != nil {
			t.Errorf("docstore rejects the namespace for app %q: %v", name, err)
		}
	}
}

func TestNameErrorsSayWhatIsWrong(t *testing.T) {
	err := ValidateName(strings.Repeat("a", MaxNameLength+1))
	if err == nil {
		t.Fatal("an over-long name was accepted")
	}
	if !strings.Contains(err.Error(), "65") || !strings.Contains(err.Error(), "64") {
		t.Fatalf("error does not name the ask and the limit: %v", err)
	}
}

func TestReservedNamesAreRefused(t *testing.T) {
	for _, name := range ReservedNames() {
		err := ValidateName(name)
		if err == nil {
			t.Fatalf("ValidateName(%q) = nil, want a refusal", name)
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("ValidateName(%q) does not say the name is reserved: %v", name, err)
		}
		for _, other := range ReservedNames() {
			if !strings.Contains(err.Error(), other) {
				t.Errorf("ValidateName(%q) does not list reserved name %q: %v", name, other, err)
			}
		}
	}
}

func TestRuntimeIsReserved(t *testing.T) {
	if err := ValidateName("runtime"); err == nil {
		t.Fatal("an app could be named runtime, which collides with the shared runtime")
	}
	for _, ok := range []string{"runtime-monitor", "my-runtime", "statusboard"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
}

func TestValidateViewName(t *testing.T) {
	for _, ok := range []string{"approvals", "a", "pending-v2", "9lives", "runtime", "status"} {
		if err := ValidateViewName(ok); err != nil {
			t.Errorf("ValidateViewName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "-leading", "Approvals", "with_underscore", "with space", "app/name", "..", strings.Repeat("a", MaxViewNameLength+1)} {
		if err := ValidateViewName(bad); err == nil {
			t.Errorf("ValidateViewName(%q) = nil, want an error", bad)
		}
	}
	err := ValidateViewName(strings.Repeat("a", MaxViewNameLength+1))
	if err == nil || !strings.Contains(err.Error(), "65") || !strings.Contains(err.Error(), "64") {
		t.Fatalf("error does not name the ask and the limit: %v", err)
	}
}

func TestDerivedIdentities(t *testing.T) {
	if got := ConsumerName("approval-gate"); got != "app:approval-gate" {
		t.Errorf("ConsumerName = %q", got)
	}
	if got := Namespace("approval-gate"); got != "app/approval-gate" {
		t.Errorf("Namespace = %q", got)
	}
}

// Nothing links the build's shell script to this constant, so a rename there produces a
// daemon that cannot find its runtime.
func TestRuntimeHostBinaryNameMatchesTheBuild(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's own file")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "scripts", "build-app-runtime-host.sh")
	contents, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading %s: %v", script, err)
	}
	want := `binary_name="` + RuntimeHostBinaryName + `"`
	if !strings.Contains(string(contents), want) {
		t.Fatalf("%s does not build %q (looked for %s)", script, RuntimeHostBinaryName, want)
	}
}

func TestRuntimeHostBinaryNameForProfile(t *testing.T) {
	if got := RuntimeHostBinaryNameForProfile(""); got != "attn-app-runtime" {
		t.Errorf("default profile = %q", got)
	}
	if got := RuntimeHostBinaryNameForProfile("dev"); got != "attn-app-runtime-dev" {
		t.Errorf("named profile = %q", got)
	}
}
