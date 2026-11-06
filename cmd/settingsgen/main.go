package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/setting"
)

func main() {
	var out strings.Builder
	out.WriteString("// Generated from internal/setting by make generate-types. Do not edit.\n\n")
	out.WriteString("export const BoolSetting = {\n")
	for _, s := range setting.All() {
		if s.Kind == setting.Bool && !s.IsFamily() {
			fmt.Fprintf(&out, "  %s: %q,\n", identifier(s.Key), s.Key)
		}
	}
	out.WriteString("} as const;\n\nexport type BoolSettingKey = (typeof BoolSetting)[keyof typeof BoolSetting];\n\n")
	out.WriteString("export const DEFAULT_ON_SETTINGS: ReadonlySet<string> = new Set([\n")
	for _, s := range setting.All() {
		if s.Kind == setting.Bool && !s.IsFamily() && s.Default == setting.On {
			fmt.Fprintf(&out, "  %q,\n", s.Key)
		}
	}
	out.WriteString("]);\n")
	if err := os.WriteFile(os.Args[1], []byte(out.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func identifier(key string) string {
	parts := strings.FieldsFunc(key, func(r rune) bool { return r == '_' || r == '.' })
	for i, part := range parts {
		if i > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}
