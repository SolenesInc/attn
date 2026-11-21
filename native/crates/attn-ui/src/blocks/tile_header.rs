use rune::{Color, El, col, row};
use rune_kit::TextInput;

use crate::Tokens;
use crate::system::{
    DotShape, DotSize, Radius, Size, Space, Type, door, dot, focus, keys_hint, words,
};

/// An agent's header: its headline, and the subline drawn up into the headline's foot.
const AGENT_HEADLINE: f32 = 30.0;
const SUBLINE: f32 = 16.0;
const SUBLINE_PULL: f32 = 6.0;
/// A document's or a browser's header: a compact control and the room above and below it.
const TILE_HEADLINE: f32 = 36.0;
/// The line that closes a header off from the tile's body.
const RULE: f32 = 1.0;

/// The header across the top of a tile: an agent's, with its state's mark, its name and a
/// second line of what it is working from, or a document's or a browser's, with its title and
/// its actions.
pub struct TileHeader {
    key: String,
    agent: bool,
    leading: f32,
    mark: Option<El>,
    title: Option<El>,
    hover: Vec<El>,
    hover_ground: Color,
    items: Vec<El>,
    subline: Option<(String, Vec<El>, f32)>,
    track: Option<El>,
}

impl TileHeader {
    /// An agent's header, keyed `key`.
    pub fn agent(key: impl Into<String>) -> TileHeader {
        TileHeader::new(key, true)
    }

    /// A document's or a browser's header, keyed `key`.
    pub fn tile(key: impl Into<String>) -> TileHeader {
        TileHeader::new(key, false)
    }

    fn new(key: impl Into<String>, agent: bool) -> TileHeader {
        TileHeader {
            key: key.into(),
            agent,
            leading: 0.0,
            mark: None,
            title: None,
            hover: Vec::new(),
            hover_ground: Color::TRANSPARENT,
            items: Vec::new(),
            subline: None,
            track: None,
        }
    }

    /// How tall a header stands, its rule included: an agent's or a document's.
    pub fn height(agent: bool) -> f32 {
        if agent {
            AGENT_HEADLINE + SUBLINE - SUBLINE_PULL + RULE
        } else {
            TILE_HEADLINE + RULE
        }
    }

    /// The room a headline leaves its title and chips in a tile `width` wide, past its insets
    /// and the one control it always shows.
    pub fn headline_room(width: f32) -> f32 {
        width - 2.0 * AGENT_INSET.pt() - Size::Small.height() - Space::S8.pt()
    }

    /// The room a subline leaves its pieces in a tile `width` wide.
    pub fn subline_room(width: f32) -> f32 {
        width - subline_start() - AGENT_INSET.pt()
    }

    /// The room between two parts of a headline.
    pub fn headline_gap() -> f32 {
        Space::S8.pt()
    }

    /// The room between two pieces of a subline.
    pub fn subline_gap() -> f32 {
        SUBLINE_GAP.pt()
    }

    /// Room at the header's start for what the window draws there, such as its own buttons in
    /// a tile that fills a window.
    pub fn leading(mut self, points: f32) -> Self {
        self.leading = points;
        self
    }

    /// The agent's state, as a dot before its name.
    pub fn mark(mut self, mark: El) -> Self {
        self.mark = Some(mark);
        self
    }

    /// The tile's name in `ink`, cut short with an ellipsis where the header runs out of room.
    pub fn title(mut self, said: impl Into<String>, ink: Color) -> Self {
        self.title = Some(
            words(said.into(), Type::Name, ink)
                .single_line()
                .truncate()
                .grow(1.0),
        );
        self
    }

    /// What stands in the title's place and takes its room, such as a browser's address.
    pub fn title_control(mut self, control: impl Into<El>) -> Self {
        self.title = Some(control.into().grow(1.0).min_w(0.0));
        self
    }

    /// A field in the title's place that the title is edited in, such as a browser's address:
    /// compact, its value in `Mono`, taking the title's room.
    pub fn title_field(self, input: TextInput, t: &Tokens) -> Self {
        let size = Size::Compact;
        let field = door::line(input.plain(), Type::Mono)
            .width(None)
            .height(door::line_height(Type::Mono))
            .build(crate::system::kit(t))
            .grow(1.0);
        let frame = row()
            .w_full()
            .h(size.height())
            .items_center()
            .fill(t.bg_input)
            .border(1.0, t.border)
            .child(field);
        let frame = door::corner(
            door::sides(frame, size.inset(), size.inset()),
            Radius::Control,
        );
        self.title_control(focus::ring_within(
            frame.hover(crate::system::layers::hover_layer(t.bg_input, t)),
            t,
        ))
    }

    /// Controls shown over the title's end while the pointer is on the header, on `ground` so
    /// the title they cover does not show through.
    pub fn hover(mut self, controls: Vec<El>, ground: Color) -> Self {
        self.hover = controls;
        self.hover_ground = ground;
        self
    }

    /// A chip or a control after the title, such as a turn settling.
    pub fn push(mut self, item: impl Into<El>) -> Self {
        self.items.push(item.into());
        self
    }

    pub fn push_all(mut self, items: impl IntoIterator<Item = El>) -> Self {
        self.items.extend(items);
        self
    }

    /// The header's own actions at its end, such as Pop out and Close, close together.
    pub fn actions(mut self, actions: Vec<El>) -> Self {
        if !actions.is_empty() {
            self.items
                .push(door::gap(row().items_center(), Space::S2).children(actions));
        }
        self
    }

    /// The agent's second line keyed `key`: its pieces from the title's start, at `opacity`.
    pub fn subline(mut self, key: impl Into<String>, pieces: Vec<El>, opacity: f32) -> Self {
        self.subline = Some((key.into(), pieces, opacity));
        self
    }

    /// A line along the header's foot, such as how long a turn has before it settles.
    pub fn track(mut self, track: El) -> Self {
        self.track = Some(track);
        self
    }

    pub fn build(self) -> El {
        let height = TileHeader::height(self.agent);
        let headline = row().w_full().items_center();
        let headline = if self.agent {
            door::inset(headline.h(AGENT_HEADLINE), AGENT_INSET, Space::S0)
        } else {
            door::sides(headline.h(TILE_HEADLINE), TILE_START, TILE_END)
        };
        let hover = (!self.hover.is_empty()).then(|| {
            door::sides(
                door::gap(row().items_center(), Space::S2),
                Space::S4,
                Space::S0,
            )
            .key(format!("{}.actions", self.key))
            .pin(None, Some(0.0), Some(0.0), Some(0.0))
            .fill(self.hover_ground)
            .group_opacity(0.0, 1.0)
            .children(self.hover)
        });
        let title = row()
            .grow(1.0)
            .min_w(0.0)
            .h_full()
            .items_center()
            .children(self.title)
            .children(hover);
        let headline = door::gap(headline, Space::S8)
            .children(self.mark)
            .child(title)
            .children(self.items);
        let subline = self.subline.map(|(key, pieces, opacity)| {
            let line = door::gap(
                row().key(key).w_full().h(SUBLINE).items_center(),
                SUBLINE_GAP,
            )
            .children(pieces)
            .opacity(opacity);
            let line = door::sides(line, Space::S0, AGENT_INSET);
            door::clip(door::inset_start(line, subline_start()))
                .absolute(0.0, AGENT_HEADLINE - SUBLINE_PULL)
                .w_full()
        });
        let track = self.track.map(|track| {
            col()
                .absolute(0.0, height - track_height())
                .w_full()
                .h(track_height())
                .child(track)
        });
        let header = col()
            .key(format!("{}.header", self.key))
            .keeps_press_from_within()
            .w_full()
            .h(height)
            .hover_group()
            .child(headline)
            .children(subline)
            .children(track);
        door::inset_start(header, self.leading)
    }
}

/// How far an agent's headline keeps its parts from the header's sides.
const AGENT_INSET: Space = Space::S8;
/// How far a document's headline starts its title from the header's start, and ends its last
/// action from its end.
const TILE_START: Space = Space::S12;
const TILE_END: Space = Space::S8;
const SUBLINE_GAP: Space = Space::S8;
const TRACK: f32 = 2.0;

/// Where a subline's first piece starts: under the title, past the state's dot.
fn subline_start() -> f32 {
    AGENT_INSET.pt() + DotSize::Small.side() + Space::S8.pt()
}

fn track_height() -> f32 {
    TRACK
}

/// How a turn that settles on its own stands, which sets how strongly its chip is tinted.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Settling {
    /// Counting down to its deadline.
    Counting,
    /// Its countdown stands still.
    Paused,
    /// The person kept the turn.
    Kept,
}

impl Settling {
    fn tint(self) -> (f32, f32, DotShape, f32) {
        match self {
            Settling::Counting => (0.16, 0.55, DotShape::Filled, 1.0),
            Settling::Paused => (0.10, 0.35, DotShape::Filled, 0.55),
            Settling::Kept => (0.09, 0.40, DotShape::Square, 0.7),
        }
    }
}

/// The chip in an agent's headline for a turn that settles on its own, keyed `key` and read as
/// `label`: the settling mark, `said`, and the keys that keep it with what they do, or the
/// mark alone where the headline is short of room.
pub struct SettlingChip {
    key: String,
    label: String,
    said: &'static str,
    keys: Option<(String, &'static str)>,
    stands: Settling,
    mark_only: bool,
    disabled: bool,
}

impl SettlingChip {
    pub fn new(
        key: impl Into<String>,
        label: impl Into<String>,
        said: &'static str,
        stands: Settling,
    ) -> SettlingChip {
        SettlingChip {
            key: key.into(),
            label: label.into(),
            said,
            keys: None,
            stands,
            mark_only: false,
            disabled: false,
        }
    }

    pub fn keys(mut self, keys: Option<(String, &'static str)>) -> Self {
        self.keys = keys;
        self
    }

    pub fn mark_only(mut self, mark_only: bool) -> Self {
        self.mark_only = mark_only;
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    /// How wide the chip stands with its words, and as its mark alone.
    pub fn widths(said: &str, keys: Option<(&str, &str)>) -> (f32, f32) {
        let mark = DotSize::Small.side() + 2.0 * Size::Small.inset().pt();
        let words = Space::S6.pt() + crate::system::width_of(said.to_owned(), Type::Tag);
        let keys = keys.map_or(0.0, |(keys, verb)| {
            Space::S6.pt() + crate::system::keys_hint_width([keys], verb)
        });
        (mark + words + keys, mark)
    }

    pub fn build<S: 'static>(self, t: &Tokens, press: impl Fn(&mut S) + 'static) -> El {
        let (fill, edge, shape, strength) = self.stands.tint();
        let ink = match self.stands {
            Settling::Counting => t.settle_ink,
            Settling::Paused | Settling::Kept => t.settle_ink_quiet,
        };
        let mark = dot(t.settle, shape, DotSize::Small).opacity(strength);
        let (said, keys, mark_only, t_face) = (self.said, self.keys, self.mark_only, *t);
        let face = move |_, _| {
            door::gap(row().items_center(), Space::S6)
                .child(mark.clone())
                .children((!mark_only).then(|| words(said, Type::Tag, ink).single_line()))
                .children(
                    keys.clone()
                        .filter(|_| !mark_only)
                        .map(|(keys, verb)| keys_hint([keys], verb, &t_face)),
                )
        };
        let chip = rune_kit::Button::new(self.label)
            .variant(rune_kit::ButtonVariant::Ghost)
            .disabled(self.disabled)
            .on_press(press)
            .label_with(face)
            .build(crate::system::kit(t))
            .key(self.key)
            .h(Size::Small.height())
            .fill(t.settle.alpha(fill))
            .border(1.0, t.settle.alpha(edge))
            .hover(t.settle.alpha(fill + 0.08));
        focus::ring(
            door::corner(
                door::inset(chip, Size::Small.inset(), Space::S0),
                Radius::Control,
            ),
            t,
        )
    }
}
