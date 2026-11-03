package protocol

import (
	"errors"
	"fmt"
)

type RefusedError struct {
	reason Refusal
	err    error
}

func Refuse(reason Refusal, format string, args ...any) error {
	return &RefusedError{reason: reason, err: fmt.Errorf(format, args...)}
}

func (e *RefusedError) Error() string { return e.err.Error() }

func (e *RefusedError) Unwrap() error { return e.err }

func (e *RefusedError) Refusal() Refusal { return e.reason }

func RefusalOf(err error) *Refusal {
	var refused interface{ Refusal() Refusal }
	if errors.As(err, &refused) {
		return Ptr(refused.Refusal())
	}
	return nil
}
