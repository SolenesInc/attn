package pathutil

import "strings"

func mergePaths(primary, secondary string) string {
	seen := make(map[string]bool)
	var merged []string

	for _, pathList := range []string{primary, secondary} {
		for _, part := range strings.Split(pathList, ":") {
			if part != "" && !seen[part] {
				seen[part] = true
				merged = append(merged, part)
			}
		}
	}
	return strings.Join(merged, ":")
}
