//! The Neovim engine: `nvim --embed` over msgpack-rpc, its one linegrid kept as a cell frame.
use std::collections::HashMap;
use std::io::{BufReader, Write};
use std::process::{Child, ChildStdin, ChildStdout, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::thread;

use anyhow::{Context, Result};
use crossbeam_channel::{Receiver, Sender, unbounded};
use rmpv::Value;

use crate::editor::{Cell, CursorShape, Editor, EditorEvent, Engine, Frame, Key, Open, Rgb};

pub struct Neovim {
    child: Child,
    stdin: ChildStdin,
    next_id: u64,
    grid: Arc<Mutex<Grid>>,
}

impl Neovim {
    pub fn spawn(open: Open) -> Result<(Self, Receiver<EditorEvent>)> {
        let mut command = Command::new("nvim");
        command.arg("--embed");
        if open.clean {
            command.arg("--clean");
        }
        let mut child = command
            .arg(&open.path)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .context("starting nvim; is Neovim installed? (brew install neovim)")?;
        let stdin = child.stdin.take().context("nvim stdin")?;
        let stdout = child.stdout.take().context("nvim stdout")?;
        let grid = Arc::new(Mutex::new(Grid::default()));
        let (sender, receiver) = unbounded();
        let reader_grid = Arc::clone(&grid);
        thread::spawn(move || read_loop(stdout, reader_grid, sender));
        let mut nvim = Self { child, stdin, next_id: 0, grid };
        nvim.request(
            "nvim_ui_attach",
            vec![
                Value::from(open.cols),
                Value::from(open.rows),
                Value::Map(vec![
                    (Value::from("rgb"), Value::from(true)),
                    (Value::from("ext_linegrid"), Value::from(true)),
                ]),
            ],
        );
        if open.line > 1 {
            nvim.goto(open.line);
        }
        Ok((nvim, receiver))
    }

    fn request(&mut self, method: &str, params: Vec<Value>) {
        self.next_id += 1;
        let message = Value::Array(vec![
            Value::from(0),
            Value::from(self.next_id),
            Value::from(method),
            Value::Array(params),
        ]);
        let mut bytes = Vec::new();
        rmpv::encode::write_value(&mut bytes, &message).expect("msgpack encodes into memory");
        // A closed pipe means nvim is gone; the reader reports that as Exited.
        let _ = self.stdin.write_all(&bytes).and_then(|_| self.stdin.flush());
    }

    fn command(&mut self, command: &str) {
        self.request("nvim_command", vec![Value::from(command)]);
    }
}

impl Editor for Neovim {
    fn engine(&self) -> Engine {
        Engine::Neovim
    }

    fn frame(&self) -> Frame {
        self.grid.lock().unwrap_or_else(|poisoned| poisoned.into_inner()).frame()
    }

    fn input(&mut self, key: &Key) {
        if let Some(keys) = notation(key) {
            self.request("nvim_input", vec![Value::from(keys)]);
        }
    }

    fn resize(&mut self, cols: u16, rows: u16) {
        self.request("nvim_ui_try_resize", vec![Value::from(cols), Value::from(rows)]);
    }

    fn save(&mut self) {
        self.command("write");
    }

    fn save_and_quit(&mut self) {
        self.command("wq");
    }

    fn quit(&mut self) {
        self.command("qa!");
    }

    fn goto(&mut self, line: u32) {
        self.command(&format!("{line}"));
    }
}

impl Drop for Neovim {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn read_loop(stdout: ChildStdout, grid: Arc<Mutex<Grid>>, events: Sender<EditorEvent>) {
    let mut reader = BufReader::new(stdout);
    while let Ok(message) = rmpv::decode::read_value(&mut reader) {
        let Value::Array(items) = message else {
            continue;
        };
        match (items.first().and_then(Value::as_u64), items.get(1), items.get(2)) {
            (Some(2), Some(method), Some(Value::Array(batches))) if method.as_str() == Some("redraw") => {
                let flushed = grid.lock().unwrap_or_else(|poisoned| poisoned.into_inner()).apply(batches);
                if flushed && events.send(EditorEvent::Redraw).is_err() {
                    return;
                }
            }
            (Some(1), _, Some(error)) if !error.is_nil() => eprintln!("nvim request failed: {error}"),
            _ => {}
        }
    }
    let _ = events.send(EditorEvent::Exited);
}

/// Neovim's `<>` notation for a key; None for keys nvim has no name for.
pub fn notation(key: &Key) -> Option<String> {
    let mods = |shift: bool| {
        format!(
            "{}{}{}",
            if key.control { "C-" } else { "" },
            if key.alt { "M-" } else { "" },
            if shift { "S-" } else { "" }
        )
    };
    let special = match key.name.as_str() {
        "enter" => Some("CR"),
        "escape" => Some("Esc"),
        "backspace" => Some("BS"),
        "tab" => Some("Tab"),
        "space" => Some("Space"),
        "delete" => Some("Del"),
        "insert" => Some("Insert"),
        "up" => Some("Up"),
        "down" => Some("Down"),
        "left" => Some("Left"),
        "right" => Some("Right"),
        "home" => Some("Home"),
        "end" => Some("End"),
        "pageup" => Some("PageUp"),
        "pagedown" => Some("PageDown"),
        _ => None,
    };
    let function = key
        .name
        .strip_prefix('f')
        .and_then(|number| number.parse::<u8>().ok())
        .filter(|number| (1..=24).contains(number))
        .map(|number| format!("F{number}"));
    if let Some(name) = special.map(str::to_string).or(function) {
        return Some(format!("<{}{name}>", mods(key.shift)));
    }
    let mut chars = key.name.chars();
    let base = chars.next()?;
    if chars.next().is_some() {
        return None;
    }
    if key.control || key.alt {
        return Some(format!("<{}{base}>", mods(key.shift)));
    }
    let text = key.text.clone().unwrap_or_else(|| base.to_string());
    Some(if text == "<" { "<lt>".to_string() } else { text })
}

#[derive(Clone, Default)]
struct Slot {
    text: String,
    hl: u64,
}

#[derive(Clone, Copy, Default)]
struct Attr {
    fg: Option<Rgb>,
    bg: Option<Rgb>,
    reverse: bool,
    bold: bool,
    italic: bool,
    underline: bool,
    strikethrough: bool,
}

#[derive(Default)]
struct Grid {
    cols: u16,
    rows: u16,
    lines: Vec<Vec<Slot>>,
    attrs: HashMap<u64, Attr>,
    default_fg: Option<Rgb>,
    default_bg: Option<Rgb>,
    cursor: (u16, u16),
    busy: bool,
    modes: Vec<CursorShape>,
    mode_index: usize,
    mode: String,
}

impl Grid {
    /// Applies one `redraw` notification; true when it ended with a flush, the moment to repaint.
    fn apply(&mut self, batches: &[Value]) -> bool {
        let mut flushed = false;
        for batch in batches {
            let Value::Array(batch) = batch else {
                continue;
            };
            let Some(name) = batch.first().and_then(Value::as_str) else {
                continue;
            };
            match name {
                "flush" => flushed = true,
                "busy_start" => self.busy = true,
                "busy_stop" => self.busy = false,
                _ => {}
            }
            for args in &batch[1..] {
                let Value::Array(args) = args else {
                    continue;
                };
                match name {
                    "grid_resize" if grid_one(args) => self.resize(number(args, 1) as u16, number(args, 2) as u16),
                    "grid_clear" if grid_one(args) => self.clear(),
                    "grid_line" if grid_one(args) => self.line(args),
                    "grid_scroll" if grid_one(args) => self.scroll(args),
                    "grid_cursor_goto" if grid_one(args) => {
                        self.cursor = (number(args, 2) as u16, number(args, 1) as u16);
                    }
                    "hl_attr_define" => self.define(args),
                    "default_colors_set" => {
                        self.default_fg = color(args.first());
                        self.default_bg = color(args.get(1));
                    }
                    "mode_info_set" => self.mode_info(args.get(1)),
                    "mode_change" => {
                        self.mode = args.first().and_then(Value::as_str).unwrap_or("").to_string();
                        self.mode_index = number(args, 1) as usize;
                    }
                    _ => {}
                }
            }
        }
        flushed
    }

    fn resize(&mut self, cols: u16, rows: u16) {
        self.cols = cols;
        self.rows = rows;
        self.lines = vec![vec![Slot { text: " ".to_string(), hl: 0 }; cols as usize]; rows as usize];
    }

    fn clear(&mut self) {
        for line in &mut self.lines {
            for slot in line {
                slot.text = " ".to_string();
                slot.hl = 0;
            }
        }
    }

    fn line(&mut self, args: &[Value]) {
        let row = number(args, 1) as usize;
        let mut col = number(args, 2) as usize;
        let Some(Value::Array(cells)) = args.get(3) else {
            return;
        };
        let Some(line) = self.lines.get_mut(row) else {
            return;
        };
        let mut hl = 0;
        for cell in cells {
            let Value::Array(cell) = cell else {
                continue;
            };
            let text = cell.first().and_then(Value::as_str).unwrap_or("");
            if let Some(id) = cell.get(1).and_then(Value::as_u64) {
                hl = id;
            }
            let repeat = cell.get(2).and_then(Value::as_u64).unwrap_or(1);
            for _ in 0..repeat {
                if let Some(slot) = line.get_mut(col) {
                    slot.text = text.to_string();
                    slot.hl = hl;
                }
                col += 1;
            }
        }
    }

    fn scroll(&mut self, args: &[Value]) {
        let top = number(args, 1) as usize;
        let bot = (number(args, 2) as usize).min(self.lines.len());
        let left = number(args, 3) as usize;
        let right = (number(args, 4) as usize).min(self.cols as usize);
        let rows = args.get(5).and_then(Value::as_i64).unwrap_or(0);
        if left >= right {
            return;
        }
        if rows > 0 {
            let rows = rows as usize;
            for dst in top..bot.saturating_sub(rows) {
                let copy = self.lines[dst + rows][left..right].to_vec();
                self.lines[dst][left..right].clone_from_slice(&copy);
            }
        } else if rows < 0 {
            let rows = rows.unsigned_abs() as usize;
            for dst in ((top + rows).min(bot)..bot).rev() {
                let copy = self.lines[dst - rows][left..right].to_vec();
                self.lines[dst][left..right].clone_from_slice(&copy);
            }
        }
    }

    fn define(&mut self, args: &[Value]) {
        let id = number(args, 0) as u64;
        let mut attr = Attr::default();
        if let Some(Value::Map(pairs)) = args.get(1) {
            for (key, value) in pairs {
                let on = value.as_bool().unwrap_or(false);
                match key.as_str() {
                    Some("foreground") => attr.fg = color(Some(value)),
                    Some("background") => attr.bg = color(Some(value)),
                    Some("reverse") => attr.reverse = on,
                    Some("bold") => attr.bold = on,
                    Some("italic") => attr.italic = on,
                    Some("strikethrough") => attr.strikethrough = on,
                    Some("underline" | "undercurl" | "underdouble" | "underdotted" | "underdashed") => {
                        attr.underline |= on;
                    }
                    _ => {}
                }
            }
        }
        self.attrs.insert(id, attr);
    }

    fn mode_info(&mut self, value: Option<&Value>) {
        let Some(Value::Array(modes)) = value else {
            return;
        };
        self.modes = modes
            .iter()
            .map(|mode| {
                let shape = match mode {
                    Value::Map(pairs) => pairs
                        .iter()
                        .find(|(key, _)| key.as_str() == Some("cursor_shape"))
                        .and_then(|(_, value)| value.as_str()),
                    _ => None,
                };
                match shape {
                    Some("vertical") => CursorShape::Vertical,
                    Some("horizontal") => CursorShape::Horizontal,
                    _ => CursorShape::Block,
                }
            })
            .collect();
    }

    fn frame(&self) -> Frame {
        let lines = self
            .lines
            .iter()
            .map(|line| {
                line.iter()
                    .enumerate()
                    .map(|(x, slot)| {
                        let attr = self.attrs.get(&slot.hl).copied().unwrap_or_default();
                        let (mut fg, mut bg) = (attr.fg, attr.bg);
                        if attr.reverse {
                            (fg, bg) = (bg.or(self.default_bg), fg.or(self.default_fg));
                        }
                        let wide = !slot.text.is_empty()
                            && line.get(x + 1).is_some_and(|next| next.text.is_empty());
                        Cell {
                            text: slot.text.clone(),
                            fg,
                            bg,
                            bold: attr.bold,
                            italic: attr.italic,
                            underline: attr.underline,
                            strikethrough: attr.strikethrough,
                            wide,
                        }
                    })
                    .collect()
            })
            .collect();
        Frame {
            cols: self.cols,
            rows: self.rows,
            lines,
            cursor: (!self.busy).then_some(self.cursor),
            cursor_shape: self.modes.get(self.mode_index).copied().unwrap_or_default(),
            mode: self.mode.clone(),
            default_fg: self.default_fg,
            default_bg: self.default_bg,
        }
    }
}

fn grid_one(args: &[Value]) -> bool {
    number(args, 0) == 1
}

fn number(args: &[Value], index: usize) -> i64 {
    args.get(index).and_then(Value::as_i64).unwrap_or(0)
}

fn color(value: Option<&Value>) -> Option<Rgb> {
    let value = value?.as_i64()?;
    (value >= 0).then(|| Rgb::from_u32(value as u32))
}

#[cfg(test)]
mod tests {
    use std::time::{Duration, Instant};

    use super::*;

    fn key(name: &str, text: Option<&str>, shift: bool, control: bool, alt: bool) -> Key {
        Key { name: name.into(), text: text.map(str::to_string), shift, control, alt }
    }

    #[test]
    fn keys_become_nvim_notation() {
        assert_eq!(notation(&key("enter", None, false, false, false)).as_deref(), Some("<CR>"));
        assert_eq!(notation(&key("a", None, false, true, false)).as_deref(), Some("<C-a>"));
        assert_eq!(notation(&key("left", None, true, false, false)).as_deref(), Some("<S-Left>"));
        assert_eq!(notation(&key("x", Some("x"), false, false, true)).as_deref(), Some("<M-x>"));
        assert_eq!(notation(&key("f5", None, false, false, false)).as_deref(), Some("<F5>"));
        assert_eq!(notation(&key("a", Some("A"), true, false, false)).as_deref(), Some("A"));
        assert_eq!(notation(&key(",", Some("<"), true, false, false)).as_deref(), Some("<lt>"));
        assert_eq!(notation(&key("fn", None, false, false, false)), None);
    }

    fn event(name: &str, args: Vec<Value>) -> Value {
        Value::Array(vec![Value::from(name), Value::Array(args)])
    }

    #[test]
    fn redraw_events_build_the_frame() {
        let mut grid = Grid::default();
        let batches = vec![
            event("grid_resize", vec![1.into(), 4.into(), 2.into()]),
            event(
                "hl_attr_define",
                vec![
                    1.into(),
                    Value::Map(vec![("foreground".into(), 0xff0000.into()), ("bold".into(), true.into())]),
                    Value::Map(vec![]),
                    Value::Array(vec![]),
                ],
            ),
            event(
                "grid_line",
                vec![
                    1.into(),
                    0.into(),
                    0.into(),
                    Value::Array(vec![
                        Value::Array(vec!["h".into(), 1.into()]),
                        Value::Array(vec!["i".into()]),
                        Value::Array(vec![" ".into(), 0.into(), 2.into()]),
                    ]),
                    false.into(),
                ],
            ),
            event("grid_line", vec![1.into(), 1.into(), 0.into(), Value::Array(vec![Value::Array(vec!["東".into(), 0.into()]), Value::Array(vec!["".into()])]), false.into()]),
            event(
                "mode_info_set",
                vec![
                    true.into(),
                    Value::Array(vec![
                        Value::Map(vec![("cursor_shape".into(), "block".into())]),
                        Value::Map(vec![("cursor_shape".into(), "vertical".into())]),
                    ]),
                ],
            ),
            event("mode_change", vec!["insert".into(), 1.into()]),
            event("grid_cursor_goto", vec![1.into(), 0.into(), 2.into()]),
            Value::Array(vec!["flush".into()]),
        ];
        assert!(grid.apply(&batches));
        let frame = grid.frame();
        let texts: Vec<&str> = frame.lines[0].iter().map(|cell| cell.text.as_str()).collect();
        assert_eq!(texts, vec!["h", "i", " ", " "]);
        assert_eq!(frame.lines[0][0].fg, Some(Rgb(0xff, 0, 0)));
        assert!(frame.lines[0][0].bold);
        assert!(frame.lines[0][1].bold, "hl_id is sticky within one grid_line");
        assert!(!frame.lines[0][2].bold);
        assert!(frame.lines[1][0].wide);
        assert_eq!(frame.cursor, Some((2, 0)));
        assert_eq!(frame.cursor_shape, CursorShape::Vertical);
        assert_eq!(frame.mode, "insert");

        assert!(!grid.apply(&[event("grid_scroll", vec![1.into(), 0.into(), 2.into(), 0.into(), 4.into(), 1.into(), 0.into()])]));
        assert_eq!(grid.frame().lines[0][0].text, "東");
    }

    /// The whole engine against a real nvim, when one is installed: draw, insert, save, quit.
    #[test]
    fn nvim_edits_a_file_end_to_end() {
        if Command::new("nvim").arg("--version").output().is_err() {
            eprintln!("skipping: nvim is not installed");
            return;
        }
        let dir = std::env::temp_dir().join(format!("attn-zero-nvim-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("note.md");
        std::fs::write(&path, "hello zero\n").unwrap();
        let (mut editor, events) =
            Neovim::spawn(Open { path: path.clone(), line: 1, cols: 40, rows: 10, clean: true }).unwrap();
        let tripwire = Duration::from_secs(20);
        let started = Instant::now();
        let wait_for = |editor: &Neovim, ok: &dyn Fn(&Frame) -> bool| {
            while started.elapsed() < tripwire {
                match events.recv_timeout(tripwire) {
                    Ok(EditorEvent::Redraw) if ok(&editor.frame()) => return true,
                    Ok(EditorEvent::Redraw) => continue,
                    Ok(EditorEvent::Exited) => return false,
                    Err(_) => return false,
                }
            }
            false
        };
        let first_line = |frame: &Frame| -> String {
            frame.lines.first().map(|line| line.iter().map(|c| c.text.as_str()).collect::<String>()).unwrap_or_default()
        };
        assert!(wait_for(&editor, &|frame| first_line(frame).starts_with("hello zero")), "nvim never drew the file");
        editor.input(&Key::typed("i"));
        assert!(wait_for(&editor, &|frame| frame.mode == "insert"), "insert mode never arrived");
        editor.input(&Key::typed("x"));
        assert!(wait_for(&editor, &|frame| first_line(frame).starts_with("xhello zero")), "the typed character never showed");
        editor.input(&Key::named("escape"));
        editor.save_and_quit();
        let exited = loop {
            match events.recv_timeout(tripwire) {
                Ok(EditorEvent::Exited) => break true,
                Ok(_) => continue,
                Err(_) => break false,
            }
        };
        assert!(exited, "nvim did not exit after :wq");
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "xhello zero\n");
        std::fs::remove_dir_all(&dir).ok();
    }
}
