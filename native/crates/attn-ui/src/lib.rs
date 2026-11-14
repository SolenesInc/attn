use std::sync::OnceLock;

use rune::color::hex;
use rune::{Color, Shadow};

/// attn's colors, from the Tauri app's CSS variables (`app/src/App.css`). Each field names
/// the variable it carries: `bg_app` is `--color-bg-app`.
#[derive(Clone, Copy, Debug, Hash)]
pub struct Tokens {
    pub dark: bool,
    pub bg_app: Color,
    pub bg_panel: Color,
    pub bg_elevated: Color,
    pub bg_selected: Color,
    pub bg_input: Color,
    pub bg_tile: Color,
    pub bg_button: Color,
    pub bg_overlay: Color,
    pub border: Color,
    pub border_hover: Color,
    pub border_focus: Color,
    pub border_subtle: Color,
    pub text_primary: Color,
    pub text_secondary: Color,
    pub text_tertiary: Color,
    pub text_muted: Color,
    pub text_dimmed: Color,
    pub accent: Color,
    pub brand: Color,
    pub error: Color,
    pub warning: Color,
    pub success: Color,
    pub merged: Color,
}

/// How the user chose the window's look: the daemon setting `theme`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum Appearance {
    #[default]
    Dark,
    Light,
    System,
}

impl Appearance {
    /// The `theme` setting's value; anything else is the default, dark.
    pub fn from_setting(value: Option<&str>) -> Appearance {
        match value {
            Some("light") => Appearance::Light,
            Some("system") => Appearance::System,
            _ => Appearance::Dark,
        }
    }

    pub fn is_dark(self, system_dark: bool) -> bool {
        match self {
            Appearance::Dark => true,
            Appearance::Light => false,
            Appearance::System => system_dark,
        }
    }
}

fn c(value: &str) -> Color {
    hex(value).unwrap_or_else(|| panic!("token {value:?} is not a hex color"))
}

pub fn tokens(dark: bool) -> Tokens {
    let (accent, brand) = (c("#5b8cff"), c("#ff6b35"));
    let (error, warning, success, merged) =
        (c("#ef4444"), c("#f59e0b"), c("#3fb950"), c("#a371f7"));
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
        }
    }
}

/// rune-kit's tokens, painted with attn's colors, for the kit's components. Each appearance's
/// kit is made once: every button of every view reads it.
pub fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    static DARK: OnceLock<rune_kit::Kit> = OnceLock::new();
    static LIGHT: OnceLock<rune_kit::Kit> = OnceLock::new();
    let once = if t.dark { &DARK } else { &LIGHT };
    once.get_or_init(|| painted(t))
}

fn painted(t: &Tokens) -> rune_kit::Kit {
    let mut kit = rune_kit::Kit::neutral(t.dark);
    let p = &mut kit.c;
    p.page = t.bg_app;
    p.bg_base = t.bg_panel;
    p.bg_deep = t.bg_app;
    p.layer_01 = t.bg_elevated;
    p.surface_raised = t.bg_elevated;
    p.surface_raised_hover = t.bg_selected;
    p.bg_button_neutral = t.bg_button;
    p.layer_04 = t.border_hover;
    p.bg_accent = t.accent;
    p.text_base = t.text_primary;
    p.text_muted = t.text_secondary;
    p.text_faint = t.text_tertiary;
    p.text_accent = t.accent;
    p.icon_base = t.text_secondary;
    p.icon_muted = t.text_tertiary;
    p.icon_faint = t.text_muted;
    p.border_muted = t.border_subtle;
    p.border_base = t.border;
    p.std_border = t.border;
    p.border_strong = t.border_hover;
    p.border_focus = t.border_focus;
    p.hover = t.bg_elevated;
    p.pressed = t.bg_selected;
    p.scrim = t.bg_overlay;
    kit.elevation.button_neutral = Vec::new();
    kit.elevation = std::mem::take(&mut kit.elevation).with_input(vec![input_ring(t)]);
    kit.elevation.floating = vec![popover_shadow()];
    kit.elevation.md = vec![popover_shadow()];
    kit.elevation.overlay = vec![popover_shadow()];
    kit
}

/// The Tauri app's field outline, `border: 1px solid var(--color-border)`.
fn input_ring(t: &Tokens) -> Shadow {
    Shadow {
        spread: 1.0,
        color: t.border,
        ..Shadow::default()
    }
}

/// The Tauri app's popover shadow, `0 16px 36px rgba(0, 0, 0, 0.42)` in both appearances.
fn popover_shadow() -> Shadow {
    Shadow {
        y: 16.0,
        blur: 36.0,
        color: Color::BLACK.alpha(0.42),
        ..Shadow::default()
    }
}

/// The faces attn draws its text in: Inter for the interface, and Symbols Nerd Font Mono for
/// the icon glyphs a terminal shows. Rune's own Inter goes once an app registers fonts, so
/// attn carries it.
pub const FONTS: &[&[u8]] = &[
    include_bytes!("../fonts/Inter-Regular.ttf"),
    include_bytes!("../fonts/Inter-Medium.ttf"),
    include_bytes!("../fonts/Inter-SemiBold.ttf"),
    include_bytes!("../../../../app/public/fonts/SymbolsNerdFontMono-Regular.ttf"),
];

/// The sans and mono families, as `AppSpec::families` takes them. Mono is the Tauri app's
/// terminal chain: the installed Iosevka, then Menlo and the other named monospaces, then
/// whatever monospace the system has (Rune's `ui-monospace`, which loads its file, where the
/// Tauri app's `monospace` needs none), and the bundled symbols last for icons no other face
/// has.
pub const FAMILIES: (&str, &str) = (
    "Inter",
    "Iosevka, Menlo, Monaco, DejaVu Sans Mono, Courier New, ui-monospace, Symbols Nerd Font Mono",
);

mod glyph_button;
pub mod icon;
pub use glyph_button::GlyphButton;
mod stories;
pub use stories::{STORIES, Showcase, playground};
