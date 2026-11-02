package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

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
	exit(executeSend(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
}

func executeSend(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasHelpFlag(args) {
		writeSendHelp(stdout)
		return 0
	}
	switch args[0] {
	case "pair":
		setCommandPath("send pair")
		return executeSendPair(args[1:], stdin, stdout, stderr)
	case "senders":
		setCommandPath("send senders")
		return executeSendSenders(args[1:], stdout, stderr)
	case "unpair":
		setCommandPath("send unpair")
		return executeSendUnpair(args[1:], stdout, stderr)
	}
	setCommandPath("send")
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	message := fs.String("m", "", "the message")
	asJSON := fs.Bool("json", false, "print the saved capture as JSON")
	tokenStdin := fs.Bool("token-stdin", false, "read the paired sender's token from stdin")
	var files fileList
	fs.Var(&files, "file", "a file to send; repeat for more")
	positionals, err := parseInterspersedFlagArgs(fs, args)
	if err != nil || len(positionals) != 1 {
		fmt.Fprintln(stderr, "usage: attn send <session> --token-stdin [--file <path>]... [-m <text>] [--json]")
		return 2
	}
	if strings.TrimSpace(*message) == "" && len(files) == 0 {
		fmt.Fprintln(stderr, "send: give -m <text>, --file <path>, or both")
		return 2
	}
	if !*tokenStdin {
		fmt.Fprintln(stderr, "send: --token-stdin is required; pipe in the token attn send pair printed")
		return 2
	}
	token, err := bufio.NewReader(stdin).ReadString('\n')
	if strings.TrimSpace(token) == "" {
		fmt.Fprintf(stderr, "send: no token on stdin: %v\n", err)
		return 2
	}
	record, err := sendCapture(client.New(""), strings.TrimSpace(token), protocol.SessionID(positionals[0]), *message, files)
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

func sendCapture(cli *client.Client, token string, session protocol.SessionID, message string, files []string) (*protocol.QuickCaptureRecord, error) {
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
			_, _ = cli.QuickCapture(protocol.QuickCaptureAttachmentDiscardMessage{Cmd: protocol.CmdQuickCaptureAttachmentDiscard, ProfileID: profileID, SenderToken: &token, CaptureID: captureID, AttachmentID: id})
		}
	}
	for _, path := range files {
		id := uuid.NewString()
		attachmentIDs = append(attachmentIDs, id)
		if err := uploadCaptureFile(cli, &token, profileID, captureID, id, path); err != nil {
			discard()
			return nil, fmt.Errorf("%s: %w", path, daemonMessage(err))
		}
	}
	result, err := cli.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd:           protocol.CmdQuickCaptureSend,
		ProfileID:     profileID,
		SenderToken:   &token,
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

func uploadCaptureFile(cli *client.Client, token, profileID *string, captureID, id, path string) error {
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
			SenderToken:  token,
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

func executeSendPair(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send pair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "the name the user sees")
	asJSON := fs.Bool("json", false, "print the request and the token as two JSON lines")
	if err := parseFlags(fs, args); err != nil || fs.NArg() != 0 || strings.TrimSpace(*name) == "" {
		fmt.Fprintln(stderr, "usage: attn send pair --name <name> [--json]")
		return 2
	}
	paired, err := client.New("").SenderPair(*name, func(request protocol.SenderPairingRequest) {
		if *asJSON {
			printJSONLine(stdout, request)
			return
		}
		fmt.Fprintf(stdout, "Waiting for you to allow %q in the attn app. Check that it shows code %s.\n", request.Name, request.Code)
	})
	if err != nil {
		fmt.Fprintf(stderr, "send pair: %v\n", daemonMessage(err))
		return 1
	}
	if *asJSON {
		printJSONLine(stdout, paired)
		return 0
	}
	fmt.Fprintf(stdout, "Paired %q. Its token, shown only now:\n%s\n", paired.Sender.Name, paired.Token)
	return 0
}

func executeSendSenders(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send senders", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print the paired senders as JSON")
	if err := parseFlags(fs, args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: attn send senders [--json]")
		return 2
	}
	senders, err := client.New("").PairedSenders()
	if err != nil {
		fmt.Fprintf(stderr, "send senders: %v\n", daemonMessage(err))
		return 1
	}
	if *asJSON {
		printJSONLine(stdout, senders)
		return 0
	}
	if len(senders) == 0 {
		fmt.Fprintln(stdout, "No paired senders. Pair one with attn send pair --name <name>.")
		return 0
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tPAIRED\tLAST USED\tID")
	for _, s := range senders {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", s.Name, s.CreatedAt, orDash(protocol.Deref(s.LastUsedAt)), s.ID)
	}
	table.Flush()
	return 0
}

func executeSendUnpair(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: attn send unpair <name|id>")
		return 2
	}
	if err := client.New("").UnpairSender(args[0]); err != nil {
		fmt.Fprintf(stderr, "send unpair: %v\n", daemonMessage(err))
		return 1
	}
	fmt.Fprintf(stdout, "Unpaired %s. Its token no longer works.\n", args[0])
	return 0
}

func printJSONLine(w io.Writer, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

func writeSendHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn send <session> --token-stdin [--file <path>]... [-m <text>] [--json]
       attn send pair --name <name> [--json]
       attn send senders [--json]
       attn send unpair <name|id>

Send files and a message to a running agent session, local or on an outpost,
as the user. Only a sender the user paired can send: pair a program once with
attn send pair, then give its token on stdin with every send.

send <session>
  attn types the message into the session's conversation as the user's, with
  an attn agent attachment command for each file that saves it where the
  agent runs. Give -m, --file, or both.
  <session>        the receiving session's id; attn session list --json names
                   each live session with its label and host
  --token-stdin    read the paired sender's token from the first line of stdin
  --file <path>    a file to send; repeat for more. Its name is kept
  -m <text>        the message
  --json           print the saved capture: id, mailbox, content,
                   created_at, and attachments with id, name, media_type
                   and bytes
  The files stay on this daemon after delivery. A failed send removes the
  files it uploaded and exits nonzero.

pair --name <name>
  ask the user to pair this program. The attn app asks the user to allow or
  deny the name and shows a 4-digit code; this command prints the same code
  and waits. When the user allows it, the command prints the token once. Keep
  the token where other programs cannot read it, such as the macOS Keychain.
  Stopping the command withdraws the request. attn asks about one program at
  a time, and refuses a request made from inside an attn session, because
  agents cannot pair. --json prints two JSON lines:
  the request (id, name, code, requested_at), then {sender, token}.

senders
  list the paired senders: name, when paired, when last used, id.

unpair <name|id>
  remove a paired sender; its token stops working at once.
`)
}
