package main

import (
	"bytes"
	"io"
	"strings"

	"github.com/victorarias/attn/internal/testhistory"
)

type goTestOutput int

const (
	goQuiet goTestOutput = iota
	goVerbose
	goJSON
)

func goTestMode(command []string) (mode goTestOutput, showsPassingPackages bool) {
	for _, arg := range command[3:] {
		name, _, _ := strings.Cut(strings.TrimPrefix(arg, "-"), "=")
		switch name {
		case "-json", "json":
			return goJSON, true
		case "-v", "v", "-list", "list":
			mode = goVerbose
		case "-bench", "bench":
			showsPassingPackages = true
		}
	}
	return mode, showsPassingPackages || mode == goVerbose
}

type goTestRun struct {
	header   string
	entries  []goRunEntry
	failed   bool
	ended    bool
	reported bool
}

type goRunEntry struct {
	logs  string
	child *goTestRun
}

type goPackageOutput struct {
	text     bytes.Buffer
	summary  bytes.Buffer
	runs     map[string]*goTestRun
	tops     []*goTestRun
	lastTest string
	ended    bool
	passed   bool
}

type goRenderer struct {
	out, errs            io.Writer
	mode                 goTestOutput
	showsPassingPackages bool
	order                []string
	packages             map[string]*goPackageOutput
	flushed              int
	streamed             int
}

func newGoRenderer(out, errs io.Writer, command []string) *goRenderer {
	mode, showsPassingPackages := goTestMode(command)
	return &goRenderer{out: out, errs: errs, mode: mode, showsPassingPackages: showsPassingPackages, packages: map[string]*goPackageOutput{}}
}

func (r *goRenderer) line(line []byte) {
	if r.mode == goJSON {
		_, _ = r.out.Write(line)
		return
	}
	event, ok := testhistory.ParseGoEvent(line)
	if !ok {
		_, _ = r.out.Write(line)
		return
	}
	if event.Package == "" {
		if event.Action == "build-output" {
			_, _ = io.WriteString(r.errs, event.Output)
		}
		return
	}
	pkg := r.packages[event.Package]
	if pkg == nil {
		pkg = &goPackageOutput{runs: map[string]*goTestRun{}}
		r.packages[event.Package] = pkg
		r.order = append(r.order, event.Package)
	}
	terminal := event.Action == "pass" || event.Action == "fail" || event.Action == "skip"
	switch {
	case event.Test == "" && event.Action == "output":
		pkg.text.WriteString(event.Output)
		pkg.summary.WriteString(event.Output)
	case event.Test == "" && terminal:
		pkg.ended, pkg.passed = true, event.Action != "fail"
		r.endPackage(pkg)
	case r.mode == goVerbose && event.Action == "output":
		if event.OutputType != "frame" && event.Test != pkg.lastTest && pkg.lastTest != "" {
			pkg.text.WriteString("=== NAME  " + event.Test + "\n")
		}
		pkg.lastTest = event.Test
		pkg.text.WriteString(event.Output)
	case r.mode == goVerbose:
	case event.Action == "run":
		run := &goTestRun{}
		if parent := pkg.runs[parentTest(event.Test)]; parent != nil && strings.Contains(event.Test, "/") {
			parent.entries = append(parent.entries, goRunEntry{child: run})
		} else {
			pkg.tops = append(pkg.tops, run)
		}
		pkg.runs[event.Test] = run
	case event.Action == "output":
		run := pkg.runs[event.Test]
		switch {
		case run == nil || run.reported:
			pkg.text.WriteString(event.Output)
		case event.OutputType == "frame" && strings.HasPrefix(event.Output, "--- "):
			run.header = strings.TrimLeft(event.Output, " ")
			run.ended, run.failed = true, strings.HasPrefix(run.header, "--- FAIL")
			if !strings.Contains(event.Test, "/") {
				r.report(pkg, run)
			}
		case event.OutputType == "frame":
		case strings.HasPrefix(event.Test, "Benchmark") && event.Output == event.Test+"\n":
		case strings.HasPrefix(event.Output, "    "):
			run.log(event.Output)
		default:
			pkg.text.WriteString(event.Output)
		}
	case terminal:
		if run := pkg.runs[event.Test]; run != nil && !run.reported {
			run.ended, run.failed = true, event.Action == "fail"
			if !strings.Contains(event.Test, "/") {
				r.report(pkg, run)
			}
		}
	}
	r.stream()
}

func parentTest(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i]
	}
	return ""
}

func (run *goTestRun) log(output string) {
	if last := len(run.entries) - 1; last >= 0 && run.entries[last].child == nil {
		run.entries[last].logs += output
		return
	}
	run.entries = append(run.entries, goRunEntry{logs: output})
}

func (r *goRenderer) report(pkg *goPackageOutput, run *goTestRun) {
	run.reported = true
	if run.failed || !run.ended {
		writeTestRun(&pkg.text, run, "")
	}
	for i, top := range pkg.tops {
		if top == run {
			pkg.tops = append(pkg.tops[:i], pkg.tops[i+1:]...)
			break
		}
	}
}

func writeTestRun(out *bytes.Buffer, run *goTestRun, indent string) {
	if !run.ended {
		indent = ""
	} else {
		out.WriteString(indent + run.header)
	}
	for _, entry := range run.entries {
		if entry.child != nil {
			if entry.child.failed || !entry.child.ended {
				writeTestRun(out, entry.child, indent+"    ")
			}
			continue
		}
		for _, line := range strings.SplitAfter(entry.logs, "\n") {
			if line != "" {
				out.WriteString(indent + line)
			}
		}
	}
}

func (r *goRenderer) endPackage(pkg *goPackageOutput) {
	for len(pkg.tops) > 0 {
		r.report(pkg, pkg.tops[0])
	}
	if pkg.passed && !r.showsPassingPackages {
		pkg.text.Reset()
		for _, line := range strings.SplitAfter(pkg.summary.String(), "\n") {
			if strings.HasPrefix(line, "ok  ") || strings.HasPrefix(line, "?   ") {
				pkg.text.WriteString(line)
			}
		}
	}
	pkg.summary.Reset()
}

func (r *goRenderer) stream() {
	for r.flushed < len(r.order) {
		pkg := r.packages[r.order[r.flushed]]
		if r.mode == goQuiet && !pkg.ended {
			return
		}
		text := pkg.text.Bytes()
		_, _ = r.out.Write(text[r.streamed:])
		r.streamed = len(text)
		if !pkg.ended {
			return
		}
		r.flushed++
		r.streamed = 0
	}
}

func (r *goRenderer) finish() {
	for _, name := range r.order[r.flushed:] {
		if pkg := r.packages[name]; !pkg.ended {
			pkg.ended = true
			r.endPackage(pkg)
		}
	}
	r.stream()
	if r.mode == goJSON {
		return
	}
	for _, pkg := range r.packages {
		if !pkg.passed {
			_, _ = io.WriteString(r.out, "FAIL\n")
			return
		}
	}
}
