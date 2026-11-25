package catalog

import (
	"maps"
	"slices"
	"strings"
)

type Receipt map[string]Receipt

func ReceiptOf(paths []string) Receipt {
	root := Receipt{}
	for _, path := range paths {
		node := root
		for name := range strings.SplitSeq(path, ".") {
			if node[name] == nil {
				node[name] = Receipt{}
			}
			node = node[name]
		}
	}
	return root
}

func (r Receipt) Names() []string { return slices.Sorted(maps.Keys(r)) }

func (r Receipt) Whole() bool { return len(r) == 0 }

func (r Receipt) Of(result any) any {
	if r.Whole() {
		return result
	}
	switch v := result.(type) {
	case map[string]any:
		kept := map[string]any{}
		for name, inner := range r {
			if value, ok := v[name]; ok {
				kept[name] = inner.Of(value)
			}
		}
		return kept
	case []any:
		kept := make([]any, len(v))
		for i, item := range v {
			kept[i] = r.Of(item)
		}
		return kept
	}
	return result
}
