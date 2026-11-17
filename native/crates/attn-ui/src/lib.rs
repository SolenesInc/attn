use std::sync::RwLock;

use rune::{Color, Shadow};

pub mod colors;
pub mod type_;

/// The interface's colors in one appearance, named by meaning. The surfaces, borders, text and
/// status colors name the Tauri app's CSS variables they carry: `bg_app` is `--color-bg-app`.
#[derive(Clone, Copy, Debug, Hash, PartialEq)]
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
    /// Text and icons on an `accent` fill.
    pub on_accent: Color,
    /// Text on a `brand` fill.
    pub on_brand: Color,
    /// Shadows and scrims, at the opacity each draws with.
    pub shadow: Color,
    /// A light edge over a raised surface.
    pub highlight: Color,
    pub state_idle: Color,
    pub state_working: Color,
    pub state_waiting: Color,
    pub state_approval: Color,
    pub state_scheduled: Color,
    pub state_unknown: Color,
    /// Something that is not running, such as a palette row's idle agent.
    pub inactive: Color,
    /// The wash over what a find or filter matched.
    pub find: Color,
    /// What the person marked to comment on.
    pub annotation: Color,
    /// A turn that settles on its own: its mark, and the chip's ink while it counts down and
    /// once it paused or was kept.
    pub settle: Color,
    pub settle_ink: Color,
    pub settle_ink_quiet: Color,
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

/// rune-kit's tokens, painted with `t`, for the kit's components. Each set of tokens' kit is
/// made once, so there are at most two per color scheme: every button of every view reads it.
pub fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    static KITS: RwLock<Vec<(Tokens, &'static rune_kit::Kit)>> = RwLock::new(Vec::new());
    let made = |kits: &[(Tokens, &'static rune_kit::Kit)]| {
        kits.iter()
            .find(|(tokens, _)| tokens == t)
            .map(|&(_, kit)| kit)
    };
    if let Some(kit) = made(&KITS.read().unwrap_or_else(|e| e.into_inner())) {
        return kit;
    }
    let mut kits = KITS.write().unwrap_or_else(|e| e.into_inner());
    if let Some(kit) = made(&kits) {
        return kit;
    }
    let kit: &'static rune_kit::Kit = Box::leak(Box::new(painted(t)));
    kits.push((*t, kit));
    kit
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
    p.bg_icon_button_contrast = t.brand;
    p.submit_fg = brand_ink(t);
    p.submit_icon = brand_ink(t);
    p.text_base = t.text_primary;
    p.text_muted = t.text_secondary;
    p.text_faint = t.text_tertiary;
    p.text_weak = t.text_tertiary;
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
    kit.elevation.floating = vec![popover_shadow(t)];
    kit.elevation.md = vec![popover_shadow(t)];
    kit.elevation.overlay = vec![popover_shadow(t)];
    kit
}

/// The Tauri app's brand-tinted control: a `brand` edge at 50% over a 12% `brand` fill.
pub fn brand_tinted(e: rune::El, t: &Tokens) -> rune::El {
    e.fill(t.brand.alpha(0.12)).border(1.0, t.brand.alpha(0.5))
}

/// The Tauri app's ink on a brand fill, `color-mix(in srgb, var(--color-brand) 12%, black)`.
pub fn brand_ink(t: &Tokens) -> Color {
    colors::mix_srgb(t.brand, t.shadow, 0.88)
}

/// The Tauri app's ink on a warning fill, `color-mix(in srgb, var(--color-warning) 10%, black)`.
pub fn warning_ink(t: &Tokens) -> Color {
    colors::mix_srgb(t.warning, t.shadow, 0.9)
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
fn popover_shadow(t: &Tokens) -> Shadow {
    Shadow {
        y: 16.0,
        blur: 36.0,
        color: t.shadow.alpha(0.42),
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
