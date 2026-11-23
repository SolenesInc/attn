package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/appserver"
	"github.com/victorarias/attn/internal/harness/builtin"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

const (
	beginMarker = "<!-- BEGIN GENERATED HARNESS TABLE -->"
	endMarker   = "<!-- END GENERATED HARNESS TABLE -->"
)

type stubHost struct {
	harness.Host
	lastSetting string
}

func (*stubHost) Logf(string, ...any) {}

func (h *stubHost) Setting(s *setting.Setting) string {
	if s != nil {
		h.lastSetting = s.Key
	}
	return ""
}

func (*stubHost) Terminals() harness.HostTerminals { return nil }

func (*stubHost) ExecutableHealth(_ harness.Facts, offered bool) harness.Health {
	return harness.Health{Offered: offered}
}

func (*stubHost) RegisterClientCommand(string, bool, func(string, any) any)                  {}
func (*stubHost) RegisterSocketCommand(string, func(context.Context, any) protocol.Response) {}
func (*stubHost) RegisterCatalogCommand(string, command.Handler)                             {}
func (*stubHost) RegisterProfileSnapshot(harness.ProfileSnapshot) func(...string) {
	return func(...string) {}
}

type capabilityRow struct {
	label  string
	cell   func(harness.Harness) string
	plugin string
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: harnessdoc <path-to-harnesses.md>\n")
		os.Exit(2)
	}
	if err := generate(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "harnessdoc: %v\n", err)
		os.Exit(1)
	}
}

func clearAgentEnv() {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "ATTN_AGENT_") {
			_ = os.Unsetenv(key)
		}
	}
}

func generate(path string) error {
	clearAgentEnv()
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(raw)
	start := strings.Index(content, beginMarker)
	end := strings.Index(content, endMarker)
	if start < 0 || end < 0 || end <= start {
		return fmt.Errorf("%s must contain %s and %s markers", path, beginMarker, endMarker)
	}

	host := &stubHost{}
	servers := appserver.New(host)
	harnesses := builtin.All(func(string) harness.Host { return host }, servers)
	slices.SortFunc(harnesses, func(a, b harness.Harness) int {
		return strings.Compare(a.Facts().ID, b.Facts().ID)
	})

	table := renderTable(host, harnesses)
	updated := content[:start+len(beginMarker)] + "\n" + table + content[end:]
	if bytes.Equal(raw, []byte(updated)) {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func renderTable(host *stubHost, harnesses []harness.Harness) string {
	ctx := context.Background()
	rows := []capabilityRow{
		{
			label: "Offered by default",
			cell: func(h harness.Harness) string {
				host.lastSetting = ""
				if h.Health(ctx).Offered {
					return "Yes"
				}
				if host.lastSetting != "" {
					return fmt.Sprintf("Experimental (`%s`)", host.lastSetting)
				}
				return "No"
			},
			plugin: "While plugin is connected",
		},
		{
			label: "Executable setting",
			cell: func(h harness.Harness) string {
				f := h.Facts()
				if f.Executable.Default == "" {
					return "No"
				}
				return fmt.Sprintf("`%s_executable` (`%s`, default `%s`)", f.ID, f.Executable.Env, f.Executable.Default)
			},
			plugin: "Owned by plugin (optional `<harness>_executable`)",
		},
		{
			label:  "Resume closed session",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Options.Resume) },
			plugin: "Declared (`resume`)",
		},
		{
			label:  "YOLO approval bypass",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Options.Yolo) },
			plugin: "Declared (`yolo`)",
		},
		{
			label: "Auto-approve",
			cell: func(h harness.Harness) string {
				opts := h.Facts().Options
				if !opts.AutoApprove {
					return "No"
				}
				if opts.AutoApproveUnreviewed {
					return "Yes (unreviewed)"
				}
				return "Yes"
			},
			plugin: "No",
		},
		{
			label:  "Automation executable override",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Options.AutomationExecutable) },
			plugin: "No",
		},
		{
			label:  "Initial prompt and delegation",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Options.InitialPrompt) },
			plugin: "Declared (`initial_prompt`)",
		},
		{
			label:  "Launch guidance",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Options.LaunchInstructions) },
			plugin: "Declared (`launch_instructions`)",
		},
		{
			label: "Model pin",
			cell: func(h harness.Harness) string {
				opts := h.Facts().Options
				if !opts.ModelPin {
					return "No"
				}
				if opts.Provider {
					return "Yes (`provider/model`)"
				}
				return "Yes"
			},
			plugin: "Declared (`model_pin`, `provider/model`)",
		},
		{
			label:  "Effort pin",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Options.EffortPin) },
			plugin: "Declared (`effort_pin`)",
		},
		{
			label:  "Model discovery",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().ModelDiscovery) },
			plugin: "Declared (`model_discovery` or `server_binding`)",
		},
		{
			label:  "Context window cap",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Options.ContextCap) },
			plugin: "No",
		},
		{
			label: "One-shot calls (background tasks and `attn model`)",
			cell: func(h harness.Harness) string {
				_, ok := harness.As[harness.OneShot](h)
				return yesNo(ok)
			},
			plugin: "No",
		},
		{
			label:  "Session activity lines",
			cell:   func(h harness.Harness) string { return yesNo(h.Facts().Activity) },
			plugin: "With `server_binding` and `watch` ability",
		},
		{
			label: "Lost-terminal state recovery",
			cell: func(h harness.Harness) string {
				if h.Facts().Recovery == harness.ByServer {
					return "From server"
				}
				return "From terminal claims"
			},
			plugin: "From terminal claims, or from server (`server_binding`)",
		},
		{
			label: "Folder trust pre-check",
			cell: func(h harness.Harness) string {
				_, ok := harness.As[harness.Trust](h)
				return yesNo(ok)
			},
			plugin: "No",
		},
		{
			label:  "Default crew prompt cache TTL",
			cell:   func(h harness.Harness) string { return formatDuration(h.Facts().CacheTTL) },
			plugin: "1h",
		},
	}

	var b strings.Builder
	b.WriteString("| Capability |")
	for _, h := range harnesses {
		fmt.Fprintf(&b, " %s |", h.Facts().DisplayName)
	}
	b.WriteString(" Plugin harness |\n")

	b.WriteString("|---|")
	for range harnesses {
		b.WriteString("---|")
	}
	b.WriteString("---|\n")

	for _, row := range rows {
		fmt.Fprintf(&b, "| %s |", row.label)
		for _, h := range harnesses {
			fmt.Fprintf(&b, " %s |", row.cell(h))
		}
		fmt.Fprintf(&b, " %s |\n", row.plugin)
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "None"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}
