package main

import (
	"bufio"
	"io"
	"text/tabwriter"
)

type tableWriter struct {
	*tabwriter.Writer
	out *bufio.Writer
}

func newTableWriter(w io.Writer) *tableWriter {
	out := bufio.NewWriter(w)
	return &tableWriter{Writer: tabwriter.NewWriter(out, 0, 0, 2, ' ', 0), out: out}
}

func (t *tableWriter) Flush() error {
	if err := t.Writer.Flush(); err != nil {
		return err
	}
	return t.out.Flush()
}
