//! attn's own colors: the Tauri app's CSS variables (`app/src/App.css`) and its terminal themes
//! (`DARK_TERMINAL_THEME` and `LIGHT_TERMINAL_THEME`).

use rune::Color;

use super::{ColorScheme, Colors, Rgb, TerminalColors, c, rgb};
use crate::Tokens;

const FIND: Rgb = [0xf5, 0xc5, 0x42];
const ANNOTATION: Rgb = [0xa7, 0x8b, 0xfa];
const BLOCK: Rgb = [0x4d, 0x9d, 0xe0];
const SEED: Rgb = [0x3b, 0x82, 0xf6];

pub(super) fn scheme() -> ColorScheme {
    ColorScheme {
        id: "attn",
        name: "attn",
        dark: Colors {
            ui: tokens(true),
            terminal: DARK,
        },
        light: Colors {
            ui: tokens(false),
            terminal: LIGHT,
        },
    }
}

fn tokens(dark: bool) -> Tokens {
    let (accent, brand) = (c("#5b8cff"), c("#ff6b35"));
    let (error, warning, success, merged) =
        (c("#ef4444"), c("#f59e0b"), c("#3fb950"), c("#a371f7"));
    let (on_accent, on_brand, shadow, highlight) =
        (c("#0d0d0f"), Color::WHITE, Color::BLACK, Color::WHITE);
    let (state_idle, state_working, state_waiting) = (c("#6b7280"), c("#22c55e"), c("#f59e0b"));
    let (state_approval, state_scheduled, state_unknown) =
        (c("#eab308"), c("#0ea5e9"), c("#a855f7"));
    let inactive = c("#555555");
    let (find, annotation) = (rgb(FIND), rgb(ANNOTATION));
    let (settle, settle_ink, settle_ink_quiet) = (rgb(ANNOTATION), c("#e2d9fd"), c("#c8bcf0"));
    if dark {
        Tokens {
            dark,
            bg_app: c("#0d0d0f"),
            bg_panel: c("#111113"),
            bg_elevated: c("#1a1a1d"),
            bg_selected: c("#38383f"),
            bg_input: c("#0d0d0e"),
            bg_tile: c("#1e1e1e"),
            bg_button: c("#252528"),
            bg_overlay: Color::BLACK.alpha(0.6),
            border: c("#2a2a2d"),
            border_hover: c("#3a3a3d"),
            border_focus: c("#4a4a4d"),
            border_subtle: c("#1f1f22"),
            text_primary: c("#e8e8e8"),
            text_secondary: c("#bbb"),
            text_tertiary: c("#888"),
            text_muted: c("#666"),
            text_dimmed: c("#555"),
            accent,
            brand,
            error,
            warning,
            success,
            merged,
            on_accent,
            on_brand,
            shadow,
            highlight,
            state_idle,
            state_working,
            state_waiting,
            state_approval,
            state_scheduled,
            state_unknown,
            inactive,
            find,
            annotation,
            settle,
            settle_ink,
            settle_ink_quiet,
        }
    } else {
        Tokens {
            dark,
            bg_app: c("#f5f5f7"),
            bg_panel: c("#ffffff"),
            bg_elevated: c("#f0f0f2"),
            bg_selected: c("#c9c9d4"),
            bg_input: c("#ffffff"),
            bg_tile: c("#ffffff"),
            bg_button: c("#e4e4e7"),
            bg_overlay: Color::BLACK.alpha(0.3),
            border: c("#d4d4d8"),
            border_hover: c("#a1a1aa"),
            border_focus: c("#71717a"),
            border_subtle: c("#e4e4e7"),
            text_primary: c("#18181b"),
            text_secondary: c("#52525b"),
            text_tertiary: c("#71717a"),
            text_muted: c("#a1a1aa"),
            text_dimmed: c("#c4c4ca"),
            accent,
            brand,
            error,
            warning,
            success,
            merged,
            on_accent,
            on_brand,
            shadow,
            highlight,
            state_idle,
            state_working,
            state_waiting,
            state_approval,
            state_scheduled,
            state_unknown,
            inactive,
            find,
            annotation,
            settle,
            settle_ink,
            settle_ink_quiet,
        }
    }
}

const fn hex(value: u32) -> Rgb {
    [(value >> 16) as u8, (value >> 8) as u8, value as u8]
}

const DARK: TerminalColors = TerminalColors {
    fg: hex(0xd4d4d4),
    bg: hex(0x1e1e1e),
    cursor: hex(0xd4d4d4),
    selection: hex(0x264f78),
    find: FIND,
    annotation: ANNOTATION,
    block: BLOCK,
    seed: SEED,
    ansi: [
        hex(0x000000),
        hex(0xcd3131),
        hex(0x0dbc79),
        hex(0xe5e510),
        hex(0x2472c8),
        hex(0xbc3fbc),
        hex(0x11a8cd),
        hex(0xe5e5e5),
        hex(0x666666),
        hex(0xf14c4c),
        hex(0x23d18b),
        hex(0xf5f543),
        hex(0x3b8eea),
        hex(0xd670d6),
        hex(0x29b8db),
        hex(0xffffff),
    ],
};

const LIGHT: TerminalColors = TerminalColors {
    fg: hex(0x3b3b3b),
    bg: hex(0xffffff),
    cursor: hex(0x3b3b3b),
    selection: hex(0xadd6ff),
    find: FIND,
    annotation: ANNOTATION,
    block: BLOCK,
    seed: SEED,
    ansi: [
        hex(0x000000),
        hex(0xcd3131),
        hex(0x00bc00),
        hex(0x949800),
        hex(0x0451a5),
        hex(0xbc05bc),
        hex(0x0598bc),
        hex(0x555555),
        hex(0x666666),
        hex(0xcd3131),
        hex(0x14ce14),
        hex(0xb5ba00),
        hex(0x0451a5),
        hex(0xbc05bc),
        hex(0x0598bc),
        hex(0xa5a5a5),
    ],
};
