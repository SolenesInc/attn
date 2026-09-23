package apps

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const RuntimeHostBinaryName = "attn-app-runtime"

func RuntimeHostBinaryNameForInstance(instance string) string {
	p := strings.TrimSpace(instance)
	if p == "" {
		return RuntimeHostBinaryName
	}
	return RuntimeHostBinaryName + "-" + p
}

const MaxNameLength = 64

var reserved = map[string]string{
	"runtime":  "the shared app runtime is called `runtime`",
	"new":      "it is an `attn app` subcommand",
	"apply":    "it is an `attn app` subcommand",
	"rollback": "it is an `attn app` subcommand",
	"enable":   "it is an `attn app` subcommand",
	"disable":  "it is an `attn app` subcommand",
	"remove":   "it is an `attn app` subcommand",
	"list":     "it is an `attn app` subcommand",
	"status":   "it is an `attn app` subcommand",
	"logs":     "it is an `attn app` subcommand",
	"dev":      "it is an `attn app` subcommand",
}

func ReservedNames() []string {
	out := make([]string, 0, len(reserved))
	for name := range reserved {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("an app name is required, as lowercase letters, digits and dashes (for example approval-gate)")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("app name %q is %d characters, over the %d-character limit", name, len(name), MaxNameLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("app name %q must be lowercase letters, digits and dashes, starting with a letter or digit (for example approval-gate)", name)
	}
	if why, taken := reserved[name]; taken {
		return fmt.Errorf("app name %q is reserved because %s; an app named %q would make every log line, fact and notification about it ambiguous. Reserved names: %s",
			name, why, name, strings.Join(ReservedNames(), ", "))
	}
	return nil
}

const MaxViewNameLength = 64

func ValidateViewName(name string) error {
	if name == "" {
		return fmt.Errorf("a view name is required, as lowercase letters, digits and dashes (for example approvals)")
	}
	if len(name) > MaxViewNameLength {
		return fmt.Errorf("view name %q is %d characters, over the %d-character limit", name, len(name), MaxViewNameLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("view name %q must be lowercase letters, digits and dashes, starting with a letter or digit (for example pending-approvals)", name)
	}
	return nil
}

const MaxCommandNameLength = 64

func ValidateCommandName(name string) error {
	if name == "" {
		return fmt.Errorf("a command name is required, as lowercase letters, digits and dashes (for example approve)")
	}
	if len(name) > MaxCommandNameLength {
		return fmt.Errorf("command name %q is %d characters, over the %d-character limit", name, len(name), MaxCommandNameLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("command name %q must be lowercase letters, digits and dashes, starting with a letter or digit (for example approve-request)", name)
	}
	return nil
}

func CommandLabel(command string) string      { return "command:" + command }
func SubscriptionLabel(pattern string) string { return "subscribe:" + pattern }
func ViewLabel(view string) string            { return "view:" + view }

const ViewTileKindPrefix = ConsumerPrefix

func ViewTileKind(app, view string) string { return ViewTileKindPrefix + app + "/" + view }

const NoSubscriptionsPattern = "app.subscribes.to.nothing"

func ConsumerName(name string) string { return ConsumerPrefix + name }

const ConsumerPrefix = "app:"

func Namespace(name string) string { return NamespacePrefix + name }

const NamespacePrefix = "app/"
