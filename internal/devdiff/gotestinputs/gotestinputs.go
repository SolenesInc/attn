package gotestinputs

import "regexp"

const (
	Changed   = "changed"
	Unchanged = "unchanged"
)

var read = regexp.MustCompile(`\.go$` +
	`|^(cmd|internal|test|pty-host|apphost|scripts|changelog\.d)/` +
	`|^(go\.mod|go\.sum|Makefile|\.tool-versions|ghostty-vt\.pin|ghostty-vt-native\.lock)$` +
	`|^app/pnpm-lock\.yaml$` +
	`|^app/src/hooks/useDaemonSocket\.ts$` +
	`|^app/src/ghostty/testdata/` +
	`|^sdk/attn-app/package\.json$`)

func Among(paths []string) []string {
	var inputs []string
	for _, p := range paths {
		if read.MatchString(p) {
			inputs = append(inputs, p)
		}
	}
	return inputs
}
