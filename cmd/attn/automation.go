package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func automationUsage() {
	fmt.Fprint(os.Stderr, "usage: attn automation <apply|set|validate|list|show|run|runs|enable|disable|delete|cleanup> [--profile <name|id>]\n")
}

func runAutomationCommand() {
	args := append([]string(nil), os.Args...)
	profile := ""
	for i := 3; i < len(args); i++ {
		if args[i] == "--profile" {
			if i+1 == len(args) {
				fmt.Fprintln(os.Stderr, "automation: --profile needs a name or id")
				os.Exit(2)
			}
			profile = strings.TrimSpace(args[i+1])
			args = append(args[:i], args[i+2:]...)
			i--
		} else if value, ok := strings.CutPrefix(args[i], "--profile="); ok {
			profile = strings.TrimSpace(value)
			args = append(args[:i], args[i+1:]...)
			i--
		}
	}
	if len(args) < 3 {
		automationUsage()
		os.Exit(2)
	}
	c := client.New(client.DefaultSocketPath()).WithGardenProfile(profile, currentSessionOrExit())
	var err error
	var definitionID int
	switch args[2] {
	case "set", "show", "run", "runs", "enable", "disable", "delete", "cleanup":
		if len(args) < 4 {
			automationUsage()
			os.Exit(2)
		}
		definitionID, err = strconv.Atoi(args[3])
		if err != nil || definitionID <= 0 {
			fmt.Fprintln(os.Stderr, "automation: ID must be a positive number")
			os.Exit(2)
		}
	}
	switch args[2] {
	case "set":
		if len(args) < 4 {
			err = fmt.Errorf("usage: attn automation set <definition-id> --launch-desktop <own|desktop>")
			break
		}
		fs := flag.NewFlagSet("automation set", flag.ContinueOnError)
		desktopName := fs.String("desktop-name", "", "name for the new desktop of own or an empty slot (defaults to the automation name)")
		desktop := fs.String("launch-desktop", "", "own, an empty slot (5–9), or a desktop digit, name or id")
		if e := fs.Parse(args[4:]); e != nil {
			os.Exit(2)
		}
		if *desktop == "" || len(fs.Args()) != 0 {
			err = fmt.Errorf("--launch-desktop is required")
			break
		}
		var result *protocol.LaunchDesktopResultMessage
		var name *string
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "desktop-name" {
				name = desktopName
			}
		})
		result, err = c.SetAutomationLaunchDesktop(definitionID, *desktop, name)
		if err == nil {
			printJSON(result.Item)
		}

	case "apply":
		fs := flag.NewFlagSet("automation apply", flag.ContinueOnError)
		desktopName := fs.String("desktop-name", "", "name for the new desktop of own or an empty slot (defaults to the automation name)")
		desktop := fs.String("launch-desktop", "", "own, an empty slot (5–9), or a desktop digit, name or id of the automation profile")
		file := fs.String("file", "", "definition YAML")
		if e := fs.Parse(args[3:]); e != nil {
			os.Exit(2)
		}
		if *file == "" {
			err = fmt.Errorf("--file is required")
			break
		}
		raw, e := os.ReadFile(*file)
		if e != nil {
			err = e
			break
		}
		var result *protocol.AutomationApplyResultMessage
		var desktopRef, name *string
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "launch-desktop":
				desktopRef = desktop
			case "desktop-name":
				name = desktopName
			}
		})
		if name != nil && desktopRef == nil {
			err = fmt.Errorf("--desktop-name needs --launch-desktop own or an empty slot")
			break
		}
		result, err = c.AutomationApplyWithNamedDesktop(string(raw), desktopRef, name)
		if err == nil {
			printJSON(result.Definition)
		}
	case "validate":
		fs := flag.NewFlagSet("automation validate", flag.ContinueOnError)
		file := fs.String("file", "", "definition YAML")
		if e := fs.Parse(args[3:]); e != nil {
			os.Exit(2)
		}
		if *file == "" {
			err = fmt.Errorf("--file is required")
			break
		}
		raw, e := os.ReadFile(*file)
		if e != nil {
			err = e
			break
		}
		if err = c.AutomationValidate(string(raw)); err == nil {
			printJSON(map[string]bool{"valid": true})
		}
	case "list":
		var result *protocol.AutomationDefinitionsResultMessage
		result, err = c.AutomationDefinitions()
		if err == nil {
			printJSON(result.Definitions)
		}
	case "show":
		if len(args) != 4 {
			err = fmt.Errorf("usage: attn automation show <definition-id>")
			break
		}
		var result *protocol.AutomationDefinitionResultMessage
		result, err = c.AutomationDefinition(definitionID)
		if err == nil && result.SpecYaml != nil {
			fmt.Printf("# Profile: %s\n# Launch desktop: %s\n", protocol.Deref(result.Definition.ProfileName), launchDesktopText(result.Definition.LaunchDesktop))
			fmt.Print(*result.SpecYaml)
		}
	case "run":
		if len(args) < 4 {
			err = fmt.Errorf("usage: attn automation run <definition-id> [--input-file <file> | --pr-url <url>] [--request-id <id>]")
			break
		}
		fs := flag.NewFlagSet("automation run", flag.ContinueOnError)
		inputFile := fs.String("input-file", "", "structured occurrence JSON")
		prURL := fs.String("pr-url", "", "resolve one GitHub pull request into structured occurrence input")
		requestID := fs.String("request-id", "", "stable idempotency key to reuse when retrying an uncertain request")
		if e := fs.Parse(args[4:]); e != nil {
			os.Exit(2)
		}
		if len(fs.Args()) != 0 {
			err = fmt.Errorf("usage: attn automation run <definition-id> [--input-file <file> | --pr-url <url>] [--request-id <id>]")
			break
		}
		if *inputFile != "" && *prURL != "" {
			err = fmt.Errorf("--input-file and --pr-url are mutually exclusive")
			break
		}
		input := "{}"
		if *inputFile != "" {
			raw, e := os.ReadFile(*inputFile)
			if e != nil {
				err = e
				break
			}
			input = string(raw)
		}
		if *requestID == "" {
			*requestID = uuid.NewString()
		}
		var result *protocol.AutomationRunResultMessage
		if *prURL != "" {
			result, err = c.AutomationRunPullRequest(definitionID, *requestID, *prURL)
		} else {
			result, err = c.AutomationRun(definitionID, *requestID, input)
		}
		if err == nil {
			printJSON(result.Run)
		}
	case "runs":
		if len(args) != 4 {
			err = fmt.Errorf("usage: attn automation runs <definition-id>")
			break
		}
		var result *protocol.AutomationRunsResultMessage
		result, err = c.AutomationRuns(definitionID)
		if err == nil {
			printJSON(result.Runs)
		}
	case "enable":
		if len(args) != 4 {
			err = fmt.Errorf("usage: attn automation enable <definition-id>")
			break
		}
		var result *protocol.AutomationSetEnabledResultMessage
		result, err = c.AutomationSetEnabled(definitionID, true)
		if err == nil {
			printJSON(result.Definition)
		}
	case "disable":
		if len(args) != 4 {
			err = fmt.Errorf("usage: attn automation disable <definition-id>")
			break
		}
		var result *protocol.AutomationSetEnabledResultMessage
		result, err = c.AutomationSetEnabled(definitionID, false)
		if err == nil {
			printJSON(result.Definition)
		}
	case "delete":
		if len(args) != 4 {
			err = fmt.Errorf("usage: attn automation delete <definition-id>")
			break
		}
		if err = c.AutomationDelete(definitionID); err == nil {
			printJSON(map[string]int{"deleted": definitionID})
		}
	case "cleanup":
		if len(args) != 4 {
			err = fmt.Errorf("usage: attn automation cleanup <definition-id>")
			break
		}
		var result *protocol.AutomationCleanupResultMessage
		result, err = c.AutomationCleanup(definitionID)
		if err == nil {
			printJSON(map[string][]string{
				"cleaned":     result.Cleaned,
				"kept_dirty":  result.KeptDirty,
				"kept_active": result.KeptActive,
			})
		}
	default:
		err = fmt.Errorf("unknown automation command %q", args[2])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "automation: %v\n", err)
		os.Exit(1)
	}
}
