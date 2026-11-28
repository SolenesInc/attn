package viewlang

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

type out = map[string]any

type token struct {
	key   string
	value string
}

func (t token) keyed() bool { return t.key != "" }

func tokenize(text string) ([]token, error) {
	var tokens []token
	rest := strings.TrimSpace(text)
	for rest != "" {
		end := strings.IndexAny(rest, " \t\"")
		if end < 0 {
			tokens = append(tokens, splitToken(rest))
			break
		}
		if rest[end] != '"' {
			tokens = append(tokens, splitToken(rest[:end]))
			rest = strings.TrimSpace(rest[end:])
			continue
		}
		closing := strings.IndexByte(rest[end+1:], '"')
		if closing < 0 {
			return nil, fmt.Errorf("the quote in %q is not closed", rest)
		}
		word := rest[:end] + rest[end+1:end+1+closing]
		after := rest[end+2+closing:]
		if after != "" && after[0] != ' ' && after[0] != '\t' {
			return nil, fmt.Errorf("%q needs a space after its closing quote", rest[:end+2+closing])
		}
		tokens = append(tokens, splitToken(word))
		rest = strings.TrimSpace(after)
	}
	return tokens, nil
}

func splitToken(word string) token {
	if key, value, ok := strings.Cut(word, "="); ok && namePattern().MatchString(key) {
		return token{key: key, value: value}
	}
	return token{value: word}
}

var (
	namePattern     = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[a-z][a-z0-9-]*$`) })
	segmentPattern  = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`) })
	templatePattern = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_-]*(?:\.[A-Za-z_][A-Za-z0-9_-]*)*)(?:\|([a-z][a-z-]*))?\}`)
	})
)

type fieldRef struct {
	path   []string
	format string
}

func template(text string) ([]out, []fieldRef) {
	pieces := []out{}
	var refs []fieldRef
	at := 0
	for _, match := range templatePattern().FindAllStringSubmatchIndex(text, -1) {
		if match[0] > at {
			pieces = append(pieces, out{"text": text[at:match[0]]})
		}
		ref := fieldRef{path: strings.Split(text[match[2]:match[3]], ".")}
		piece := out{"field": ref.path}
		if match[4] >= 0 {
			ref.format = text[match[4]:match[5]]
			piece["format"] = ref.format
		}
		pieces = append(pieces, piece)
		refs = append(refs, ref)
		at = match[1]
	}
	if at < len(text) {
		pieces = append(pieces, out{"text": text[at:]})
	}
	return pieces, refs
}

func fieldPath(text string) ([]string, bool) {
	path := strings.Split(text, ".")
	for _, segment := range path {
		if !segmentPattern().MatchString(segment) {
			return nil, false
		}
	}
	return path, true
}

func wireName(name string) string { return strings.ReplaceAll(name, "-", "_") }

func suggest(word string, candidates []string) string {
	best, bestDistance := "", 3
	for _, candidate := range candidates {
		if d := distance(word, candidate); d < bestDistance && d < len(candidate) {
			best, bestDistance = candidate, d
		}
	}
	return best
}

func distance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

func names[T any](items []T, name func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, name(item))
	}
	return out
}

func quoted(words []string) string {
	q := make([]string, len(words))
	for i, word := range words {
		q[i] = fmt.Sprintf("%q", word)
	}
	return strings.Join(q, ", ")
}

func didYouMean(word string, candidates []string, otherwise string) string {
	if near := suggest(word, candidates); near != "" {
		return fmt.Sprintf("Did you mean %q?", near)
	}
	return otherwise
}
