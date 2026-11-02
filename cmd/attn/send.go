package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

// 524288 raw bytes encode to a 699309-byte request, measured for the app's 1 MiB WebSocket frames; the socket allows 8 MiB.
const captureUploadChunkBytes = 524288

type fileList []string

func (f *fileList) String() string     { return strings.Join(*f, ",") }
func (f *fileList) Set(v string) error { *f = append(*f, v); return nil }

func runSend() {
	exit(executeSend(os.Args[2:], os.Stdout, os.Stderr))
}

func executeSend(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasHelpFlag(args) {
		writeSendHelp(stdout)
		return 0
	}
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("m", "", "the message")
	asJSON := fs.Bool("json", false, "print the saved capture as JSON")
	var files fileList
	fs.Var(&files, "file", "a file to send; repeat for more")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil || len(positionals) != 1 {
		fmt.Fprintln(stderr, "usage: attn send <session> [--file <path>]... [-m <text>] [--json]")
		return 2
	}
	if strings.TrimSpace(*message) == "" && len(files) == 0 {
		fmt.Fprintln(stderr, "send: give -m <text>, --file <path>, or both")
		return 2
	}
	record, err := sendCapture(client.New(""), protocol.SessionID(positionals[0]), *message, files)
	if err != nil {
		fmt.Fprintf(stderr, "send: %v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(record)
		return 0
	}
	fmt.Fprintf(stdout, "Sent %d file(s) and the message to session %s as capture %s.\n", len(record.Attachments), *record.Mailbox.SessionID, record.ID)
	return 0
}

func sendCapture(cli *client.Client, session protocol.SessionID, message string, files []string) (*protocol.QuickCaptureRecord, error) {
	shown, err := cli.SessionShow(session)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", session, daemonMessage(err))
	}
	if shown.Entry.ClosedAt != nil {
		return nil, fmt.Errorf("session %s is closed", shown.Entry.ID)
	}
	profileID := protocol.Ptr(shown.Entry.ProfileID)
	captureID := uuid.NewString()
	attachmentIDs := []string{}
	discard := func() {
		for _, id := range attachmentIDs {
			_, _ = cli.QuickCapture(protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, ProfileID: profileID, CaptureID: captureID, AttachmentID: id})
		}
	}
	for _, path := range files {
		id := uuid.NewString()
		attachmentIDs = append(attachmentIDs, id)
		if err := uploadCaptureFile(cli, profileID, captureID, id, path); err != nil {
			discard()
			return nil, fmt.Errorf("%s: %w", path, daemonMessage(err))
		}
	}
	result, err := cli.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd:           protocol.CmdQuickCaptureSend,
		ProfileID:     profileID,
		CaptureID:     captureID,
		Mailbox:       protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(shown.Entry.ID)},
		Content:       message,
		AttachmentIds: attachmentIDs,
	})
	if err != nil {
		discard()
		return nil, daemonMessage(err)
	}
	return result.Record, nil
}

func uploadCaptureFile(cli *client.Client, profileID *string, captureID, id, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	size := int(info.Size())
	chunk := make([]byte, captureUploadChunkBytes)
	for offset := 0; ; {
		n, err := io.ReadFull(f, chunk[:min(len(chunk), size-offset)])
		if err != nil && n == 0 && offset < size {
			return err
		}
		final := offset+n == size
		result, err := cli.QuickCapture(protocol.QuickCaptureAttachmentPutMessage{
			Cmd:          protocol.CmdQuickCaptureAttachmentPut,
			ProfileID:    profileID,
			CaptureID:    captureID,
			AttachmentID: id,
			Name:         filepath.Base(path),
			Offset:       offset,
			DataBase64:   base64.StdEncoding.EncodeToString(chunk[:n]),
			Final:        final,
		})
		if err != nil {
			return err
		}
		if result.Upload == nil || result.Upload.NextOffset != offset+n {
			return fmt.Errorf("daemon upload receipt disagrees with the %d bytes sent at offset %d", n, offset)
		}
		offset += n
		if final {
			return nil
		}
	}
}

func writeSendHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn send <session> [--file <path>]... [-m <text>] [--json]

Send files and a message to a running agent session, local or on an outpost,
as the user. attn types the message into the session's conversation, with an
attn agent attachment command for each file that saves it where the agent
runs. Give -m, --file, or both.

  <session>        the receiving session's id; attn session list --json names
                   each live session with its label and host
  --file <path>    a file to send; repeat for more. Its name is kept
  -m <text>        the message
  --json           print the saved capture: id, mailbox, content,
                   created_at, and attachments with id, name, media_type
                   and bytes

The files stay on this daemon after delivery. A failed send removes the files
it uploaded and exits nonzero.
`)
}
