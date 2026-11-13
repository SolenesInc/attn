//! How the tiles show which one the keyboard acts on, as the Tauri app's spotlight: the active
//! tile lifts on a shadow with a lighter header and two corner marks while the screen shows
//! more than one tile, and a veil dims the bodies of the agents that are not active. Tiles carry
//! no border, except the one a desktop shows alone in focus.

use attn_ui::Tokens;
use rune::{Color, Curve, Easing, El, Shadow, el};

/// How strongly an inactive agent's body is veiled, and more lightly while zoom gives the
/// active tile most of the row.
const VEIL: f32 = 0.42;
const ZOOMED_VEIL: f32 = 0.28;
/// How a tile that is not an agent fades while another tile is active, and less while zoom
/// gives the active tile most of the row.
const FADED: f32 = 0.58;
const ZOOMED_FADED: f32 = 0.72;
/// The tile being dragged.
const DRAGGED: f32 = 0.5;
const FADE_SECONDS: f32 = 0.12;
const SUBLINE_DIMMED: f32 = 0.7;
const MARK_PX: f32 = 13.0;
const MARK_LINE_PX: f32 = 2.0;
/// The share of the primary text colour the spotlit header mixes into the elevated surface.
const HEADER_LIFT: f32 = 0.08;

/// What the screen shows around the tiles.
#[derive(Clone, Copy)]
pub(super) struct Screen {
    /// More than one tile is on screen, so the active one needs telling apart.
    pub many: bool,
    pub zoomed: bool,
    pub focus: bool,
    /// The tile fills a window of its own, where it is the one the keyboard acts on.
    pub lone: bool,
}

/// How one tile looks.
pub(super) struct Look {
    active: bool,
    agent: bool,
    dragged: bool,
    screen: Screen,
}

impl Look {
    pub(super) fn of(agent: bool, active: bool, dragged: bool, screen: Screen) -> Look {
        Look {
            active: active || screen.lone,
            agent,
            dragged,
            screen,
        }
    }

    fn spotlit(&self) -> bool {
        self.active && self.screen.many
    }

    fn dimmed(&self) -> bool {
        self.agent && !self.active && !self.dragged
    }

    /// The tile's own frame: its opacity, the spotlight's lift, and the focus border.
    pub(super) fn frame(&self, tile: El, t: &Tokens) -> El {
        let opacity = if self.dragged {
            DRAGGED
        } else if !self.agent && !self.active {
            if self.screen.zoomed {
                ZOOMED_FADED
            } else {
                FADED
            }
        } else {
            1.0
        };
        let tile = tile.opacity(opacity).opacity_motion(fade());
        let tile = if self.spotlit() {
            tile.paint_order(1)
                .box_shadow(Shadow {
                    y: 10.0,
                    blur: 26.0,
                    color: Color::BLACK.alpha(0.42),
                    ..Shadow::default()
                })
                .box_shadow(Shadow {
                    spread: 1.0,
                    color: Color::WHITE.alpha(0.09),
                    inset: true,
                    ..Shadow::default()
                })
        } else {
            tile
        };
        if self.screen.focus {
            tile.border(1.0, t.brand.alpha(0.28)).box_shadow(Shadow {
                spread: 1.0,
                color: t.brand.alpha(0.08),
                ..Shadow::default()
            })
        } else {
            tile
        }
    }

    /// The veil over an agent's body, below its header, there while the agent is inactive.
    pub(super) fn veil(&self, key: &str, below: f32, t: &Tokens) -> Option<El> {
        let strength = match (self.dimmed(), self.screen.zoomed) {
            (false, _) => 0.0,
            (true, false) => VEIL,
            (true, true) => ZOOMED_VEIL,
        };
        self.agent.then(|| {
            el().key(format!("{key}.veil"))
                .pin(Some(0.0), Some(below), Some(0.0), Some(0.0))
                .paint_order(1)
                .fill(t.bg_tile)
                .opacity(strength)
                .opacity_motion(fade())
        })
    }

    /// The spotlight's corner marks, top left and bottom right. Like the Tauri app's, they
    /// come and go at once.
    pub(super) fn marks(&self, key: &str, t: &Tokens) -> Vec<El> {
        if !self.spotlit() {
            return Vec::new();
        }
        let line = |side: &str, el: El| {
            el.key(format!("{key}.spotlight.{side}"))
                .paint_order(2)
                .fill(t.brand)
        };
        let (start, end) = (Some(0.0), Some(0.0));
        vec![
            line(
                "top",
                el().pin(start, start, None, None)
                    .w(MARK_PX)
                    .h(MARK_LINE_PX),
            ),
            line(
                "left",
                el().pin(start, start, None, None)
                    .w(MARK_LINE_PX)
                    .h(MARK_PX),
            ),
            line(
                "bottom",
                el().pin(None, None, end, end).w(MARK_PX).h(MARK_LINE_PX),
            ),
            line(
                "right",
                el().pin(None, None, end, end).w(MARK_LINE_PX).h(MARK_PX),
            ),
        ]
    }

    pub(super) fn title(&self, t: &Tokens) -> Color {
        if self.dimmed() {
            t.text_secondary
        } else {
            t.text_primary
        }
    }

    pub(super) fn subline_opacity(&self) -> f32 {
        if self.dimmed() { SUBLINE_DIMMED } else { 1.0 }
    }

    /// The header's background, lighter on the spotlit tile.
    pub(super) fn header_fill(&self, t: &Tokens) -> Option<Color> {
        self.spotlit()
            .then(|| srgb_mix(t.bg_elevated, t.text_primary, HEADER_LIFT))
    }
}

fn fade() -> Curve {
    Curve::Tween {
        duration: FADE_SECONDS,
        easing: Easing::EASE,
    }
}

/// `t` of the way from `a` to `b` in encoded sRGB, as CSS `color-mix(in srgb, ..)`.
fn srgb_mix(a: Color, b: Color, t: f32) -> Color {
    let m = |x: f32, y: f32| {
        let (x, y) = (rune::color::srgb_encode(x), rune::color::srgb_encode(y));
        rune::color::srgb_decode(x + (y - x) * t)
    };
    Color {
        r: m(a.r, b.r),
        g: m(a.g, b.g),
        b: m(a.b, b.b),
        a: a.a + (b.a - a.a) * t,
    }
}
