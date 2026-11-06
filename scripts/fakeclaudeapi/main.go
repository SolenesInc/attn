package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/victorarias/attn/internal/fakeclaudeapi"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: fakeclaudeapi serve [-addr 127.0.0.1:0] | check-idle")
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "check-idle":
		checkIdle()
	case hookCommand:
		recordHook(os.Args[2:])
	default:
		fail("unknown command " + os.Args[1])
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}

type control struct {
	ContextTokens *int    `json:"context_tokens"`
	Compact       *string `json:"compact"`
}

func serve(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := flags.String("addr", "127.0.0.1:0", "address to listen on")
	_ = flags.Parse(args)
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fail(err.Error())
	}
	model := fakeclaudeapi.New()
	mux := http.NewServeMux()
	mux.Handle("/", model)
	mux.Handle("/control", controlHandler(model))
	url := "http://" + listener.Addr().String()
	fmt.Printf("ANTHROPIC_BASE_URL=%s\nANTHROPIC_API_KEY=%s\nENABLE_PROMPT_CACHING_1H=1\n", url, fakeclaudeapi.APIKey)
	fmt.Printf("control: curl %s/control -d '{\"context_tokens\":245000,\"compact\":\"ok|hold|fail\"}'\n", url)
	fail(http.Serve(listener, mux).Error())
}

func controlHandler(model *fakeclaudeapi.Model) http.Handler {
	var mu sync.Mutex
	release := func() {}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var change control
			if err := json.NewDecoder(r.Body).Decode(&change); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if change.Compact != nil && *change.Compact != "ok" && *change.Compact != "hold" && *change.Compact != "fail" {
				http.Error(w, fmt.Sprintf("compact is %q; it must be ok, hold or fail", *change.Compact), http.StatusBadRequest)
				return
			}
			if change.ContextTokens != nil {
				model.SetContextTokens(*change.ContextTokens)
			}
			if change.Compact != nil {
				mu.Lock()
				release()
				release = func() {}
				if *change.Compact == "hold" {
					release = model.HoldCompacts()
				}
				mu.Unlock()
				model.BreakCompacts(*change.Compact == "fail")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"compacts": model.CompactCount(), "prompts": model.Prompts()})
	})
}
