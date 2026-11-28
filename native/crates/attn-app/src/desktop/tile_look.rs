//! How the tiles show which one the keyboard acts on, in the tile focus style the user chose:
//! a veil dims the bodies of the agents that are not active, and while the screen shows more
//! than one tile the style marks the active one. Tiles carry no border, except the one a
//! desktop shows alone in focus.

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
/// A 13 px mark box, with its 2 px border outside it.
const MARK_PX: f32 = 15.0;
const MARK_LINE_PX: f32 = 2.0;
/// The share of the primary text colour the spotlit header mixes into the elevated surface.
const HEADER_LIFT: f32 = 0.08;
/// An agent header's light from the top: the brand colour at its top and bottom, over a white
/// sheen that fades out.
const HEADER_BRAND: (f32, f32) = (0.11, 0.03);
const HEADER_SHEEN: f32 = 0.05;
/// The light line along an agent header's top edge.
const HEADER_TOP_LINE: f32 = 0.05;
/// The brand's share of the surface under the hover actions.
const HEADER_FADE: f32 = 0.07;

/// The app setting that keeps the chosen style.
const STYLE_PREF: &str = "attn.workspace.selectionStyle";
/// The app setting that gives ordinary desktops the softer look Home always has.
const SOFT_PREF: &str = "attn.workspace.softTiles";
/// The softer look sets its tiles this far apart and from the screen's edges, rounded as a
/// card, over a backdrop this much nearer the shadow colour than their fill.
const SOFT_GAP: f32 = 8.0;
const SOFT_BACKDROP: f32 = 0.3;
/// The rail down the active tile's left edge.
const RAIL_PX: f32 = 3.0;

/// How the active tile stands out among several. Dim leaves it at that: only the others
/// fade. Rail draws a brand line down its left edge, and spotlight lifts it on a shadow with a
/// lighter header and two corner marks. Both read its title in full.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum FocusStyle {
    Dim,
    Rail,
    Spotlight,
}

impl FocusStyle {
    pub const ALL: [FocusStyle; 3] = [FocusStyle::Dim, FocusStyle::Rail, FocusStyle::Spotlight];

    pub fn name(self) -> &'static str {
        match self {
            FocusStyle::Dim => "dim",
            FocusStyle::Rail => "rail",
            FocusStyle::Spotlight => "spotlight",
        }
    }

    /// The style the user last chose, or spotlight.
    pub(crate) fn chosen() -> FocusStyle {
        match crate::frame::pref_string(STYLE_PREF).as_deref() {
            Some("dim") => FocusStyle::Dim,
            Some("rail") => FocusStyle::Rail,
            _ => FocusStyle::Spotlight,
        }
    }

    pub(crate) fn choose(self) {
        crate::frame::set_pref_string(STYLE_PREF, self.name());
    }
}

/// Whether ordinary desktops have the softer look: rounded tiles apart on a darker backdrop.
pub(crate) fn soft_chosen() -> bool {
    crate::frame::pref_string(SOFT_PREF).as_deref() == Some("on")
}

pub(crate) fn choose_soft(on: bool) {
    crate::frame::set_pref_string(SOFT_PREF, if on { "on" } else { "off" });
}

/// What the screen shows around the tiles.
#[derive(Clone, Copy)]
pub(super) struct Screen {
    /// The softer look: rounded tiles apart on a darker backdrop, with no line under a header.
    pub soft: bool,
    /// The style that tells the active tile apart, while more than one tile is on screen.
    pub marking: Option<FocusStyle>,
    pub zoomed: bool,
    pub focus: bool,
    /// The tile fills a window of its own, where it is the one the keyboard acts on.
    pub lone: bool,
}

impl Screen {
    /// What shows between and around the tiles.
    pub(super) fn backdrop(&self, t: &Tokens) -> Color {
        if self.soft {
            attn_ui::colors::mix_srgb(t.bg_tile, t.shadow, SOFT_BACKDROP)
        } else {
            t.bg_tile
        }
    }

    /// Where a tile laid out at `rect` draws: inside it by half the gap in the softer look.
    pub(super) fn place(&self, [x, y, w, h]: [f32; 4]) -> [f32; 4] {
        if !self.soft {
            return [x, y, w, h];
        }
        let inset = SOFT_GAP / 2.0;
        [
            x + inset,
            y + inset,
            (w - SOFT_GAP).max(0.0),
            (h - SOFT_GAP).max(0.0),
        ]
    }
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

    fn marked(&self) -> Option<FocusStyle> {
        self.screen.marking.filter(|_| self.active)
    }

    fn spotlit(&self) -> bool {
        self.marked() == Some(FocusStyle::Spotlight)
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
        let tile = if self.screen.soft {
            let corner = attn_ui::system::Radius::Card.pt();
            tile.fill(t.bg_tile).radius(corner).clip_rounded(corner)
        } else {
            tile
        };
        let tile = if self.spotlit() {
            tile.paint_order(1)
                .box_shadow(Shadow {
                    y: 10.0,
                    blur: 26.0,
                    color: t.shadow.alpha(0.42),
                    ..Shadow::default()
                })
                .box_shadow(Shadow {
                    spread: 1.0,
                    color: t.highlight.alpha(0.09),
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
                .inert()
                .fill(t.bg_tile)
                .opacity(strength)
                .opacity_motion(fade())
        })
    }

    /// The lines the style draws over the active tile: the rail down its left edge, or the
    /// spotlight's corner marks, top left and bottom right. They come and go at once.
    pub(super) fn marks(&self, key: &str, t: &Tokens) -> Vec<El> {
        let line = |name: &str, el: El| {
            el.key(format!("{key}.{name}"))
                .paint_order(2)
                .inert()
                .fill(t.brand)
        };
        let (start, end) = (Some(0.0), Some(0.0));
        match self.marked() {
            None | Some(FocusStyle::Dim) => Vec::new(),
            Some(FocusStyle::Rail) => {
                vec![line("rail", el().pin(start, start, None, end).w(RAIL_PX))]
            }
            Some(FocusStyle::Spotlight) => vec![
                line(
                    "spotlight.top",
                    el().pin(start, start, None, None)
                        .w(MARK_PX)
                        .h(MARK_LINE_PX),
                ),
                line(
                    "spotlight.left",
                    el().pin(start, start, None, None)
                        .w(MARK_LINE_PX)
                        .h(MARK_PX),
                ),
                line(
                    "spotlight.bottom",
                    el().pin(None, None, end, end).w(MARK_PX).h(MARK_LINE_PX),
                ),
                line(
                    "spotlight.right",
                    el().pin(None, None, end, end).w(MARK_LINE_PX).h(MARK_PX),
                ),
            ],
        }
    }

    /// An agent's name reads in full while it is active; any other tile's title, such as a
    /// document's, reads quieter, unless rail or spotlight marks the tile.
    pub(super) fn title(&self, t: &Tokens) -> Color {
        let marked_in_full = self.marked().is_some_and(|style| style != FocusStyle::Dim);
        if marked_in_full || (self.agent && !self.dimmed()) {
            t.text_primary
        } else {
            t.text_secondary
        }
    }

    pub(super) fn subline_opacity(&self) -> f32 {
        if self.dimmed() { SUBLINE_DIMMED } else { 1.0 }
    }

    /// The header's surface: an agent's lit from the top in the brand colour under a light top
    /// edge, a document's or browser's on the elevated surface, both lighter on the spotlit
    /// tile, and closed off from the body by a line.
    pub(super) fn header(&self, header: El, t: &Tokens) -> El {
        let header = if self.screen.soft {
            header
        } else {
            header.border_sides([0.0, 0.0, 1.0, 0.0], t.border)
        };
        let header = match self.header_fill(t) {
            Some(fill) => header.fill(fill),
            None if self.agent => {
                let lit = |brand: f32, sheen: f32| {
                    let sheened = attn_ui::colors::mix_srgb(t.bg_tile, t.highlight, sheen);
                    attn_ui::colors::mix_srgb(sheened, t.brand, brand)
                };
                header.gradient(lit(HEADER_BRAND.0, HEADER_SHEEN), lit(HEADER_BRAND.1, 0.0))
            }
            None => header.fill(t.bg_elevated),
        };
        if !self.agent {
            return header;
        }
        header.child(
            el().pin(Some(0.0), Some(0.0), Some(0.0), None)
                .h(1.0)
                .inert()
                .fill(t.highlight.alpha(HEADER_TOP_LINE)),
        )
    }

    /// The surface under the header's hover actions, which hides the title they cover.
    pub(super) fn header_under(&self, t: &Tokens) -> Color {
        self.header_fill(t).unwrap_or_else(|| {
            if self.agent {
                attn_ui::colors::mix_srgb(t.bg_tile, t.brand, HEADER_FADE)
            } else {
                t.bg_elevated
            }
        })
    }

    fn header_fill(&self, t: &Tokens) -> Option<Color> {
        self.spotlit()
            .then(|| attn_ui::colors::mix_srgb(t.bg_elevated, t.text_primary, HEADER_LIFT))
    }
}

fn fade() -> Curve {
    Curve::Tween {
        duration: FADE_SECONDS,
        easing: Easing::EASE,
    }
}
