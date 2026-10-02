package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func retrieveAgentAttachment(cli *client.Client, capture, id, path string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".attn-attachment-*")
	if err != nil {
		return err
	}
	defer func() { temp.Close(); os.Remove(temp.Name()) }()
	var offset int
	for {
		result, err := cli.Capture(protocol.CaptureAttachmentGetMessage{Cmd: protocol.CmdCaptureAttachmentGet, CaptureID: capture, AttachmentID: id, Offset: offset})
		if err != nil {
			return err
		}
		chunk := result.Download
		if chunk == nil {
			return fmt.Errorf("daemon returned no file chunk")
		}
		data, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil {
			return err
		}
		if chunk.NextOffset != offset+len(data) || (!chunk.Eof && len(data) == 0) {
			return fmt.Errorf("file download offset receipt disagrees with bytes")
		}
		if _, err := temp.Write(data); err != nil {
			return err
		}
		offset = chunk.NextOffset
		if chunk.Eof {
			break
		}
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// Link publishes without overwriting an existing user file; the temporary file is on the same filesystem.
	if err := os.Link(temp.Name(), path); err != nil {
		return err
	}
	return nil
}
func runAgentAttachment(args []string) {
	if hasHelpFlag(args) {
		writeAgentHelp(os.Stdout)
		return
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: attn agent attachment <capture-id> <attachment-id> --out <path>")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("agent attachment", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("out", "", "local output path")
	if err := fs.Parse(args[2:]); err != nil || *out == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "agent attachment: --out <path> is required")
		os.Exit(2)
	}
	if err := retrieveAgentAttachment(client.New(""), args[0], args[1], *out); err != nil {
		fmt.Fprintf(os.Stderr, "agent attachment: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "Saved file to %s. Inspect it with your tools.\n", *out)
}
