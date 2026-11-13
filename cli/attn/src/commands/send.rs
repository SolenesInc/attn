use super::support::{
    bool_flag, client, daemon_message, has_help_flag, or_dash, parse_flags, parse_interspersed,
    string_flag,
};
use super::{Run, Stop, print_compact_line, print_reply_field, print_stdout};
use crate::client::{Client, Reply, SendError};
use crate::paths::{base, os_error_text};
use attn::go::flag::{Flag, FlagDefault, FlagSet, Value};
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn_protocol::go_values::QUICK_CAPTURE_MAILBOX_SESSION;
use attn_protocol::{
    PairedSender, PairedSenderToken, QuickCaptureAttachmentDiscardMessage,
    QuickCaptureAttachmentPutMessage, QuickCaptureMailbox, QuickCaptureResult,
    QuickCaptureSendMessage, SenderListMessage, SenderPairMessage, SenderPairingRequest,
    SenderSession, SenderSessionsMessage, SenderUnpairMessage, SessionShowMessage,
    SessionShowResult,
};
use std::fmt::Write as _;
use std::fs::File;
use std::io::{self, BufRead, Read};

const CHUNK_BYTES: usize = 524_288;
const CAPTURE_FIELD: &str = "quick_capture_result";

const HELP: &str = r"usage: attn send <session> --token-stdin [--file <path>]... [-m <text>] [--json]
       attn send sessions --token-stdin [--json]
       attn send pair --name <name> [--json]
       attn send senders [--profile <name|id>] [--json]
       attn send unpair <name|id> [--profile <name|id>]

Send files and a message to a running agent session, local or on an outpost,
as the user. Only a sender the user paired can send: pair a program once with
attn send pair, then give its token on stdin with every send. A sender belongs
to the profile the app showed when the user allowed it, and sends only to that
profile's agent sessions. A shell cannot receive a send.

send <session>
  attn types the message into the session's conversation as the user's, with
  an attn agent attachment command for each file that saves it where the
  agent runs. Give -m, --file, or both.
  <session>        the receiving session's id; attn send sessions lists them
  --token-stdin    read the paired sender's token from the first line of stdin
  --file <path>    a file to send; repeat for more. Its name is kept
  -m <text>        the message
  --json           print the saved capture: id, mailbox, content,
                   created_at, and attachments with id, name, media_type
                   and bytes
  The files stay on this daemon after delivery. A failed send removes the
  files it uploaded and exits nonzero.

sessions --token-stdin
  list every live agent session of the sender's profile, the sessions it can
  send to, in one unpaged list. --json prints an array of objects with id,
  label, agent, state, host (only for a session on an outpost) and
  last_focused_at (only once the user has focused the session's terminal in
  attn). The most recent last_focused_at is the session the user last focused.

pair --name <name>
  ask the user to pair this program. The attn app asks the user to allow or
  deny the name and shows a 4-digit code; this command prints the same code
  and waits. When the user allows it, the command prints the token once. Keep
  the token where other programs cannot read it, such as the macOS Keychain.
  Stopping the command withdraws the request. attn asks about one program at
  a time, and refuses a request made from an attn session's environment.
  --json prints two JSON lines: the request (id, name, code, requested_at),
  then {sender, token}.

senders
  list a profile's paired senders: name, when paired, when last used, id.

unpair <name|id>
  remove a profile's paired sender; its token stops working at once.

  --profile <name|id> chooses the profile for senders and unpair. It defaults
  to the calling session's profile inside attn, or the only profile.

A paired sender can also clean up dictated text with attn refine; see
attn refine --help.
";

pub(super) fn usage(line: &str) -> Stop {
    eprintln!("{line}");
    Stop(2)
}

pub(super) fn failed(run: &mut Run<'_>, label: &str, error: &SendError) -> Stop {
    run.refusal.clone_from(&error.refusal);
    eprintln!("{label}: {}", daemon_message(error));
    Stop(1)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.is_empty() || has_help_flag(args) {
        print_stdout(HELP);
        return Ok(());
    }
    let rest = &args[1..];
    match args[0].as_str() {
        "pair" => {
            run.path = "send pair".to_string();
            pair(run, rest)
        }
        "senders" => {
            run.path = "send senders".to_string();
            senders(run, rest)
        }
        "unpair" => {
            run.path = "send unpair".to_string();
            unpair(run, rest)
        }
        "sessions" => {
            run.path = "send sessions".to_string();
            sessions(run, rest)
        }
        _ => send(run, args),
    }
}

pub(super) fn read_sender_token(command: &str) -> Result<String, Stop> {
    let mut line = Vec::new();
    let read = io::stdin().lock().read_until(b'\n', &mut line);
    let token = String::from_utf8_lossy(&line).trim().to_string();
    if !token.is_empty() {
        return Ok(token);
    }
    let cause = match &read {
        Err(error) => format!("read /dev/stdin: {}", os_error_text(error)),
        Ok(_) if line.ends_with(b"\n") => "<nil>".to_string(),
        Ok(_) => "EOF".to_string(),
    };
    eprintln!("{command}: no token on stdin: {cause}");
    Err(Stop(2))
}

fn send(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    const USAGE: &str =
        "usage: attn send <session> --token-stdin [--file <path>]... [-m <text>] [--json]";
    let mut flags = FlagSet::new(
        "send",
        &[
            string_flag("m"),
            bool_flag("json"),
            bool_flag("token-stdin"),
            Flag {
                name: "file",
                default: FlagDefault::Repeated,
                usage: "",
            },
        ],
    );
    let positionals = parse_interspersed(run, &mut flags, args).map_err(|_| usage(USAGE))?;
    if positionals.len() != 1 {
        return Err(usage(USAGE));
    }
    let files = match flags.value("file") {
        Value::Repeated(values) => values.clone(),
        _ => Vec::new(),
    };
    let message = flags.string("m").to_string();
    if message.trim().is_empty() && files.is_empty() {
        return Err(usage("send: give -m <text>, --file <path>, or both"));
    }
    if !flags.bool("token-stdin") {
        return Err(usage(
            "send: --token-stdin is required; pipe in the token attn send pair printed",
        ));
    }
    let token = read_sender_token("send")?;
    let capture = Capture {
        client: client(run),
        token,
        message,
        files,
    };
    let reply = capture.send(run, &positionals[0]).map_err(|message| {
        eprintln!("send: {message}");
        Stop(1)
    })?;
    if flags.bool("json") {
        return print_reply_field(&reply, &[CAPTURE_FIELD, "record"])
            .map_err(|error| failed(run, "send", &error));
    }
    let result: QuickCaptureResult = reply
        .value(CAPTURE_FIELD)
        .map_err(|error| failed(run, "send", &error))?;
    let Some(record) = result.record else {
        eprintln!("send: daemon returned no capture record");
        return Err(Stop(1));
    };
    print_stdout(&format!(
        "Sent {} file(s) and the message to session {} as capture {}.\n",
        record.attachments.len(),
        record.mailbox.session_id.unwrap_or_default(),
        record.id
    ));
    Ok(())
}

struct Capture<'a> {
    client: Client<'a>,
    token: String,
    message: String,
    files: Vec<String>,
}

impl Capture<'_> {
    fn call_daemon<M: serde::Serialize>(
        &self,
        run: &mut Run<'_>,
        message: &M,
    ) -> Result<Reply, String> {
        self.client
            .call_field(
                message,
                CAPTURE_FIELD,
                "daemon returned no quick capture receipt",
            )
            .map_err(|error| {
                run.refusal.clone_from(&error.refusal);
                daemon_message(&error)
            })
    }

    fn send(&self, run: &mut Run<'_>, session: &str) -> Result<Reply, String> {
        let mut show = SessionShowMessage::new();
        show.session_id = session.trim().to_string();
        let shown: SessionShowResult = self
            .client
            .call_field(
                &show,
                "session_show_result",
                "daemon returned no session show result",
            )
            .and_then(|reply| reply.value("session_show_result"))
            .map_err(|error| {
                run.refusal.clone_from(&error.refusal);
                format!("session {session}: {}", daemon_message(&error))
            })?;
        if shown.entry.closed_at.is_some() {
            return Err(format!("session {} is closed", shown.entry.id));
        }
        let profile_id = shown.entry.profile_id.clone();
        let capture_id = new_uuid();
        let mut attachment_ids = Vec::new();
        for path in &self.files {
            let id = new_uuid();
            attachment_ids.push(id.clone());
            if let Err(cause) = self.upload(run, &profile_id, &capture_id, &id, path) {
                self.discard(&profile_id, &capture_id, &attachment_ids);
                return Err(format!("{path}: {cause}"));
            }
        }
        let mut message = QuickCaptureSendMessage::new();
        message.profile_id = Some(profile_id.clone());
        message.sender_token = Some(self.token.clone());
        message.capture_id.clone_from(&capture_id);
        message.mailbox = QuickCaptureMailbox {
            kind: QUICK_CAPTURE_MAILBOX_SESSION.to_string(),
            session_id: Some(shown.entry.id.clone()),
            member_id: None,
        };
        message.content.clone_from(&self.message);
        message.attachment_ids.clone_from(&attachment_ids);
        self.call_daemon(run, &message).inspect_err(|_| {
            self.discard(&profile_id, &capture_id, &attachment_ids);
        })
    }

    fn discard(&self, profile_id: &str, capture_id: &str, attachment_ids: &[String]) {
        for id in attachment_ids {
            let mut message = QuickCaptureAttachmentDiscardMessage::new();
            message.profile_id = Some(profile_id.to_string());
            message.sender_token = Some(self.token.clone());
            message.capture_id = capture_id.to_string();
            message.attachment_id.clone_from(id);
            let _ = self.client.call(&message);
        }
    }

    fn upload(
        &self,
        run: &mut Run<'_>,
        profile_id: &str,
        capture_id: &str,
        id: &str,
        path: &str,
    ) -> Result<(), String> {
        let mut file =
            File::open(path).map_err(|error| format!("open {path}: {}", os_error_text(&error)))?;
        let info = file
            .metadata()
            .map_err(|error| format!("stat {path}: {}", os_error_text(&error)))?;
        if !info.is_file() {
            return Err("not a regular file".to_string());
        }
        let size = usize::try_from(info.len()).unwrap_or(usize::MAX);
        let mut chunk = vec![0_u8; CHUNK_BYTES];
        let mut offset = 0;
        loop {
            let want = CHUNK_BYTES.min(size - offset);
            let read = read_full(&mut file, &mut chunk[..want], path)?;
            if read == 0 && want > 0 {
                return Err("EOF".to_string());
            }
            let last = offset + read == size;
            let mut message = QuickCaptureAttachmentPutMessage::new();
            message.profile_id = Some(profile_id.to_string());
            message.sender_token = Some(self.token.clone());
            message.capture_id = capture_id.to_string();
            message.attachment_id = id.to_string();
            message.name = base(path).to_string();
            message.offset = i64::try_from(offset).unwrap_or(i64::MAX);
            message.data_base64 = base64(&chunk[..read]);
            message.r#final = last;
            let result: QuickCaptureResult = self
                .call_daemon(run, &message)?
                .value(CAPTURE_FIELD)
                .map_err(|error| daemon_message(&error))?;
            let expected = i64::try_from(offset + read).unwrap_or(i64::MAX);
            if result.upload.as_ref().map(|upload| upload.next_offset) != Some(expected) {
                return Err(format!(
                    "daemon upload receipt disagrees with the {read} bytes sent at offset {offset}"
                ));
            }
            offset += read;
            if last {
                return Ok(());
            }
        }
    }
}

fn read_full(file: &mut File, buffer: &mut [u8], path: &str) -> Result<usize, String> {
    let mut filled = 0;
    while filled < buffer.len() {
        match file.read(&mut buffer[filled..]) {
            Ok(0) => break,
            Ok(read) => filled += read,
            Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
            Err(_) if filled > 0 => break,
            Err(error) => return Err(format!("read {path}: {}", os_error_text(&error))),
        }
    }
    Ok(filled)
}

fn new_uuid() -> String {
    let mut bytes = [0_u8; 16];
    File::open("/dev/urandom")
        .and_then(|mut random| random.read_exact(&mut bytes))
        .expect("the kernel provides /dev/urandom");
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    let mut text = String::with_capacity(36);
    for (index, byte) in bytes.iter().enumerate() {
        if matches!(index, 4 | 6 | 8 | 10) {
            text.push('-');
        }
        let _ = write!(text, "{byte:02x}");
    }
    text
}

fn base64(data: &[u8]) -> String {
    const ALPHABET: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(data.len().div_ceil(3) * 4);
    for group in data.chunks(3) {
        let bits = group
            .iter()
            .fold(0_u32, |bits, byte| (bits << 8) | u32::from(*byte))
            << (8 * (3 - group.len()));
        for position in 0..4 {
            if position <= group.len() {
                let index = (bits >> (18 - 6 * position)) & 63;
                out.push(char::from(ALPHABET[index as usize]));
            } else {
                out.push('=');
            }
        }
    }
    out
}

fn pair(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    const USAGE: &str = "usage: attn send pair --name <name> [--json]";
    let mut flags = FlagSet::new("send pair", &[string_flag("name"), bool_flag("json")]);
    let parsed = parse_flags(run, &mut flags, args);
    let name = flags.string("name").to_string();
    if parsed.is_err() || !flags.args().is_empty() || name.trim().is_empty() {
        return Err(usage(USAGE));
    }
    let json = flags.bool("json");
    let mut message = SenderPairMessage::new();
    message.name = name;
    let mut stream = client(run)
        .open_stream(&message)
        .map_err(|error| failed(run, "send pair", &error))?;
    let first = stream
        .next_reply()
        .map_err(|error| failed(run, "send pair", &error))?;
    if !first.present("sender_pairing") {
        eprintln!("send pair: daemon returned no pairing request");
        return Err(Stop(1));
    }
    if json {
        let raw = first
            .raw_field(&["sender_pairing"])
            .map_err(|error| failed(run, "send pair", &error))?;
        print_compact_line(raw);
    } else {
        let request: SenderPairingRequest = first
            .value("sender_pairing")
            .map_err(|error| failed(run, "send pair", &error))?;
        print_stdout(&format!(
            "Waiting for you to allow {} in the attn app. Check that it shows code {}.\n",
            quote(request.name.as_bytes()),
            request.code
        ));
    }
    let second = stream
        .next_reply()
        .map_err(|error| failed(run, "send pair", &error))?;
    if !second.present("paired_sender_token") {
        eprintln!("send pair: daemon returned no sender token");
        return Err(Stop(1));
    }
    if json {
        let raw = second
            .raw_field(&["paired_sender_token"])
            .map_err(|error| failed(run, "send pair", &error))?;
        print_compact_line(raw);
        return Ok(());
    }
    let paired: PairedSenderToken = second
        .value("paired_sender_token")
        .map_err(|error| failed(run, "send pair", &error))?;
    print_stdout(&format!(
        "Paired {}. Its token, shown only now:\n{}\n",
        quote(paired.sender.name.as_bytes()),
        paired.token
    ));
    Ok(())
}

fn print_compact_list(raw: &str) {
    print_compact_line(if raw == "null" { "[]" } else { raw });
}

fn senders(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("send senders", &[bool_flag("json"), string_flag("profile")]);
    let parsed = parse_flags(run, &mut flags, args);
    if parsed.is_err() || !flags.args().is_empty() {
        return Err(usage(
            "usage: attn send senders [--profile <name|id>] [--json]",
        ));
    }
    let mut message = SenderListMessage::new();
    message.profile_id = Some(flags.string("profile").trim().to_string());
    let reply = client(run)
        .call(&message)
        .map_err(|error| failed(run, "send senders", &error))?;
    if flags.bool("json") {
        let raw = reply
            .raw_field(&["paired_senders"])
            .map_err(|error| failed(run, "send senders", &error))?;
        print_compact_list(raw);
        return Ok(());
    }
    let senders: Vec<PairedSender> = reply
        .value::<Option<Vec<PairedSender>>>("paired_senders")
        .map_err(|error| failed(run, "send senders", &error))?
        .unwrap_or_default();
    if senders.is_empty() {
        print_stdout("No paired senders. Pair one with attn send pair --name <name>.\n");
        return Ok(());
    }
    let mut table = String::from("NAME\tPAIRED\tLAST USED\tID\n");
    for sender in &senders {
        let _ = writeln!(
            table,
            "{}\t{}\t{}\t{}",
            sender.name,
            sender.created_at,
            or_dash(sender.last_used_at.as_deref().unwrap_or("")),
            sender.id
        );
    }
    print_stdout(&tabwriter::format(&table));
    Ok(())
}

fn unpair(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    const USAGE: &str = "usage: attn send unpair <name|id> [--profile <name|id>]";
    let mut flags = FlagSet::new("send unpair", &[string_flag("profile")]);
    let positionals = parse_interspersed(run, &mut flags, args).map_err(|_| usage(USAGE))?;
    if positionals.len() != 1 {
        return Err(usage(USAGE));
    }
    let mut message = SenderUnpairMessage::new();
    message.sender.clone_from(&positionals[0]);
    message.profile_id = Some(flags.string("profile").trim().to_string());
    client(run)
        .send(&message)
        .map_err(|error| failed(run, "send unpair", &error))?;
    print_stdout(&format!(
        "Unpaired {}. Its token no longer works.\n",
        positionals[0]
    ));
    Ok(())
}

fn sessions(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(
        "send sessions",
        &[bool_flag("token-stdin"), bool_flag("json")],
    );
    let parsed = parse_flags(run, &mut flags, args);
    if parsed.is_err() || !flags.args().is_empty() || !flags.bool("token-stdin") {
        return Err(usage("usage: attn send sessions --token-stdin [--json]"));
    }
    let token = read_sender_token("send sessions")?;
    let mut message = SenderSessionsMessage::new();
    message.sender_token = token;
    let reply = client(run)
        .call(&message)
        .map_err(|error| failed(run, "send sessions", &error))?;
    if flags.bool("json") {
        let raw = reply
            .raw_field(&["sender_sessions"])
            .map_err(|error| failed(run, "send sessions", &error))?;
        print_compact_list(raw);
        return Ok(());
    }
    let sessions: Vec<SenderSession> = reply
        .value::<Option<Vec<SenderSession>>>("sender_sessions")
        .map_err(|error| failed(run, "send sessions", &error))?
        .unwrap_or_default();
    if sessions.is_empty() {
        print_stdout("No live sessions in this sender's profile.\n");
        return Ok(());
    }
    let mut table = String::from("ID\tAGENT\tSTATE\tHOST\tLAST FOCUSED\tLABEL\n");
    for session in &sessions {
        let _ = writeln!(
            table,
            "{}\t{}\t{}\t{}\t{}\t{}",
            session.id,
            session.agent,
            session.state,
            or_dash(session.host.as_deref().unwrap_or("")),
            or_dash(session.last_focused_at.as_deref().unwrap_or("")),
            session.label
        );
    }
    print_stdout(&tabwriter::format(&table));
    Ok(())
}
