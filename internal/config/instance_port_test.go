package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func TestInstancePortClaim(t *testing.T) {
	if derivedInstancePort("demo") != derivedInstancePort("onboarding") {
		t.Fatalf("demo and onboarding no longer share a derived port; pick two names that do")
	}
	shared := derivedInstancePort("demo")

	t.Run("an instance that already uses the derived port keeps it", func(t *testing.T) {
		ports := instancePorts(filepath.Join(t.TempDir(), ".attn"))
		mkdir(t, ports.dir("onboarding"))
		got, err := ports.claim("onboarding")
		if err != nil || got != shared {
			t.Fatalf("claim(onboarding) = %q, %v; want %s", got, err, shared)
		}
	})

	t.Run("a second name with the same derived port moves to the next free one", func(t *testing.T) {
		ports := instancePorts(filepath.Join(t.TempDir(), ".attn"))
		mkdir(t, ports.dir("onboarding"))
		got, err := ports.claim("demo")
		if err != nil {
			t.Fatal(err)
		}
		if got == shared {
			t.Fatalf("claim(demo) = %s, the port onboarding uses", got)
		}
		if ports.port("demo") != got || ports.port("onboarding") != shared {
			t.Fatalf("after claim: demo=%s onboarding=%s, want demo=%s onboarding=%s", ports.port("demo"), ports.port("onboarding"), got, shared)
		}
		if again, _ := ports.claim("demo"); again != got {
			t.Fatalf("second claim(demo) = %s, want the stored %s", again, got)
		}
		if holders := ports.holders(shared, "demo"); len(holders) != 1 || holders[0] != "onboarding" {
			t.Fatalf("holders(%s) = %v, want [onboarding]", shared, holders)
		}
	})

	t.Run("a held top port wraps to the bottom of the range", func(t *testing.T) {
		ports := instancePorts(filepath.Join(t.TempDir(), ".attn"))
		if derivedInstancePort("w12536") != "29848" {
			t.Fatalf("w12536 no longer derives 29848; pick a name that does")
		}
		if err := ports.store("other", "29848"); err != nil {
			t.Fatal(err)
		}
		if got, err := ports.claim("w12536"); err != nil || got != "20000" {
			t.Fatalf("claim(w12536) = %q, %v; want 20000", got, err)
		}
	})

	t.Run("default and dev keep fixed ports and store nothing", func(t *testing.T) {
		root := t.TempDir()
		ports := instancePorts(filepath.Join(root, ".attn"))
		for instance, want := range map[string]string{"": "9849", "default": "9849", "dev": "29849"} {
			if got, err := ports.claim(instance); err != nil || got != want {
				t.Errorf("claim(%q) = %q, %v; want %s", instance, got, err, want)
			}
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Fatalf("claiming fixed ports wrote %v", entries)
		}
	})
}

func TestInstancePortClaim_NamesNeverShareAPort(t *testing.T) {
	name := rapid.StringMatching(`[a-z0-9][a-z0-9-]{0,15}`).Filter(func(n string) bool { return n != "dev" && n != "default" })
	rapid.Check(t, func(rt *rapid.T) {
		ports := instancePorts(filepath.Join(t.TempDir(), ".attn"))
		names := rapid.SliceOfNDistinct(name, 1, 60, rapid.ID[string]).Draw(rt, "names")
		existing := rapid.IntRange(0, len(names)).Draw(rt, "instances that predate claims")
		inUse := map[string]string{}
		for _, legacy := range names[:existing] {
			mkdir(t, ports.dir(legacy))
			inUse[ports.port(legacy)] = legacy
		}
		for _, claimed := range names[existing:] {
			port, err := ports.claim(claimed)
			if err != nil {
				rt.Fatal(err)
			}
			if owner, taken := inUse[port]; taken {
				rt.Fatalf("claim(%s) = %s, which %s already uses", claimed, port, owner)
			}
			inUse[port] = claimed
		}
		for _, claimed := range names[existing:] {
			if again, _ := ports.claim(claimed); inUse[again] != claimed {
				rt.Fatalf("second claim(%s) = %s, which belongs to %s", claimed, again, inUse[again])
			}
		}
		for _, instance := range names {
			if owner := inUse[ports.port(instance)]; owner != instance && !slices.Contains(names[:existing], instance) {
				rt.Fatalf("%s reads port %s, which belongs to %s", instance, ports.port(instance), owner)
			}
		}
	})
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}
