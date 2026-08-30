package headless

import (
	"errors"
	"testing"
)

func TestParseSwitch(t *testing.T) {
	cases := map[string]struct {
		value bool
		ok    bool
	}{
		"on":       {true, true},
		"ON":       {true, true},
		" true ":   {true, true},
		"1":        {true, true},
		"yes":      {true, true},
		"enabled":  {true, true},
		"off":      {false, true},
		"0":        {false, true},
		"false":    {false, true},
		"no":       {false, true},
		"disabled": {false, true},
		"":         {false, false},
		"maybe":    {false, false},
	}
	for raw, want := range cases {
		value, ok := ParseSwitch(raw)
		if value != want.value || ok != want.ok {
			t.Errorf("ParseSwitch(%q) = (%v, %v), want (%v, %v)", raw, value, ok, want.value, want.ok)
		}
	}
}

func TestResolutionOrder(t *testing.T) {
	t.Cleanup(func() { SetStoredEnabled(true) })

	if !Enabled() {
		t.Fatal("headless tasks must default to on")
	}

	SetStoredEnabled(false)
	if Enabled() {
		t.Fatal("the stored setting must decide when no env override is set")
	}
	if got := Describe(); got != "off ("+SettingKey+")" {
		t.Fatalf("Describe() = %q", got)
	}

	t.Setenv(EnvVar, "on")
	if !Enabled() || Describe() != "on" {
		t.Fatalf("the env override must win over the stored setting (Describe=%q)", Describe())
	}

	t.Setenv(EnvVar, "off")
	SetStoredEnabled(true)
	if Enabled() {
		t.Fatal("the env override must win over an enabled setting")
	}
	if got := Describe(); got != "off ("+EnvVar+")" {
		t.Fatalf("Describe() = %q", got)
	}
	if Mode() != "off" {
		t.Fatalf("Mode() = %q", Mode())
	}
}

func TestRefusalNamesTheCallerAndWrapsErrRefused(t *testing.T) {
	err := Refusal("summarize_session")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("Refusal() = %v, want an ErrRefused wrapper", err)
	}
	if got, want := err.Error(), "headless task refused (summarize_session): headless tasks are off"; got != want {
		t.Fatalf("Refusal() message = %q, want %q", got, want)
	}
}

func TestOverrideReportsTheRawEnvironmentValue(t *testing.T) {
	if raw, ok := Override(); ok {
		t.Fatalf("Override() = (%q, true), want no override with the env var unset", raw)
	}

	t.Setenv(EnvVar, " Off ")
	raw, ok := Override()
	if !ok || raw != "Off" {
		t.Fatalf("Override() = (%q, %v), want (\"Off\", true)", raw, ok)
	}

	t.Setenv(EnvVar, "maybe")
	if raw, ok := Override(); ok {
		t.Fatalf("Override() = (%q, true), want an unparsable value to report no override", raw)
	}
}
