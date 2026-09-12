package jobs

import (
	"errors"
	"strings"
)

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
	diagnostic = strings.TrimSpace(diagnostic)
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
	return strings.TrimSpace(failure.DiagnosticOutput())
}
