//! Spike 0: can a Rust client run libghostty-vt at attn's pin and decode a real
//! attach snapshot from the daemon? Throwaway code; the plot is s-p54fpa.

use std::fs;
use std::io::ErrorKind;
use std::net::TcpStream;
use std::path::PathBuf;
use std::time::{Duration, Instant};

use anyhow::{Context, Result, anyhow, bail};
use base64::Engine;
use libghostty_vt::key::{self, Action, Key, Mods, OptionAsAlt};
use libghostty_vt::render::{CellIterator, RenderState, RowIterator};
use libghostty_vt::screen::CellWide;
use libghostty_vt::snapshot::Decoder;
use libghostty_vt::terminal::Terminal;
use serde_json::{Value, json};
use tungstenite::stream::MaybeTlsStream;
use tungstenite::{Message, WebSocket};

type Socket = WebSocket<MaybeTlsStream<TcpStream>>;

const USAGE: &str = "usage:
  spike0 keys
  spike0 attach --data-dir DIR --port PORT [--session ID | --spawn --cwd DIR [--type CMD]...]
                [--cols N] [--rows N] [--listen SECS] [--out DIR] [--expect-format TAG]";

fn main() -> Result<()> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    match args.first().map(String::as_str) {
        Some("keys") => keys(),
        Some("attach") => attach(Opts::parse(&args[1..])?),
        Some("roundtrip") => roundtrip(args.get(1).map(PathBuf::from)),
        _ => {
            eprintln!("{USAGE}");
            std::process::exit(2)
        }
    }
}

// ---------------------------------------------------------------------------
// keys: the fixtures of app/src/ghostty/keyEncoder.binding.test.ts, byte for byte.

fn keys() -> Result<()> {
    let failed = std::cell::Cell::new(0usize);
    let check = |name: &str, got: Result<Vec<u8>>, want: &[u8]| match got {
        Ok(got) if got == want => println!("PASS {name}: {}", escape(&got)),
        Ok(got) => {
            failed.set(failed.get() + 1);
            println!("FAIL {name}: got {} want {}", escape(&got), escape(want));
        }
        Err(err) => {
            failed.set(failed.get() + 1);
            println!("FAIL {name}: {err}");
        }
    };

    {
        let mut t = Terminal::new(80, 24)?;
        let mut e = key::Encoder::new()?;
        check("arrow up", encode(&mut e, &t, &event(Action::Press, Key::ArrowUp, Mods::empty(), Mods::empty(), None, None)?), b"\x1b[A");
        check("arrow down", encode(&mut e, &t, &event(Action::Press, Key::ArrowDown, Mods::empty(), Mods::empty(), None, None)?), b"\x1b[B");
        t.vt_write(b"\x1b[?1h");
        check("arrow up, app cursor", encode(&mut e, &t, &event(Action::Press, Key::ArrowUp, Mods::empty(), Mods::empty(), None, None)?), b"\x1bOA");
        check("arrow down, app cursor", encode(&mut e, &t, &event(Action::Press, Key::ArrowDown, Mods::empty(), Mods::empty(), None, None)?), b"\x1bOB");
    }
    {
        let t = Terminal::new(80, 24)?;
        let mut e = key::Encoder::new()?;
        check("printable a", encode(&mut e, &t, &event(Action::Press, Key::A, Mods::empty(), Mods::empty(), Some("a"), Some('a'))?), b"a");
        check("printable shift A", encode(&mut e, &t, &event(Action::Press, Key::A, Mods::SHIFT, Mods::SHIFT, Some("A"), Some('a'))?), b"A");
    }
    {
        let mut t = Terminal::new(80, 24)?;
        let mut e = key::Encoder::new()?;
        let option_f = event(Action::Press, Key::F, Mods::ALT, Mods::empty(), None, Some('f'))?;
        check("option-f", encode(&mut e, &t, &option_f), b"\x1bf");
        t.vt_write(b"\x1b[?1036l");
        check("option-f, 1036 off", encode(&mut e, &t, &option_f), b"");
        t.vt_write(b"\x1b[?1036h");
        check("option-f, 1036 on", encode(&mut e, &t, &option_f), b"\x1bf");
    }
    {
        let mut t = Terminal::new(80, 24)?;
        let mut e = key::Encoder::new()?;
        t.vt_write(b"\x1b[>4;2m");
        check("modifyOtherKeys ctrl-shift-i", encode(&mut e, &t, &event(Action::Press, Key::I, Mods::SHIFT | Mods::CTRL, Mods::SHIFT, Some("I"), Some('i'))?), b"\x1b[27;6;73~");
    }
    {
        let mut t = Terminal::new(80, 24)?;
        let mut e = key::Encoder::new()?;
        t.vt_write(b"\x1b[>31u");
        check("kitty press", encode(&mut e, &t, &event(Action::Press, Key::A, Mods::empty(), Mods::empty(), Some("a"), Some('a'))?), b"\x1b[97;;97u");
        check("kitty repeat", encode(&mut e, &t, &event(Action::Repeat, Key::A, Mods::empty(), Mods::empty(), Some("a"), Some('a'))?), b"\x1b[97;1:2;97u");
        check("kitty release", encode(&mut e, &t, &event(Action::Release, Key::A, Mods::empty(), Mods::empty(), None, Some('a'))?), b"\x1b[97;1:3u");
        let long = "a".repeat(40);
        match encode(&mut e, &t, &event(Action::Press, Key::A, Mods::empty(), Mods::empty(), Some(long.as_str()), Some('a'))?) {
            Ok(bytes) if bytes.len() > 64 && bytes.starts_with(b"\x1b[") => println!("PASS kitty long associated text: {} bytes", bytes.len()),
            Ok(bytes) => { failed.set(failed.get() + 1); println!("FAIL kitty long associated text: {}", escape(&bytes)); }
            Err(err) => { failed.set(failed.get() + 1); println!("FAIL kitty long associated text: {err}"); }
        }
    }
    {
        let mut t = Terminal::new(80, 24)?;
        let mut e = key::Encoder::new()?;
        let numpad = event(Action::Press, Key::Numpad1, Mods::empty(), Mods::empty(), Some("1"), Some('1'))?;
        check("numpad 1", encode(&mut e, &t, &numpad), b"1");
        t.vt_write(b"\x1b[?1035l\x1b=");
        check("numpad 1, app keypad", encode(&mut e, &t, &numpad), b"\x1bOq");
    }

    if failed.get() > 0 {
        bail!("{} key fixtures failed", failed.get());
    }
    println!("all key fixtures match the web client");
    Ok(())
}

fn event(action: Action, key: Key, mods: Mods, consumed: Mods, utf8: Option<&str>, unshifted: Option<char>) -> Result<key::Event<'static>> {
    let mut ev = key::Event::new()?;
    ev.set_action(action).set_key(key).set_mods(mods).set_consumed_mods(consumed);
    if let Some(text) = utf8 {
        ev.set_utf8(Some(text));
    }
    if let Some(cp) = unshifted {
        ev.set_unshifted_codepoint(cp);
    }
    Ok(ev)
}

// Same order as the web client: terminal state first, then Option-as-Alt on top.
fn encode(e: &mut key::Encoder<'_>, t: &Terminal<'_, '_>, ev: &key::Event<'_>) -> Result<Vec<u8>> {
    e.set_options_from_terminal(t);
    e.set_macos_option_as_alt(OptionAsAlt::True);
    let mut out = Vec::new();
    e.encode_to_vec(ev, &mut out)?;
    Ok(out)
}

fn escape(bytes: &[u8]) -> String {
    let mut s = String::new();
    for &b in bytes {
        match b {
            0x1b => s.push_str("\\x1b"),
            0x20..=0x7e => s.push(b as char),
            _ => s.push_str(&format!("\\x{b:02x}")),
        }
    }
    format!("\"{s}\"")
}

// ---------------------------------------------------------------------------
// attach: hello, attach, decode, print, listen, detach.

struct Opts {
    data_dir: PathBuf,
    port: u16,
    session: Option<String>,
    spawn: bool,
    cwd: PathBuf,
    typed: Vec<String>,
    cols: i64,
    rows: i64,
    listen: u64,
    out: PathBuf,
    expect_format: Option<String>,
}

impl Opts {
    fn parse(args: &[String]) -> Result<Self> {
        let mut o = Opts {
            data_dir: PathBuf::new(),
            port: 0,
            session: None,
            spawn: false,
            cwd: std::env::current_dir()?,
            typed: Vec::new(),
            cols: 120,
            rows: 40,
            listen: 3,
            out: PathBuf::from("spike0-out"),
            expect_format: None,
        };
        let mut it = args.iter();
        while let Some(flag) = it.next() {
            let mut value = || it.next().ok_or_else(|| anyhow!("{flag} needs a value"));
            match flag.as_str() {
                "--data-dir" => o.data_dir = PathBuf::from(value()?),
                "--port" => o.port = value()?.parse()?,
                "--session" => o.session = Some(value()?.clone()),
                "--spawn" => o.spawn = true,
                "--cwd" => o.cwd = PathBuf::from(value()?),
                "--type" => o.typed.push(value()?.clone()),
                "--cols" => o.cols = value()?.parse()?,
                "--rows" => o.rows = value()?.parse()?,
                "--listen" => o.listen = value()?.parse()?,
                "--out" => o.out = PathBuf::from(value()?),
                "--expect-format" => o.expect_format = Some(value()?.clone()),
                other => bail!("unknown flag {other}\n{USAGE}"),
            }
        }
        if o.data_dir.as_os_str().is_empty() || o.port == 0 {
            bail!("--data-dir and --port are required\n{USAGE}");
        }
        if o.session.is_none() && !o.spawn {
            bail!("pick --session ID or --spawn");
        }
        Ok(o)
    }
}

fn attach(o: Opts) -> Result<()> {
    fs::create_dir_all(&o.out)?;
    let token = fs::read_to_string(o.data_dir.join("client-token"))
        .with_context(|| format!("reading {}", o.data_dir.join("client-token").display()))?;
    let (mut ws, _) = tungstenite::connect(format!("ws://127.0.0.1:{}/ws", o.port))?;
    if let MaybeTlsStream::Plain(stream) = ws.get_mut() {
        stream.set_read_timeout(Some(Duration::from_millis(250)))?;
    }
    send(&mut ws, json!({
        "cmd": "client_hello",
        "client_kind": "native",
        "client_id": format!("spike0-{}", std::process::id()),
        "version": "zero-spike0",
        "capabilities": ["workspace_sessions", "binary_pty_output", "kitty_images"],
        "client_token": token.trim(),
    }))?;

    let initial = wait_event(&mut ws, "initial_state", 10, |_| true)?;
    println!("daemon protocol {}", initial["protocol_version"]);
    let sessions = initial["sessions"].as_array().cloned().unwrap_or_default();
    for s in &sessions {
        println!("  session {} agent={} state={} workspace={} label={}", s["id"], s["agent"], s["state"], s["workspace_id"], s["label"]);
    }
    for w in initial["workspaces"].as_array().cloned().unwrap_or_default() {
        println!("  workspace {} title={} dir={}", w["id"], w["title"], w["directory"]);
    }

    let mut renderer = Renderer::new()?;
    let session_id = match &o.session {
        Some(id) => id.clone(),
        None => {
            let id = uuid::Uuid::new_v4().to_string();
            let workspace_id = uuid::Uuid::new_v4().to_string();
            send(&mut ws, json!({"cmd": "register_workspace", "id": workspace_id, "title": "spike0", "directory": o.cwd}))?;
            send(&mut ws, json!({
                "cmd": "spawn_session", "id": id, "cwd": o.cwd, "workspace_id": workspace_id,
                "agent": "shell", "cols": o.cols, "rows": o.rows, "label": "spike0",
            }))?;
            let result = wait_event(&mut ws, "spawn_result", 30, |e| e["id"] == id)?;
            if result["success"] != true {
                bail!("spawn failed: {}", result["error"]);
            }
            println!("spawned shell session {id} in workspace {workspace_id}");
            send(&mut ws, json!({"cmd": "attach_session", "id": id, "attach_policy": "fresh_spawn", "cols": o.cols, "rows": o.rows}))?;
            let fresh = wait_event(&mut ws, "attach_result", 30, |e| e["id"] == id)?;
            println!("fresh attach: success={} snapshot={} last_seq={}", fresh["success"], fresh["snapshot"].is_object(), fresh["last_seq"]);
            std::thread::sleep(Duration::from_millis(2500));
            for cmd in &o.typed {
                // Typed text goes as a bracketed paste: fish auto-pairs quotes and brackets
                // on keystrokes, which leaves a pasted-looking command unbalanced.
                send(&mut ws, json!({"cmd": "pty_input", "id": id, "data": format!("\x1b[200~{cmd}\x1b[201~")}))?;
                std::thread::sleep(Duration::from_millis(200));
                send(&mut ws, json!({"cmd": "pty_input", "id": id, "data": "\r"}))?;
                std::thread::sleep(Duration::from_millis(700));
            }
            let mut live = Terminal::new(o.cols as u16, o.rows as u16)?;
            let frames = pump(&mut ws, &id, &mut live, None, o.listen)?;
            println!("fresh attach: {} live frames, last seq {}", frames.0, frames.1);
            for line in dump(&mut renderer, &live)? {
                println!("  live| {line}");
            }
            send(&mut ws, json!({"cmd": "detach_session", "id": id}))?;
            std::thread::sleep(Duration::from_millis(300));
            id
        }
    };

    send(&mut ws, json!({"cmd": "attach_session", "id": session_id, "attach_policy": "relaunch_restore", "cols": o.cols, "rows": o.rows}))?;
    let result = wait_event(&mut ws, "attach_result", 30, |e| e["id"] == session_id)?;
    fs::write(o.out.join("attach_result.json"), serde_json::to_vec_pretty(&result)?)?;
    if result["success"] != true {
        bail!("attach failed: {}", result["error"]);
    }
    println!("attach_result: last_seq={} cols={} rows={} running={}", result["last_seq"], result["cols"], result["rows"], result["running"]);
    let snapshot = &result["snapshot"];
    if !snapshot.is_object() {
        bail!("attach_result carried no snapshot");
    }
    let format = snapshot["format"].as_str().unwrap_or("");
    match &o.expect_format {
        Some(want) if want == format => println!("snapshot format {format} matches this build"),
        Some(want) => println!("snapshot format {format} does NOT match expected {want} (the web client would refuse to decode this)"),
        None => println!("snapshot format {format}"),
    }
    let bytes = base64::engine::general_purpose::STANDARD.decode(snapshot["snapshot_b64"].as_str().unwrap_or(""))?;
    fs::write(o.out.join("snapshot.bin"), &bytes)?;
    println!("snapshot: {} bytes, {}x{}, scrollback_truncated={}, {} blocks, {} placements",
        bytes.len(), snapshot["cols"], snapshot["rows"], snapshot["scrollback_truncated"],
        snapshot["blocks"].as_array().map_or(0, Vec::len), snapshot["placements"].as_array().map_or(0, Vec::len));
    for p in snapshot["placements"].as_array().cloned().unwrap_or_default() {
        println!("  placement image={} id={} gen={} at row {} col {} visible={} virtual={} z={} {}x{}px",
            p["image_id"], p["placement_id"], p["image_generation"], p["viewport_row"], p["viewport_col"],
            p["viewport_visible"], p["virtual"], p["z"], p["pixel_width"], p["pixel_height"]);
    }

    let started = Instant::now();
    let decoder = Decoder::new_buf(&bytes).context("Decoder::new_buf")?;
    // The web client does ready() then pages history lazily; mirror that.
    let mut inc = decoder.ready().context("Decoder::ready")?;
    println!("ready in {:?}", started.elapsed());
    let (mut pages, mut prepended) = (0usize, 0usize);
    loop {
        match inc.next() {
            Ok(Some(progress)) => {
                pages += 1;
                prepended += progress.rows()?;
            }
            Ok(None) => break,
            Err(err) => {
                println!("history page {} failed: {err} ({pages} pages, {prepended} rows restored before it)", pages + 1);
                break;
            }
        }
    }
    println!("history: {pages} pages, {prepended} rows");
    let mut term = inc.into_terminal();
    println!("decoded in {:?}: {}x{}, scrollback rows {}, cursor ({}, {}), screen {:?}, title {:?}",
        started.elapsed(), term.cols()?, term.rows()?, term.scrollback_rows()?, term.cursor_x()?, term.cursor_y()?,
        term.active_screen()?, term.title()?);
    let grid = dump(&mut renderer, &term)?;
    fs::write(o.out.join("grid.txt"), grid.join("\n") + "\n")?;
    let base: Vec<String> = grid.iter().map(|l| base_codepoints(l)).collect();
    fs::write(o.out.join("grid-base.txt"), base.join("\n") + "\n")?;
    for line in &grid {
        println!("  snap| {line}");
    }

    let last_seq = result["last_seq"].as_u64().map(|s| s as u32);
    let (frames, seq) = pump(&mut ws, &session_id, &mut term, last_seq, o.listen)?;
    println!("live after snapshot: {frames} frames, last seq {seq}");
    if frames > 0 {
        let grid = dump(&mut renderer, &term)?;
        fs::write(o.out.join("grid-after-live.txt"), grid.join("\n") + "\n")?;
        for line in &grid {
            println!("  live| {line}");
        }
    }
    send(&mut ws, json!({"cmd": "detach_session", "id": session_id}))?;
    println!("wrote {}", o.out.display());
    Ok(())
}

fn send(ws: &mut Socket, msg: Value) -> Result<()> {
    ws.send(Message::Text(msg.to_string().into()))?;
    Ok(())
}

// One read with the socket's timeout; Ok(None) when nothing arrived.
fn read(ws: &mut Socket) -> Result<Option<Message>> {
    match ws.read() {
        Ok(msg) => Ok(Some(msg)),
        Err(tungstenite::Error::Io(err)) if matches!(err.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut) => Ok(None),
        Err(err) => Err(err.into()),
    }
}

fn wait_event(ws: &mut Socket, event: &str, secs: u64, matches: impl Fn(&Value) -> bool) -> Result<Value> {
    let deadline = Instant::now() + Duration::from_secs(secs);
    while Instant::now() < deadline {
        let Some(msg) = read(ws)? else { continue };
        if let Message::Text(text) = msg {
            let value: Value = serde_json::from_str(text.as_str())?;
            if value["event"] == "error" || value["ok"] == false {
                println!("daemon error: {value}");
            } else if value["event"] != event && std::env::var_os("SPIKE0_TRACE").is_some() {
                let text = value.to_string();
                println!("  <- {}", &text[..text.len().min(200)]);
            }
            if value["event"] == event && matches(&value) {
                return Ok(value);
            }
        }
    }
    bail!("no {event} within {secs}s")
}

// Feeds this session's binary output frames into the terminal for `secs`, deduping on seq.
fn pump(ws: &mut Socket, session_id: &str, term: &mut Terminal<'static, '_>, mut last_seq: Option<u32>, secs: u64) -> Result<(usize, u32)> {
    let deadline = Instant::now() + Duration::from_secs(secs);
    let mut frames = 0;
    while Instant::now() < deadline {
        let Some(msg) = read(ws)? else { continue };
        let Message::Binary(buf) = msg else { continue };
        if buf.len() < 7 || buf[0] != 0x01 {
            continue;
        }
        let id_len = buf[1] as usize;
        let id = std::str::from_utf8(&buf[2..2 + id_len])?;
        if id != session_id {
            continue;
        }
        let seq = u32::from_be_bytes(buf[2 + id_len..6 + id_len].try_into()?);
        if last_seq.is_some_and(|last| seq <= last) {
            continue;
        }
        last_seq = Some(seq);
        if let Some(path) = std::env::var_os("SPIKE0_FRAMES") {
            use std::io::Write;
            let mut f = fs::OpenOptions::new().append(true).create(true).open(path)?;
            writeln!(f, "seq {seq}: {}", escape(&buf[6 + id_len..]))?;
        }
        term.vt_write(&buf[6 + id_len..]);
        frames += 1;
    }
    Ok((frames, last_seq.unwrap_or(0)))
}

struct Renderer {
    state: RenderState<'static>,
    rows: RowIterator<'static>,
    cells: CellIterator<'static>,
}

impl Renderer {
    fn new() -> Result<Self> {
        Ok(Self { state: RenderState::new()?, rows: RowIterator::new()?, cells: CellIterator::new()?, })
    }
}

// The visible grid as text rows, trailing blanks trimmed; wide spacers are skipped.
fn dump(r: &mut Renderer, term: &Terminal<'static, '_>) -> Result<Vec<String>> {
    let snapshot = r.state.update(term)?;
    let mut lines = Vec::with_capacity(snapshot.rows()? as usize);
    let mut text = String::new();
    let mut rows = r.rows.update(&snapshot)?;
    while let Some(row) = rows.next() {
        let mut line = String::new();
        let mut cells = r.cells.update(row)?;
        while let Some(cell) = cells.next() {
            if matches!(cell.raw_cell()?.wide()?, CellWide::SpacerTail | CellWide::SpacerHead) {
                continue;
            }
            if cell.graphemes_len()? == 0 {
                line.push(' ');
            } else {
                text.clear();
                cell.graphemes_utf8(&mut text)?;
                line.push_str(&text);
            }
        }
        lines.push(line.trim_end().to_string());
    }
    Ok(lines)
}

// The web decoder exposes one codepoint per cell; strip combining marks and
// variation selectors so both sides compare the same thing.
fn base_codepoints(line: &str) -> String {
    line.chars()
        .filter(|c| !matches!(*c as u32, 0x0300..=0x036f | 0xfe00..=0xfe0f | 0x200d | 0x1f3fb..=0x1f3ff))
        .collect()
}

// Encode a terminal here and decode it here; with a path, decode that file instead.
fn roundtrip(path: Option<PathBuf>) -> Result<()> {
    let bytes = match path {
        Some(path) => fs::read(&path).with_context(|| format!("reading {}", path.display()))?,
        None => {
            let mut term = Terminal::new(40, 6)?;
            term.vt_write(b"hello \x1b[1;31mred\x1b[0m \xe6\xbc\xa2\xe5\xad\x97 \xf0\x9f\x8e\x89\r\nline two\r\n");
            let mut buf = Vec::new();
            term.encode_snapshot(&mut buf).context("encode_snapshot")?;
            println!("encoded {} bytes", buf.len());
            buf
        }
    };
    println!("first bytes: {:02x?}", &bytes[..bytes.len().min(24)]);
    let decoder = Decoder::new_buf(&bytes).context("Decoder::new_buf")?;
    println!("max continuation bytes {:?}", decoder.max_continuation_bytes());
    let mut inc = decoder.ready().context("Decoder::ready")?;
    let mut pages = 0;
    while let Some(_progress) = inc.next().context("Decoder::next")? {
        pages += 1;
    }
    let term = inc.into_terminal();
    println!("decoded: {} history pages, {}x{}", pages, term.cols()?, term.rows()?);
    let mut renderer = Renderer::new()?;
    for line in dump(&mut renderer, &term)? {
        println!("  rt| {line}");
    }
    Ok(())
}
