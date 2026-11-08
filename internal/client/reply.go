package client

import (
	"bytes"
	"errors"
	"io"
)

const replyChunkBytes = 64 << 10

func readReply(conn io.Reader) ([]byte, error) {
	var chunks [][]byte
	var end jsonValueEnd
	for {
		chunk := make([]byte, replyChunkBytes)
		filled := 0
		for filled < len(chunk) {
			n, err := conn.Read(chunk[filled:])
			if at, done := end.scan(chunk[filled : filled+n]); done {
				chunks = append(chunks, chunk[:filled+at])
				return join(chunks), nil
			}
			filled += n
			if err != nil {
				if errors.Is(err, io.EOF) && (len(chunks) > 0 || filled > 0) {
					err = io.ErrUnexpectedEOF
				}
				return nil, err
			}
		}
		chunks = append(chunks, chunk)
	}
}

func join(chunks [][]byte) []byte {
	if len(chunks) == 1 {
		return chunks[0]
	}
	return bytes.Join(chunks, nil)
}

type jsonValueEnd struct {
	depth    int
	started  bool
	inString bool
	escaped  bool
}

func (e *jsonValueEnd) scan(data []byte) (int, bool) {
	for i, b := range data {
		switch {
		case e.inString:
			switch {
			case e.escaped:
				e.escaped = false
			case b == '\\':
				e.escaped = true
			case b == '"':
				e.inString = false
			}
		case b == '"':
			e.inString = true
		case b == '{' || b == '[':
			e.started = true
			e.depth++
		case b == '}' || b == ']':
			e.depth--
			if e.started && e.depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}
