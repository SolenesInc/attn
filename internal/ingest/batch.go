package ingest

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/victorarias/attn/internal/analytics"
)

const (
	maxBodyGzipBytes = 4 << 20
	maxBodyBytes     = 64 << 20
	maxLineBytes     = 64 << 10
	maxStreamBytes   = 32
	maxNameBytes     = 256
	maxSubjectBytes  = 256
)

var maxRecordsPerBatch = eventsPerInstallPerMinute.value

var streamPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type refusedError struct {
	status  int
	refusal analytics.Refusal
}

func (e *refusedError) Error() string {
	return fmt.Sprintf("%s: the limit is %d and this request has %d", e.refusal.Limit, e.refusal.Value, e.refusal.Observed)
}

type malformedError struct{ message string }

func (e *malformedError) Error() string { return e.message }

func malformed(format string, args ...any) error {
	return &malformedError{message: fmt.Sprintf(format, args...)}
}

func tooLarge(limit string, value, observed int64) error {
	return &refusedError{status: http.StatusRequestEntityTooLarge, refusal: analytics.Refusal{Limit: limit, Value: value, Observed: observed}}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func readBatch(body io.Reader, declaredLength int64) (analytics.Batch, error) {
	if declaredLength > maxBodyGzipBytes {
		return analytics.Batch{}, tooLarge("request_body_gzip_bytes", maxBodyGzipBytes, declaredLength)
	}
	compressed := &countingReader{r: io.LimitReader(body, maxBodyGzipBytes+1)}
	zr, err := gzip.NewReader(compressed)
	if err != nil {
		return analytics.Batch{}, malformed("the body must be gzip: %v", err)
	}
	plain := &countingReader{r: io.LimitReader(zr, maxBodyBytes+1)}
	lines := bufio.NewReaderSize(plain, maxLineBytes+1)
	batch, err := decodeLines(lines)
	if compressed.n > maxBodyGzipBytes {
		return analytics.Batch{}, tooLarge("request_body_gzip_bytes", maxBodyGzipBytes, compressed.n)
	}
	if plain.n > maxBodyBytes {
		return analytics.Batch{}, tooLarge("request_body_bytes", maxBodyBytes, plain.n)
	}
	return batch, err
}

func decodeLines(lines *bufio.Reader) (analytics.Batch, error) {
	var batch analytics.Batch
	sawBuild := false
	records := int64(0)
	for number := 1; ; number++ {
		line, length, err := readLine(lines)
		if errors.Is(err, io.EOF) && length == 0 {
			break
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return batch, malformed("line %d: %v", number, err)
		}
		if length > maxLineBytes {
			return batch, tooLarge("record_line_bytes", maxLineBytes, length)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !sawBuild {
			var header struct {
				Build *analytics.Build `json:"build"`
			}
			if err := json.Unmarshal(line, &header); err != nil || header.Build == nil {
				return batch, malformed(`line %d: the first line must be {"build": {...}}`, number)
			}
			batch.Build = *header.Build
			sawBuild = true
			continue
		}
		records++
		if records > maxRecordsPerBatch {
			continue
		}
		record, err := parseRecord(line)
		if err != nil {
			return batch, malformed("line %d: %v", number, err)
		}
		batch.Records = append(batch.Records, record)
	}
	if !sawBuild {
		return batch, malformed(`the body is empty; the first line must be {"build": {...}}`)
	}
	if records > maxRecordsPerBatch {
		return batch, tooLarge("records_per_batch", maxRecordsPerBatch, records)
	}
	return batch, nil
}

func readLine(r *bufio.Reader) ([]byte, int64, error) {
	line, err := r.ReadSlice('\n')
	length := int64(len(line))
	if !errors.Is(err, bufio.ErrBufferFull) {
		return line, length, err
	}
	for errors.Is(err, bufio.ErrBufferFull) {
		var rest []byte
		rest, err = r.ReadSlice('\n')
		length += int64(len(rest))
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return nil, length, err
}

func parseRecord(line []byte) (analytics.Record, error) {
	var record analytics.Record
	if err := json.Unmarshal(line, &record); err != nil {
		return record, err
	}
	switch {
	case len(record.Stream) > maxStreamBytes || !streamPattern.MatchString(string(record.Stream)):
		return record, fmt.Errorf("stream %q must be lowercase letters, digits and underscores, at most %d bytes", record.Stream, maxStreamBytes)
	case record.Name == "" || len(record.Name) > maxNameBytes:
		return record, fmt.Errorf("name must be 1 to %d bytes, got %d", maxNameBytes, len(record.Name))
	case len(record.Subject) > maxSubjectBytes:
		return record, fmt.Errorf("subject must be at most %d bytes, got %d", maxSubjectBytes, len(record.Subject))
	case record.At.IsZero():
		return record, fmt.Errorf("at is required")
	case record.Bytes < 0 || int64(record.Bytes) > 1<<32-1:
		return record, fmt.Errorf("bytes must be between 0 and %d, got %d", int64(1<<32-1), record.Bytes)
	}
	return record, nil
}
