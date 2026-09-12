package jobs

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const diagnosticOutputLimit = 16 << 10

const diagnosticTruncatedMarker = "\n…(diagnostic truncated)…\n"

type diagnosticFailure interface {
	DiagnosticOutput() string
}

type failureWithDiagnostic struct {
	cause      error
	diagnostic string
}

func (f failureWithDiagnostic) Error() string { return f.cause.Error() }
func (f failureWithDiagnostic) Unwrap() error { return f.cause }
func (f failureWithDiagnostic) DiagnosticOutput() string {
	return f.diagnostic
}

func WithDiagnostic(cause error, diagnostic string) error {
	if cause == nil {
		return nil
	}
	diagnostic = boundDiagnostic(diagnostic)
	if diagnostic == "" {
		return cause
	}
	return failureWithDiagnostic{cause: cause, diagnostic: diagnostic}
}

func DiagnosticOutput(err error) string {
	var failure diagnosticFailure
	if !errors.As(err, &failure) {
		return ""
	}
	return boundDiagnostic(failure.DiagnosticOutput())
}

func boundDiagnostic(diagnostic string) string {
	diagnostic = strings.TrimSpace(diagnostic)
	if len(diagnostic) <= diagnosticOutputLimit {
		return diagnostic
	}
	keep := diagnosticOutputLimit - len(diagnosticTruncatedMarker)
	headEnd := keep / 2
	for headEnd > 0 && !utf8.RuneStart(diagnostic[headEnd]) {
		headEnd--
	}
	tailStart := len(diagnostic) - (keep - headEnd)
	for tailStart < len(diagnostic) && !utf8.RuneStart(diagnostic[tailStart]) {
		tailStart++
	}
	return diagnostic[:headEnd] + diagnosticTruncatedMarker + diagnostic[tailStart:]
}
