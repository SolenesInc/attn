package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/codemode"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/tendril"
)

func runCode(args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "api":
			runCodeAPI(args[1:])
			return
		case "check":
			name, src := readCodeSource(args[1:])
			if _, err := codemode.Check(name, src); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			return
		case "stub":
			stub, err := codemode.Stub()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Print(stub)
			return
		case "-h", "--help", "help":
			writeCodeHelp(os.Stdout)
			return
		}
	}
	name, src := readCodeSource(args)
	if _, err := codemode.Check(name, src); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	session, err := currentSession(client.New(""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn code: resolve current session: %v\n", err)
		os.Exit(1)
	}
	caller := codemode.Caller{Client: client.New(config.SocketPath()).WithGardenProfile("", session), Session: session}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	_, err = codemode.Run(ctx, caller, name, src, os.Stdout, os.Stderr)
	var exit *tendril.ExitError
	switch {
	case errors.As(err, &exit):
		os.Exit(exit.Code)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCodeAPI(args []string) {
	if len(args) == 0 {
		fmt.Print(codemode.Schema())
		return
	}
	for _, name := range args {
		doc, err := codemode.Doc(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(doc)
	}
}

func readCodeSource(args []string) (string, string) {
	if len(args) == 0 || args[0] == "-" {
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "attn code: read program: %v\n", err)
			os.Exit(1)
		}
		return "main.go", string(src)
	}
	src, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn code: %v\n", err)
		os.Exit(1)
	}
	return args[0], string(src)
}

func writeCodeHelp(w io.Writer) {
	fmt.Fprint(w, `attn code runs a Go program that calls attn's tools directly.

Usage:
  attn code [file.go]     run a program; reads stdin when no file is given
  attn code check [file]  type-check a program without running it
  attn code api [name]    print the attn package, or one of its declarations
  attn code stub          print the attn package as compilable Go, for editors

A program is a main package that imports "attn". It acts as the session
whose terminal runs it. Compile errors exit 2, runtime errors exit 1, and
os.Exit and log.Fatal set the exit status.

Example:
  attn code <<'EOF'
  package main

  import (
  	"attn"
  	"fmt"
  	"log"
  )

  func main() {
  	seeds, err := attn.Seeds()
  	if err != nil {
  		log.Fatal(err)
  	}
  	for _, s := range seeds {
  		fmt.Println(s.ID, s.State, s.Title)
  	}
  }
  EOF
`)
}
