package fakeclickhouse

import "strings"

type tokenKind int

const (
	tokenEnd tokenKind = iota
	tokenWord
	tokenString
	tokenNumber
	tokenSymbol
)

type token struct {
	kind  tokenKind
	text  string
	start int
	end   int
}

var symbols = []string{"!=", "<=", ">=", "(", ")", ",", ".", "=", "<", ">", "*", "{", "}", ":"}

var stringEscapes = map[byte]byte{'n': '\n', 't': '\t', 'r': '\r', '0': 0, 'b': '\b', 'f': '\f', 'a': '\a', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"'}

func scan(source string, offset int) token {
	for offset < len(source) && strings.IndexByte(" \t\r\n", source[offset]) >= 0 {
		offset++
	}
	if offset == len(source) {
		return token{kind: tokenEnd, start: offset, end: offset}
	}
	end := offset
	switch first := source[offset]; {
	case isWordStart(first):
		for end < len(source) && (isWordStart(source[end]) || isDigit(source[end])) {
			end++
		}
		return token{tokenWord, source[offset:end], offset, end}
	case isDigit(first):
		for end < len(source) && isDigit(source[end]) {
			end++
		}
		return token{tokenNumber, source[offset:end], offset, end}
	case first == '\'':
		return scanString(source, offset)
	}
	for _, symbol := range symbols {
		if strings.HasPrefix(source[offset:], symbol) {
			return token{tokenSymbol, symbol, offset, offset + len(symbol)}
		}
	}
	panic(syntaxFailure(source, offset, "a word, a number, a string or an operator"))
}

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func scanString(source string, offset int) token {
	var text strings.Builder
	for at := offset + 1; at < len(source); at++ {
		switch c := source[at]; {
		case c == '\\' && at+1 < len(source):
			at++
			if escaped, ok := stringEscapes[source[at]]; ok {
				text.WriteByte(escaped)
			} else {
				text.WriteByte('\\')
				text.WriteByte(source[at])
			}
		case c == '\'' && at+1 < len(source) && source[at+1] == '\'':
			at++
			text.WriteByte('\'')
		case c == '\'':
			return token{tokenString, text.String(), offset, at + 1}
		default:
			text.WriteByte(c)
		}
	}
	panic(syntaxFailure(source, offset, "the closing quote of a string"))
}

func syntaxFailure(source string, offset int, expected string) *queryError {
	rest := source[offset:]
	near, _, _ := strings.Cut(rest, " ")
	if len(near) > 40 {
		near = near[:40]
	}
	if len(rest) > 160 {
		rest = rest[:160]
	}
	return failure(syntaxError, "Syntax error: failed at position %d (%s): %s. Expected %s. %s", offset+1, near, rest, expected, teach)
}
