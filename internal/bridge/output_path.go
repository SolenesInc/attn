package bridge

import "time"

const (
	outputPathHistory = 5 * time.Second
	outputProbeRounds = 8
	outputProbeGain   = 1.25
	outputDrainGain   = 0.75
)

type outputSend struct {
	bytes int
	at    time.Time
}

type outputDelay struct {
	at    time.Time
	delay time.Duration
}

type outputRate struct {
	at   time.Time
	rate float64
}

type outputPath struct {
	delays       []outputDelay
	rates        []outputRate
	rate         float64
	next         time.Time
	ackAt        time.Time
	lastAckAt    time.Time
	learning     *outputFlow
	sendAt       time.Time
	ackBytes     int
	phaseAt      time.Time
	phaseStarted time.Time
	drained      bool
	drainDelay   time.Duration
	latest       time.Time
	phase        int
	frame        int
}

func (p *outputPath) sent(flow *outputFlow, n int, now time.Time) {
	flow.sent = append(flow.sent, outputSend{n, now})
	if p.rate > 0 {
		gain := 1.0
		if len(p.rates) > 0 {
			if !now.Before(p.phaseAt) && (p.phase != 1 || p.drained) {
				p.phase = (p.phase + 1) % outputProbeRounds
				p.phaseStarted, p.drained = now, false
				p.drainDelay = 0
				p.phaseAt = now.Add(p.delays[0].delay + time.Duration(float64(p.frame)/p.rates[0].rate*float64(time.Second)))
			}
			switch p.phase {
			case 0:
				gain = outputProbeGain
			case 1:
				gain = outputDrainGain
			}
		}
		quantum := time.Duration(float64(outputChunkBytes) / (p.rate * gain) * float64(time.Second))
		if floor := now.Add(-quantum); p.next.Before(floor) {
			p.next = floor
		}
		p.next = p.next.Add(time.Duration(float64(n) / (p.rate * gain) * float64(time.Second)))
	}
}

func (p *outputPath) returned(flow *outputFlow, n int, now time.Time) {
	feedback := flow.outstanding >= bulkChunkBytes
	frame := n
	acknowledged := 0
	var first time.Time
	var sent time.Time
	for n > 0 && len(flow.sent) > 0 {
		chunk := &flow.sent[0]
		if first.IsZero() {
			first = chunk.at
		}
		taken := min(n, chunk.bytes)
		n -= taken
		chunk.bytes -= taken
		sent = chunk.at
		acknowledged += taken
		if chunk.bytes == 0 {
			flow.sent = flow.sent[1:]
		}
	}
	if sent.IsZero() || !feedback {
		return
	}
	if sent.Before(p.latest) {
		return
	}
	p.latest = sent
	if first.After(p.lastAckAt) {
		p.ackAt = time.Time{}
	}
	p.lastAckAt = now
	p.frame = max(p.frame, frame)
	p.ackBytes += acknowledged
	oldRate := p.rate
	defer func() {
		if oldRate > 0 && p.rate > 0 && p.next.After(now) {
			p.next = now.Add(time.Duration(float64(p.next.Sub(now)) * oldRate / p.rate))
		}
	}()
	delay := now.Sub(sent)
	if delay <= 0 {
		return
	}
	if p.phase == 1 && !first.Before(p.phaseStarted) {
		p.drained = p.drained || (p.drainDelay > 0 && delay >= p.drainDelay)
		p.drainDelay = delay
	}
	for len(p.delays) > 0 && now.Sub(p.delays[0].at) > outputPathHistory {
		p.delays = p.delays[1:]
	}
	for len(p.delays) > 0 && p.delays[len(p.delays)-1].delay >= delay {
		p.delays = p.delays[:len(p.delays)-1]
	}
	p.delays = append(p.delays, outputDelay{now, delay})
	base := p.delays[0].delay
	if p.ackAt.IsZero() {
		if len(p.rates) == 0 {
			p.rate = float64(p.ackBytes) / delay.Seconds()
			p.learning = flow
		}
		p.ackAt, p.sendAt, p.ackBytes = now, sent, 0
		if p.phaseAt.IsZero() {
			p.phaseAt = now
		}
		return
	}
	if elapsed := max(now.Sub(p.ackAt), sent.Sub(p.sendAt)); elapsed >= base {
		rate := float64(p.ackBytes) / elapsed.Seconds()
		for len(p.rates) > 0 && now.Sub(p.rates[0].at) > outputPathHistory {
			p.rates = p.rates[1:]
		}
		for len(p.rates) > 0 && p.rates[len(p.rates)-1].rate <= rate {
			p.rates = p.rates[:len(p.rates)-1]
		}
		p.rates = append(p.rates, outputRate{now, rate})
		p.learning = nil
		p.ackAt, p.sendAt, p.ackBytes = now, sent, 0
	}
	if len(p.rates) > 0 {
		bandwidth := p.rates[0].rate
		headroom := time.Duration(float64(outputChunkBytes/2) / bandwidth * float64(time.Second))
		p.rate = bandwidth * (base + headroom).Seconds() / delay.Seconds()
	}
}
