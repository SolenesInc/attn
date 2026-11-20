use rune::{Color, El, KeyPlatform, Shadow, row, text};

use crate::Tokens;
use crate::system::{Radius, Space, Type, door, words};

/// The two keycaps the Tauri app draws. `Raised` is `Keycap.css`, the shortcut sheet's and the
/// palette's key: bold sans on a lifted chip. `Outlined` is the `kbd` hint in drawers, footers
/// and sentences: quiet monospace in a thin frame; `Ledged` is that frame with a 2-point
/// bottom edge, as the ledger, the analytics list and the settings notes draw it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Look {
    Raised,
    Outlined,
    Ledged,
}

/// A raised keycap's height, so a row that carries one and a row that does not line up.
pub const RAISED_H: f32 = 19.0;

const RAISED_TYPE: Type = Type::Control;
const HINT_TYPE: Type = Type::Mono;

/// Keys drawn as keycaps, one cap per key, named as this platform writes the chord: `⇧⌘K` on
/// a Mac, `Ctrl+Shift+K` elsewhere.
pub struct Keys {
    caps: Vec<String>,
    look: Look,
    colors: Option<[Color; 3]>,
}

impl Keys {
    pub fn new<S: AsRef<str>>(look: Look, caps: &[S]) -> Keys {
        Keys {
            caps: caps.iter().map(|c| c.as_ref().to_owned()).collect(),
            look,
            colors: None,
        }
    }

    pub fn raised<S: AsRef<str>>(caps: &[S]) -> Keys {
        Keys::new(Look::Raised, caps)
    }

    pub fn outlined<S: AsRef<str>>(caps: &[S]) -> Keys {
        Keys::new(Look::Outlined, caps)
    }

    pub fn ledged<S: AsRef<str>>(caps: &[S]) -> Keys {
        Keys::new(Look::Ledged, caps)
    }

    /// An outlined hint's own ink, frame and fill, for a cap on a coloured surface such as a
    /// filled button.
    pub fn colors(mut self, ink: Color, edge: Color, fill: Color) -> Self {
        self.colors = Some([ink, edge, fill]);
        self
    }

    fn said(&self) -> String {
        self.caps
            .join(if KeyPlatform::current() == KeyPlatform::Mac {
                ""
            } else {
                "+"
            })
    }

    pub fn build(self, t: &Tokens) -> El {
        let label = self.said();
        let (look, colors) = (self.look, self.colors);
        door::gap(row(), Space::S2)
            .role("group")
            .label(label)
            .items_center()
            .children(
                self.caps
                    .into_iter()
                    .map(move |key| cap(key, look, colors, t)),
            )
    }
}

fn cap(key: String, look: Look, colors: Option<[Color; 3]>, t: &Tokens) -> El {
    let raised = look == Look::Raised;
    let [ink, edge, fill] = colors.unwrap_or(if raised {
        [t.text_primary, Color::TRANSPARENT, t.bg_button]
    } else {
        [t.text_tertiary, t.border, t.bg_app]
    });
    let face = door::font(text(key), if raised { RAISED_TYPE } else { HINT_TYPE }).color(ink);
    let cap = row().center().a11y_hidden().child(face);
    match look {
        Look::Raised => raised_cap(cap, fill),
        Look::Outlined | Look::Ledged => {
            let bottom = if look == Look::Ledged { 2.0 } else { 1.0 };
            door::corner(door::inset(cap, Space::S4, Space::S0), Radius::Tag)
                .fill(fill)
                .border_sides([1.0, 1.0, bottom, 1.0], edge)
                .border_layout()
        }
    }
}

/// Groups of outlined keys in a row ending on its end, each whole or gone: a group that does not
/// fit after those before it leaves, the last first.
pub fn giving_way(groups: impl IntoIterator<Item = El>) -> El {
    door::clip(
        door::gap(row(), Space::S6)
            .flex_wrap()
            .justify_end()
            .h(door::line_height(HINT_TYPE) + 2.0)
            .shrink()
            .min_w(0.0)
            .children(groups),
    )
}

/// The Tauri app's `.keycap`: a chip lifted by a drop shadow, a faint ring and a top light,
/// in `Keycap.css`'s literal black and white.
fn raised_cap(cap: El, fill: Color) -> El {
    door::corner(door::inset(cap, Space::S6, Space::S0), Radius::Tag)
        .min_w(18.0)
        .h(RAISED_H)
        .fill(fill)
        .box_shadow(Shadow {
            y: 1.0,
            blur: 2.0,
            color: Color::BLACK.alpha(0.35),
            ..Shadow::default()
        })
        .box_shadow(Shadow {
            spread: 1.0,
            color: Color::WHITE.alpha(0.07),
            ..Shadow::default()
        })
        .box_shadow(Shadow {
            y: 1.0,
            color: Color::WHITE.alpha(0.08),
            inset: true,
            ..Shadow::default()
        })
}

/// One raised key in the kit's colors, which `crate::kit` paints from the tokens: what rune-kit's
/// `Keybind`, and so the shortcut recorder, draws each key with.
pub(crate) fn kit_cap(k: &rune_kit::Kit, key: String) -> El {
    let face = door::font(text(key), RAISED_TYPE).color(k.c.text_base);
    raised_cap(
        row().center().a11y_hidden().child(face),
        k.c.bg_button_neutral,
    )
}

/// Alternative shortcuts for one action, such as `⌘ ↑ / ⌘ ↓`: each a group of keycaps, a dim
/// slash before each one after the first. Where one line cannot hold them all, the groups wrap
/// onto lines of their own, each group whole, flush to the end.
pub fn alternatives(groups: Vec<Keys>, t: &Tokens) -> El {
    let label = groups.iter().map(Keys::said).collect::<Vec<_>>().join("/");
    let children = groups.into_iter().enumerate().map(|(i, group)| {
        let keys = group.build(t).a11y_hidden();
        if i == 0 {
            return keys;
        }
        door::gap(row().items_center(), Space::S6)
            .child(words("/", Type::Small, t.text_dimmed).a11y_hidden())
            .child(keys)
    });
    door::gap(row().flex_wrap().justify_end(), Space::S6)
        .role("group")
        .label(label)
        .items_center()
        .shrink()
        .min_w(0.0)
        .children(children)
}
