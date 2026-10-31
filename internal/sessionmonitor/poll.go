package sessionmonitor

import "github.com/victorarias/attn/internal/stepclock"

type transcriptPoll struct {
	timer  stepclock.Timer
	passes chan chan struct{}
	exited chan struct{}
	passed func()
}

func startTranscriptPoll(clock *stepclock.Clock) *transcriptPoll {
	p := &transcriptPoll{passes: make(chan chan struct{}), exited: make(chan struct{}), passed: func() {}}
	p.timer = clock.AfterFunc(TranscriptPoll, transcriptPollInterval, func() {
		done := make(chan struct{})
		select {
		case p.passes <- done:
			<-done
		case <-p.exited:
		}
	})
	return p
}

func (p *transcriptPoll) take(done chan struct{}) {
	p.passed = func() { close(done) }
	p.timer.Reset(transcriptPollInterval)
}

func (p *transcriptPoll) finishPass() {
	p.passed()
	p.passed = func() {}
}

func (p *transcriptPoll) stop() {
	p.timer.Stop()
	close(p.exited)
	p.finishPass()
}
