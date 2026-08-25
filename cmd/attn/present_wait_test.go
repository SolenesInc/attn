package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestWaitForPresentFeedbackPrintsOnceThenReturns(t *testing.T) {
	type step struct {
		result *protocol.PresentFeedbackResult
		err    error
	}
	steps := []step{
		{&protocol.PresentFeedbackResult{Submitted: false, PresentationStatus: "open"}, nil},
		{&protocol.PresentFeedbackResult{Submitted: false, PresentationStatus: "open"}, nil},
		{&protocol.PresentFeedbackResult{Submitted: true, Markdown: "## feedback\n", PresentationStatus: "open"}, nil},
	}

	fetchCh := make(chan step, len(steps))
	for _, s := range steps {
		fetchCh <- s
	}
	fetch := func() (*protocol.PresentFeedbackResult, error) {
		s := <-fetchCh
		return s.result, s.err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time) // unbuffered: a send blocks until the loop is back at its select
	var out bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- waitForPresentFeedback(ctx, tick, fetch, &out, false)
	}()

	tick <- time.Time{}
	tick <- time.Time{}

	if err := <-errCh; err != nil {
		t.Fatalf("waitForPresentFeedback returned error %v, want nil", err)
	}
	if got := strings.Count(out.String(), "## feedback"); got != 1 {
		t.Fatalf("feedback printed %d times, want exactly 1\nout:\n%s", got, out.String())
	}
}

func TestWaitForPresentFeedbackReturnsCtxErrOnCancel(t *testing.T) {
	fetch := func() (*protocol.PresentFeedbackResult, error) {
		return &protocol.PresentFeedbackResult{Submitted: false, PresentationStatus: "open"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	var out bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- waitForPresentFeedback(ctx, tick, fetch, &out, false)
	}()

	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForPresentFeedback returned %v, want context.Canceled", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output before submission, got %q", out.String())
	}
}

func TestWaitForPresentFeedbackKeepsPollingThroughTransientError(t *testing.T) {
	type step struct {
		result *protocol.PresentFeedbackResult
		err    error
	}
	down := errors.New("daemon down")
	steps := []step{
		{nil, down},
		{nil, down},
		{&protocol.PresentFeedbackResult{Submitted: true, Markdown: "ok", PresentationStatus: "open"}, nil},
	}

	fetchCh := make(chan step, len(steps))
	for _, s := range steps {
		fetchCh <- s
	}
	fetch := func() (*protocol.PresentFeedbackResult, error) {
		s := <-fetchCh
		return s.result, s.err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	var out bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- waitForPresentFeedback(ctx, tick, fetch, &out, false)
	}()

	tick <- time.Time{}
	tick <- time.Time{}

	if err := <-errCh; err != nil {
		t.Fatalf("waitForPresentFeedback returned error %v, want nil", err)
	}
	if got := out.String(); got != "ok" {
		t.Fatalf("out = %q, want %q", got, "ok")
	}
}

func TestWaitForPresentFeedbackReturnsOnClose(t *testing.T) {
	type step struct {
		result *protocol.PresentFeedbackResult
		err    error
	}
	steps := []step{
		{&protocol.PresentFeedbackResult{Submitted: false, PresentationStatus: "open"}, nil},
		{&protocol.PresentFeedbackResult{Submitted: false, PresentationStatus: "closed"}, nil},
	}

	fetchCh := make(chan step, len(steps))
	for _, s := range steps {
		fetchCh <- s
	}
	fetch := func() (*protocol.PresentFeedbackResult, error) {
		s := <-fetchCh
		return s.result, s.err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	var out bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- waitForPresentFeedback(ctx, tick, fetch, &out, false)
	}()

	tick <- time.Time{}

	if err := <-errCh; err != nil {
		t.Fatalf("waitForPresentFeedback returned error %v, want nil", err)
	}
	got := out.String()
	if !strings.Contains(got, "closed by the reviewer") {
		t.Fatalf("out = %q, want a message mentioning the presentation was closed by the reviewer", got)
	}
	if strings.Contains(got, "##") {
		t.Fatalf("out = %q, markdown should never be printed on close", got)
	}
}
