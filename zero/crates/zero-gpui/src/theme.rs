//! Tokyo Night (night variant), the palette the maintainer asked to be inspired by.
use gpui::{Hsla, rgb};

use zero::mermaid::Palette;
use zero::model::{AgentKind, AgentState};

fn c(hex: u32) -> Hsla {
    rgb(hex).into()
}

/// The desk behind the cards, darker than Tokyo Night's editor background so cards lift off it.
pub fn desk() -> Hsla {
    c(0x111119)
}
pub fn desk_far() -> Hsla {
    c(0x0d0d13)
}
pub fn bg_dark() -> Hsla {
    c(0x16161e)
}
/// Card body and terminal background: Tokyo Night's editor background.
pub fn card() -> Hsla {
    c(0x1a1b26)
}
pub fn card_header() -> Hsla {
    c(0x1f2133)
}
pub fn bg_highlight() -> Hsla {
    c(0x292e42)
}
pub fn fg() -> Hsla {
    c(0xc0caf5)
}
pub fn fg_dark() -> Hsla {
    c(0xa9b1d6)
}
pub fn comment() -> Hsla {
    c(0x565f89)
}
pub fn gutter() -> Hsla {
    c(0x3b4261)
}
pub fn blue() -> Hsla {
    c(0x7aa2f7)
}
pub fn purple() -> Hsla {
    c(0xbb9af7)
}
pub fn yellow() -> Hsla {
    c(0xe0af68)
}
pub fn orange() -> Hsla {
    c(0xff9e64)
}
pub fn red() -> Hsla {
    c(0xf7768e)
}
pub fn teal() -> Hsla {
    c(0x1abc9c)
}
pub fn green() -> Hsla {
    c(0x9ece6a)
}
pub fn cyan() -> Hsla {
    c(0x7dcfff)
}
/// Document tiles: a softer purple than the scenario pill so the two never read as one thing.
pub fn doc() -> Hsla {
    c(0x9d7cd8)
}

/// The 16 ANSI colors as Tokyo Night paints them, indexed like the terminal palette.
pub const ANSI: [u32; 16] = [
    0x15161e, 0xf7768e, 0x9ece6a, 0xe0af68, 0x7aa2f7, 0xbb9af7, 0x7dcfff, 0xa9b1d6, 0x414868,
    0xf7768e, 0x9ece6a, 0xe0af68, 0x7aa2f7, 0xbb9af7, 0x7dcfff, 0xc0caf5,
];

pub fn ansi(index: usize) -> Hsla {
    c(ANSI[index])
}

pub fn state_color(kind: AgentKind, state: AgentState) -> Hsla {
    if kind == AgentKind::Shell {
        return teal();
    }
    if kind == AgentKind::Document {
        return doc();
    }
    match state {
        AgentState::Working => blue(),
        AgentState::WaitingInput => yellow(),
        AgentState::PendingApproval => red(),
        AgentState::Idle => comment(),
    }
}

/// An editor mode as the header shows it: the label and its color.
pub fn mode(mode: &str) -> (&'static str, Hsla) {
    match mode {
        "" | "normal" => ("NORMAL", blue()),
        m if m.starts_with("insert") => ("INSERT", green()),
        m if m.starts_with("visual") => ("VISUAL", purple()),
        m if m.starts_with("replace") => ("REPLACE", red()),
        m if m.starts_with("cmdline") => ("COMMAND", yellow()),
        m if m.starts_with("operator") => ("PENDING", orange()),
        m if m.starts_with("terminal") => ("TERMINAL", teal()),
        _ => ("OTHER", yellow()),
    }
}

/// The palette mermaid diagrams are drawn in, the same Tokyo Night surfaces as the cards.
pub fn mermaid_palette() -> Palette {
    Palette {
        background: "#1a1b26".to_string(),
        surface: "#292e42".to_string(),
        border: "#7aa2f7".to_string(),
        text: "#c0caf5".to_string(),
        line: "#a9b1d6".to_string(),
        font_family: "Helvetica Neue, Helvetica, Arial, sans-serif".to_string(),
    }
}
