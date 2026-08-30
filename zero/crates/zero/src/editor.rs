//! The editor adapter: what the client asks of an editor and what it gets back, with no engine
//! leaking through. Neovim (`crate::nvim`) is the first engine; others slot in behind this seam.
use std::path::PathBuf;

use anyhow::Result;
use crossbeam_channel::Receiver;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Engine {
    Neovim,
}

impl Engine {
    pub fn label(self) -> &'static str {
        match self {
            Self::Neovim => "neovim",
        }
    }
}

/// A keystroke the way the client sees it: the key's name, the text it types, the held modifiers.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct Key {
    pub name: String,
    pub text: Option<String>,
    pub shift: bool,
    pub control: bool,
    pub alt: bool,
}

impl Key {
    pub fn named(name: &str) -> Self {
        Self { name: name.to_string(), ..Self::default() }
    }

    pub fn typed(text: &str) -> Self {
        Self { name: text.to_string(), text: Some(text.to_string()), ..Self::default() }
    }
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct Rgb(pub u8, pub u8, pub u8);

impl Rgb {
    pub fn from_u32(value: u32) -> Self {
        Self((value >> 16) as u8, (value >> 8) as u8, value as u8)
    }
}

/// One screen cell; `text` is empty for the right half of a wide character, `None` colors are the engine's defaults.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct Cell {
    pub text: String,
    pub fg: Option<Rgb>,
    pub bg: Option<Rgb>,
    pub bold: bool,
    pub italic: bool,
    pub underline: bool,
    pub strikethrough: bool,
    pub wide: bool,
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum CursorShape {
    #[default]
    Block,
    Vertical,
    Horizontal,
}

#[derive(Clone, Debug, Default)]
pub struct Frame {
    pub cols: u16,
    pub rows: u16,
    pub lines: Vec<Vec<Cell>>,
    /// Column and row; absent while the engine is busy.
    pub cursor: Option<(u16, u16)>,
    pub cursor_shape: CursorShape,
    pub mode: String,
    pub default_fg: Option<Rgb>,
    pub default_bg: Option<Rgb>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum EditorEvent {
    Redraw,
    Exited,
}

pub struct Open {
    pub path: PathBuf,
    pub line: u32,
    pub cols: u16,
    pub rows: u16,
    /// Start without the user's configuration; tests set it so their outcome does not depend on the machine.
    pub clean: bool,
}

pub trait Editor {
    fn engine(&self) -> Engine;
    fn frame(&self) -> Frame;
    fn input(&mut self, key: &Key);
    fn resize(&mut self, cols: u16, rows: u16);
    fn save(&mut self);
    fn save_and_quit(&mut self);
    fn quit(&mut self);
    fn goto(&mut self, line: u32);
}

pub fn spawn(engine: Engine, open: Open) -> Result<(Box<dyn Editor>, Receiver<EditorEvent>)> {
    match engine {
        Engine::Neovim => {
            let (editor, events) = crate::nvim::Neovim::spawn(open)?;
            Ok((Box::new(editor), events))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A second engine that records what crosses the boundary: it compiles against nothing Neovim-shaped.
    #[derive(Default)]
    struct Recorder {
        log: Vec<String>,
    }

    impl Editor for Recorder {
        fn engine(&self) -> Engine {
            Engine::Neovim
        }
        fn frame(&self) -> Frame {
            Frame::default()
        }
        fn input(&mut self, key: &Key) {
            self.log.push(format!("input {}", key.name));
        }
        fn resize(&mut self, cols: u16, rows: u16) {
            self.log.push(format!("resize {cols}x{rows}"));
        }
        fn save(&mut self) {
            self.log.push("save".into());
        }
        fn save_and_quit(&mut self) {
            self.log.push("save_and_quit".into());
        }
        fn quit(&mut self) {
            self.log.push("quit".into());
        }
        fn goto(&mut self, line: u32) {
            self.log.push(format!("goto {line}"));
        }
    }

    #[test]
    fn the_client_drives_any_engine_through_the_trait_object() {
        let mut editor: Box<dyn Editor> = Box::new(Recorder::default());
        editor.resize(80, 24);
        editor.goto(7);
        editor.input(&Key::typed("i"));
        editor.input(&Key::named("escape"));
        editor.save_and_quit();
        assert_eq!(editor.frame().lines.len(), 0);
        assert_eq!(editor.engine().label(), "neovim");
    }
}
