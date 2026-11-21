package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

type RefusedError struct {
	reason Refusal
	err    error
}

func Refuse(reason Refusal, format string, args ...any) error {
	return &RefusedError{reason: reason, err: fmt.Errorf(format, args...)}
}

func (e *RefusedError) Error() string { return CLIText(e.err.Error()) }

func (e *RefusedError) Unwrap() error { return e.err }

func (e *RefusedError) Refusal() Refusal { return e.reason }

func (e *RefusedError) Mentioned() string { return e.err.Error() }

func RefusalOf(err error) *Refusal {
	var refused interface{ Refusal() Refusal }
	if errors.As(err, &refused) {
		return Ptr(refused.Refusal())
	}
	return nil
}

var CommandMention = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile("`?\\{(attn [^{}]*)\\}`?") })

func CLIText(text string) string {
	return CommandMention().ReplaceAllStringFunc(text, func(mention string) string {
		open := strings.Index(mention, "{")
		close := strings.LastIndex(mention, "}")
		return mention[:open] + mention[open+1:close] + mention[close+1:]
	})
}

func MentionedText(err error) string {
	text := err.Error()
	var refused interface {
		error
		Mentioned() string
	}
	if errors.As(err, &refused) {
		return strings.Replace(text, refused.Error(), refused.Mentioned(), 1)
	}
	return text
}
