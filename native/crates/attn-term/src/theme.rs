use crate::grid::Rgb;

/// A terminal's default colors: the Tauri app's `DARK_TERMINAL_THEME` and
/// `LIGHT_TERMINAL_THEME`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Theme {
    pub fg: Rgb,
    pub bg: Rgb,
    pub cursor: Rgb,
    pub ansi: [Rgb; 16],
}

const fn rgb(hex: u32) -> Rgb {
    [(hex >> 16) as u8, (hex >> 8) as u8, hex as u8]
}

pub const DARK: Theme = Theme {
    fg: rgb(0xd4d4d4),
    bg: rgb(0x1e1e1e),
    cursor: rgb(0xd4d4d4),
    ansi: [
        rgb(0x000000),
        rgb(0xcd3131),
        rgb(0x0dbc79),
        rgb(0xe5e510),
        rgb(0x2472c8),
        rgb(0xbc3fbc),
        rgb(0x11a8cd),
        rgb(0xe5e5e5),
        rgb(0x666666),
        rgb(0xf14c4c),
        rgb(0x23d18b),
        rgb(0xf5f543),
        rgb(0x3b8eea),
        rgb(0xd670d6),
        rgb(0x29b8db),
        rgb(0xffffff),
    ],
};

pub const LIGHT: Theme = Theme {
    fg: rgb(0x3b3b3b),
    bg: rgb(0xffffff),
    cursor: rgb(0x3b3b3b),
    ansi: [
        rgb(0x000000),
        rgb(0xcd3131),
        rgb(0x00bc00),
        rgb(0x949800),
        rgb(0x0451a5),
        rgb(0xbc05bc),
        rgb(0x0598bc),
        rgb(0x555555),
        rgb(0x666666),
        rgb(0xcd3131),
        rgb(0x14ce14),
        rgb(0xb5ba00),
        rgb(0x0451a5),
        rgb(0xbc05bc),
        rgb(0x0598bc),
        rgb(0xa5a5a5),
    ],
};

impl Default for Theme {
    fn default() -> Theme {
        DARK
    }
}

impl Theme {
    pub fn of(dark: bool) -> Theme {
        if dark { DARK } else { LIGHT }
    }

    /// `#rrggbb`, as `set_terminal_theme` carries a color.
    pub fn hex(color: Rgb) -> String {
        format!("#{:02x}{:02x}{:02x}", color[0], color[1], color[2])
    }
}
