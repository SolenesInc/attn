package pty

func indexOfByte(b []byte, target byte) int {
	for i, c := range b {
		if c == target {
			return i
		}
	}
	return -1
}

var kittyAPCIntroducer = []byte{0x1b, 0x5f, 0x47}

const kittySegMaxPendingBytes = 72 * 1024 * 1024

const osc133MarkerMaxPendingBytes = 16 * 1024 * 1024

type kittySegMode uint8

const (
	kittySegGround kittySegMode = iota
	kittySegEscape
	kittySegEscapeIntermediate
	kittySegCSI
	kittySegOSC
	kittySegOSC133Prefix
	kittySegOSC133Body
	kittySegOpaque
	kittySegKitty
)

func c1Executed(b byte) bool {
	switch {
	case b >= 0x80 && b <= 0x8f:
		return true
	case b >= 0x91 && b <= 0x97:
		return true
	case b >= 0x99 && b <= 0x9a:
		return true
	case b == 0x9c:
		return true
	}
	return false
}

func kittySegAborts(b byte) bool {
	return b == 0x18 || b == 0x1a || c1Executed(b)
}

func kittySegOpensInsideString(b byte) (kittySegMode, bool) {
	switch b {
	case 0x90:
		return kittySegOpaque, true
	case 0x9b:
		return kittySegCSI, true
	case 0x9d:
		return kittySegOSC, true
	}
	return 0, false
}

func kittySegOpensC1(b byte) (kittySegMode, bool) {
	switch b {
	case 0x98, 0x9e, 0x9f:
		return kittySegOpaque, true
	}
	return kittySegOpensInsideString(b)
}

func kittySegOpens7Bit(b byte) (kittySegMode, bool) {
	switch b {
	case 'P', 'X', '^', '_':
		return kittySegOpaque, true
	case ']':
		return kittySegOSC, true
	case '[':
		return kittySegCSI, true
	}
	return kittySegOpensC1(b)
}

type feedSegKind uint8

const (
	feedSegPlain feedSegKind = iota
	feedSegKittyAPC
	feedSegOSC133
)

type feedSegment struct {
	Kind   feedSegKind
	Bytes  []byte
	Marker *osc133Marker
}

type feedSegmenter struct {
	mode    kittySegMode
	pending []byte
	resume  int
}

func (m kittySegMode) holding() bool {
	return m == kittySegKitty || m == kittySegOSC133Prefix || m == kittySegOSC133Body
}

func (m kittySegMode) maxPending() int {
	if m == kittySegKitty {
		return kittySegMaxPendingBytes
	}
	return osc133MarkerMaxPendingBytes
}

func (m kittySegMode) abandoned() kittySegMode {
	if m == kittySegKitty {
		return kittySegOpaque
	}
	return kittySegOSC
}

func (s *feedSegmenter) Feed(chunk []byte, emit func(feedSegment)) {
	if s.mode == kittySegGround && len(s.pending) == 0 && indexOfByte(chunk, oscESC) < 0 {
		if len(chunk) > 0 {
			emit(feedSegment{Kind: feedSegPlain, Bytes: chunk})
		}
		return
	}

	carried := len(s.pending) > 0
	buffer := chunk
	holdStart := -1
	i := 0
	if carried {
		s.pending = append(s.pending, chunk...)
		buffer = s.pending
		if s.mode.holding() {
			holdStart = 0
			i = s.resume
		}
	}

	emitPlain := func(from, to int) {
		if to > from {
			emit(feedSegment{Kind: feedSegPlain, Bytes: buffer[from:to]})
		}
	}

	plainStart := 0

scan:
	for i < len(buffer) {
		b := buffer[i]
		switch s.mode {
		case kittySegGround:
			if b != oscESC {
				i++
				continue
			}
			if i+1 >= len(buffer) || (buffer[i+1] == kittyAPCIntroducer[1] && i+2 >= len(buffer)) {
				emitPlain(plainStart, i)
				s.hold(buffer, carried, i, i)
				return
			}
			switch {
			case buffer[i+1] == ']':
				holdStart = i
				s.mode = kittySegOSC133Prefix
				i += 2
			case buffer[i+1] == kittyAPCIntroducer[1] && buffer[i+2] == kittyAPCIntroducer[2]:
				holdStart = i
				s.mode = kittySegKitty
				i += len(kittyAPCIntroducer)
			default:
				s.mode = kittySegEscape
				i++
			}

		case kittySegEscape, kittySegEscapeIntermediate:
			if mode, ok := kittySegOpensC1(b); ok {
				s.mode = mode
				i++
				continue
			}
			if s.mode == kittySegEscape {
				if mode, ok := kittySegOpens7Bit(b); ok {
					s.mode = mode
					i++
					continue
				}
			}
			switch {
			case b == oscESC:
				s.mode = kittySegEscape
			case b == 0x18 || b == 0x1a || c1Executed(b):
				s.mode = kittySegGround
			case b >= 0x20 && b <= 0x2f:
				s.mode = kittySegEscapeIntermediate
			case b >= 0x30 && b <= 0x7e:
				s.mode = kittySegGround
			default:
			}
			i++

		case kittySegCSI:
			switch {
			case b == oscESC:
				s.mode = kittySegEscape
			case b == 0x18 || b == 0x1a:
				s.mode = kittySegGround
			case b >= 0x80:
				if mode, ok := kittySegOpensC1(b); ok {
					s.mode = mode
				} else if c1Executed(b) {
					s.mode = kittySegGround
				}
			case b >= 0x40 && b <= 0x7e:
				s.mode = kittySegGround
			}
			i++

		case kittySegOSC:
			switch b {
			case oscESC:
				s.mode = kittySegEscape
			case 0x07, 0x18, 0x1a:
				s.mode = kittySegGround
			}
			i++

		case kittySegOSC133Prefix:
			if b == osc133Prefix[i-holdStart] {
				i++
				if i-holdStart == len(osc133Prefix) {
					s.mode = kittySegOSC133Body
				}
				continue
			}
			holdStart = -1
			s.mode = kittySegOSC

		case kittySegOSC133Body:
			switch {
			case b == oscBEL:
				i++
				emitPlain(plainStart, holdStart)
				emitMarker(emit, buffer[holdStart:i])
				plainStart = i
				holdStart = -1
				s.mode = kittySegGround
			case b == oscESC:
				if i+1 >= len(buffer) {
					break scan
				}
				if buffer[i+1] == oscBackslash {
					i += 2
					emitPlain(plainStart, holdStart)
					emitMarker(emit, buffer[holdStart:i])
					plainStart = i
					holdStart = -1
					s.mode = kittySegGround
					continue
				}
				holdStart = -1
				s.mode = kittySegEscape
				i++
			case b == 0x18 || b == 0x1a:
				holdStart = -1
				s.mode = kittySegGround
				i++
			default:
				i++
			}

		case kittySegOpaque:
			switch {
			case b == oscESC:
				s.mode = kittySegEscape
			case kittySegAborts(b):
				s.mode = kittySegGround
			default:
				if mode, ok := kittySegOpensInsideString(b); ok {
					s.mode = mode
				}
			}
			i++

		case kittySegKitty:
			switch {
			case b == oscESC:
				if i+1 >= len(buffer) {
					break scan
				}
				if buffer[i+1] == oscBackslash {
					emitPlain(plainStart, holdStart)
					emit(feedSegment{Kind: feedSegKittyAPC, Bytes: buffer[holdStart : i+2]})
					i += 2
					plainStart = i
					holdStart = -1
					s.mode = kittySegGround
					continue
				}
				holdStart = -1
				s.mode = kittySegEscape
				i++
			case b == 0x9c:
				emitPlain(plainStart, holdStart)
				i++
				emit(feedSegment{Kind: feedSegKittyAPC, Bytes: buffer[holdStart:i]})
				plainStart = i
				holdStart = -1
				s.mode = kittySegGround
			case kittySegAborts(b):
				holdStart = -1
				s.mode = kittySegGround
				i++
			default:
				if mode, ok := kittySegOpensInsideString(b); ok {
					holdStart = -1
					s.mode = mode
				}
				i++
			}
		}
	}

	if holdStart >= 0 {
		if len(buffer)-holdStart > s.mode.maxPending() {
			emitPlain(plainStart, len(buffer))
			s.release()
			s.mode = s.mode.abandoned()
			return
		}
		emitPlain(plainStart, holdStart)
		s.hold(buffer, carried, holdStart, i)
		return
	}
	emitPlain(plainStart, len(buffer))
	s.release()
}

func emitMarker(emit func(feedSegment), raw []byte) {
	payloadEnd := len(raw) - 1
	if raw[len(raw)-1] != oscBEL {
		payloadEnd--
	}
	emit(feedSegment{
		Kind:   feedSegOSC133,
		Bytes:  raw,
		Marker: osc133MarkerFromPayload(string(raw[len(osc133Prefix):payloadEnd])),
	})
}

func (s *feedSegmenter) hold(buffer []byte, carried bool, from, resumeAt int) {
	if from >= len(buffer) {
		s.release()
		return
	}
	if carried && resumeAt > from {
		if from > 0 {
			s.pending = s.pending[:copy(s.pending, buffer[from:])]
		}
		s.resume = resumeAt - from
		return
	}
	s.pending = append([]byte(nil), buffer[from:]...)
	s.resume = resumeAt - from
}

func (s *feedSegmenter) release() {
	s.pending = nil
	s.resume = 0
}
