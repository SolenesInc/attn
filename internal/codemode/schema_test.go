package codemode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaBindsEveryDeclaration(t *testing.T) {
	if _, err := env(); err != nil {
		t.Fatal(err)
	}
}

func TestDocShowsOneDeclaration(t *testing.T) {
	doc, err := Doc("Plant")
	if err != nil {
		t.Fatal(err)
	}
	want := "// Plant creates a seed in the garden and returns it.\nfunc Plant(title string, opts PlantOptions) (Seed, error)"
	if doc != want {
		t.Fatalf("got %q", doc)
	}
}

const sampleProgram = `package main

import (
	"attn"
	"fmt"
	"log"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

func main() {
	var g errgroup.Group
	titles := []string{"Fix login", "Add metrics"}
	planted := make([]attn.Seed, len(titles))
	for i, title := range titles {
		g.Go(func() error {
			s, err := attn.Plant(title, attn.PlantOptions{Body: "from code mode"})
			planted[i] = s
			return err
		})
	}
	if err := g.Wait(); err != nil {
		log.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	var ids []string
	for _, s := range planted {
		ids = append(ids, s.ID)
	}
	fmt.Println(strings.Join(ids, ","), attn.Me())
}
`

func TestProgramsCompileWithTheRealGoToolchain(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	if _, err := Check("main.go", sampleProgram); err != nil {
		t.Fatalf("code mode rejects the program: %v", err)
	}
	stub, err := Stub()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("testdata", "_vet")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.MkdirAll(filepath.Join(dir, "attn"), 0o755); err != nil {
		t.Fatal(err)
	}
	module := "module codemodecheck\n\ngo 1.24\n\nrequire golang.org/x/sync v0.22.0\n\nreplace attn => ./attn\n"
	for name, content := range map[string]string{
		"go.mod":       module,
		"attn/go.mod":  "module attn\n\ngo 1.24\n",
		"attn/attn.go": stub,
		"main.go":      sampleProgram,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gosum, _ := os.ReadFile("../../go.sum")
	var sum []string
	for _, line := range strings.Split(string(gosum), "\n") {
		if strings.HasPrefix(line, "golang.org/x/sync ") {
			sum = append(sum, line)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(strings.Join(sum, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "mod", "edit", "-require=attn@v0.0.0")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	vet := func() ([]byte, error) {
		cmd := exec.Command("go", "vet", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
		return cmd.CombinedOutput()
	}
	if out, err := vet(); err != nil {
		t.Fatalf("go vet rejects a program code mode accepts: %v\n%s", err, out)
	}
	broken := strings.Replace(sampleProgram, "attn.PlantOptions{Body:", "attn.PlantOptions{Bodyy:", 1)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := vet()
	_, ourErr := Check("main.go", broken)
	if err == nil || ourErr == nil || !strings.Contains(string(out), "unknown field Bodyy") || !strings.Contains(ourErr.Error(), "unknown field Bodyy") {
		t.Fatalf("both checkers must reject the same mistake:\ngo vet: %v %s\ncode mode: %v", err, out, ourErr)
	}
}
