use rune::theme::{srgb_decode, srgb_encode};
use rune::{Color, El};

use crate::Tokens;

/// How far hover and pressed stand from rest, as contrast against the surface they cover: the
/// steps a list row takes, the text colour at 8% and then 12% more on the panel.
const STEPS: Steps = Steps {
    dark: (1.20, 1.66),
    light: (1.17, 1.50),
};

struct Steps {
    dark: (f32, f32),
    light: (f32, f32),
}

/// What a pointer lays over a surface of colour `fill`: whichever of the text colour and the
/// app's background stands further from it, just strong enough to stand one hover step from rest.
pub fn hover_layer(fill: Color, t: &Tokens) -> Color {
    let (hover, _) = steps(t);
    let ink = away_from(fill, t);
    ink.alpha(alpha_for(srgb(fill), ink, hover))
}

/// What lies over the hovered surface while it is held down, one pressed step from rest.
pub fn pressed_layer(fill: Color, t: &Tokens) -> Color {
    let (hover, pressed) = steps(t);
    let ink = away_from(fill, t);
    let hovered = over(srgb(ink), alpha_for(srgb(fill), ink, hover), srgb(fill));
    let alpha = alpha_for_from(hovered, srgb(fill), ink, pressed);
    ink.alpha(alpha)
}

/// Pressed alone, for a part of a row whose hover the row draws, over a surface of colour `fill`.
pub(crate) fn pressed(e: El, fill: Color, t: &Tokens) -> El {
    e.press(pressed_layer(fill, t))
}

/// Hover and pressed over a surface of colour `fill`.
pub(crate) fn pressable(e: El, fill: Color, t: &Tokens) -> El {
    e.hover(hover_layer(fill, t)).press(pressed_layer(fill, t))
}

fn steps(t: &Tokens) -> (f32, f32) {
    if dark(t) { STEPS.dark } else { STEPS.light }
}

fn dark(t: &Tokens) -> bool {
    t.bg_app.luminance() < t.text_primary.luminance()
}

fn away_from(fill: Color, t: &Tokens) -> Color {
    if !dark(t) {
        return darker_from(fill, t);
    }
    let distance = |ink: Color| (ink.luminance() - fill.luminance()).abs();
    if distance(t.text_primary) >= distance(t.bg_app) {
        t.text_primary
    } else {
        t.bg_app
    }
}

fn darker_from(fill: Color, t: &Tokens) -> Color {
    let reaches = |ink: Color| {
        ink.luminance() < fill.luminance() && contrast(srgb(ink), srgb(fill)) >= STEPS.light.1
    };
    if reaches(t.text_primary) {
        t.text_primary
    } else {
        t.shadow
    }
}

fn alpha_for(fill: [f32; 3], ink: Color, contrast: f32) -> f32 {
    alpha_for_from(fill, fill, ink, contrast)
}

/// The least alpha of `ink` over `below` that stands `target` from `rest`, as the frame blends
/// it: in sRGB, premultiplied.
fn alpha_for_from(below: [f32; 3], rest: [f32; 3], ink: Color, target: f32) -> f32 {
    let ink = srgb(ink);
    let (mut low, mut high) = (0.0f32, 1.0f32);
    for _ in 0..24 {
        let mid = (low + high) / 2.0;
        if contrast(over(ink, mid, below), rest) < target {
            low = mid;
        } else {
            high = mid;
        }
    }
    high
}

fn srgb(c: Color) -> [f32; 3] {
    [c.r, c.g, c.b].map(srgb_encode)
}

fn over(top: [f32; 3], alpha: f32, below: [f32; 3]) -> [f32; 3] {
    [0, 1, 2].map(|c| top[c] * alpha + below[c] * (1.0 - alpha))
}

fn contrast(a: [f32; 3], b: [f32; 3]) -> f32 {
    let luminance = |c: [f32; 3]| {
        let [r, g, b] = c.map(srgb_decode);
        0.2126 * r + 0.7152 * g + 0.0722 * b
    };
    let (a, b) = (luminance(a), luminance(b));
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}
