package daemon

import "encoding/json"

func applyJSONOp(root any, kind string, path []any, args []json.RawMessage) any {
	if len(path) == 0 {
		return applyJSONOpHere(root, kind, args)
	}
	switch container := root.(type) {
	case map[string]any:
		key, ok := path[0].(string)
		if !ok {
			return root
		}
		if kind == "d" && len(path) == 1 {
			delete(container, key)
			return container
		}
		container[key] = applyJSONOp(container[key], kind, path[1:], args)
		return container
	case []any:
		index, ok := path[0].(float64)
		at := int(index)
		if !ok || at < 0 || at >= len(container) {
			return root
		}
		container[at] = applyJSONOp(container[at], kind, path[1:], args)
		return container
	case nil:
		if kind == "d" {
			return nil
		}
		key, ok := path[0].(string)
		if !ok {
			return nil
		}
		return map[string]any{key: applyJSONOp(nil, kind, path[1:], args)}
	}
	return root
}

func applyJSONOpHere(value any, kind string, args []json.RawMessage) any {
	switch kind {
	case "s", "r":
		if len(args) == 1 {
			return decodeJSON(args[0])
		}
	case "d":
		return nil
	case "a":
		var text string
		if len(args) == 1 && json.Unmarshal(args[0], &text) == nil {
			current, _ := value.(string)
			return current + text
		}
	case "t":
		var length int
		current, isText := value.(string)
		if len(args) == 1 && isText && json.Unmarshal(args[0], &length) == nil && length >= 0 && length <= len(current) {
			return current[:length]
		}
	case "p":
		var at, deleteCount int
		var items []any
		array, _ := value.([]any)
		if len(args) == 3 && json.Unmarshal(args[0], &at) == nil && json.Unmarshal(args[1], &deleteCount) == nil && json.Unmarshal(args[2], &items) == nil &&
			at >= 0 && deleteCount >= 0 && at+deleteCount <= len(array) {
			spliced := append(append(append([]any{}, array[:at]...), items...), array[at+deleteCount:]...)
			return spliced
		}
	case "m":
		var permutation []int
		array, _ := value.([]any)
		if len(args) == 1 && json.Unmarshal(args[0], &permutation) == nil && len(permutation) == len(array) {
			moved := make([]any, len(array))
			for to, from := range permutation {
				if from < 0 || from >= len(array) {
					return value
				}
				moved[to] = array[from]
			}
			return moved
		}
	}
	return value
}
