package tendril

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConformanceWithGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	env, err := NewEnv(Std()...)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("testdata/conformance/*.go")
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			want := runWithGo(t, string(src))
			prog, err := env.Compile(filepath.Base(file), string(src))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, stats, err := prog.Call(context.Background(), DefaultLimits, "Run")
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			got := out[0].(string)
			if got != want {
				diffLines(t, want, got)
			}
			t.Logf("fuel=%d memory=%d", stats.Fuel, stats.Memory)
		})
	}
}

func runWithGo(t *testing.T, src string) string {
	dir := t.TempDir()
	main := src + "\nfunc main() { fmt.Print(Run()) }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "main.go")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	return string(out)
}

func diffLines(t *testing.T, want, got string) {
	t.Helper()
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			t.Errorf("line %d:\n  go:      %s\n  tendril: %s", i+1, wl, gl)
		}
	}
}
