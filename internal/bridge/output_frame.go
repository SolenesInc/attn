package bridge

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const outputFrameHeaderBytes = 9
const MaxOutputFrameBytes = pty.OutputEventMaxBytes + outputFrameHeaderBytes

func encodeOutputEvent(event ptybackend.OutputEvent) ([]byte, error) {
	if event.Kind == ptybackend.OutputEventKindOutput {
		frame := make([]byte, outputFrameHeaderBytes+len(event.Data))
		binary.BigEndian.PutUint32(frame, uint32(len(frame)-4))
		frame[4] = 1
		binary.BigEndian.PutUint32(frame[5:], event.Seq)
		copy(frame[outputFrameHeaderBytes:], event.Data)
		return frame, nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 5+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(frame)-4))
	copy(frame[5:], data)
	return frame, nil
}

func readOutputEvent(reader io.Reader) (ptybackend.OutputEvent, int, error) {
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return ptybackend.OutputEvent{}, 0, err
	}
	n := int(binary.BigEndian.Uint32(header[:]))
	if n < 1 {
		return ptybackend.OutputEvent{}, 0, fmt.Errorf("bridge output frame length=%d, need at least 1", n)
	}
	data := make([]byte, n-1)
	if _, err := io.ReadFull(reader, data); err != nil {
		return ptybackend.OutputEvent{}, 0, err
	}
	var event ptybackend.OutputEvent
	switch header[4] {
	case 1:
		if len(data) < 4 {
			return event, 0, fmt.Errorf("bridge output sequence needs 4 bytes, received %d", len(data))
		}
		event = ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindOutput, Seq: binary.BigEndian.Uint32(data), Data: data[4:]}
	case 0:
		if err := json.Unmarshal(data, &event); err != nil {
			return event, 0, err
		}
	default:
		return event, 0, fmt.Errorf("unknown bridge output encoding %d", header[4])
	}
	return event, n + 4, nil
}
