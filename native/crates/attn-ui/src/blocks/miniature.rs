use rune::{El, Spring, SpringPair, Transition, col, el, row};

use super::shelf::{Ground, MemberState, ShelfMember, legible, mark};
use crate::colors::{MARK_READS, READS};
use crate::system::{IconSize, Space, Type, door, icon, keycaps, keys_hint, words};
use crate::{Glyph, Tokens};

/// A row drawn small over the tile area, and what stands around it: a strip per other desktop
/// and a line per tile that needs the user.
pub struct Overview;

/// One tile of the row drawn small, under its name.
pub struct Miniature;

const LABEL_LEAST: f32 = 22.0;
const LABEL_FULL: f32 = 46.0;
const LABEL_EDGE: f32 = 1.0;
const NEEDS_EDGE: f32 = 2.0;
const HIGHLIGHT_EDGE: f32 = 3.0;
const COLUMN_CORNER: f32 = 6.0;
const BAR: f32 = 3.0;
const BAR_CORNER: f32 = 2.0;
const BAR_GAP: f32 = 5.0;
const SWATCH_W: f32 = 18.0;
const STRIP: f32 = 22.0;
const STRIP_CORNER: f32 = 6.0;
const LINE: f32 = 24.0;
const LINE_EDGE: f32 = 3.0;
const MARK_GAP: Space = Space::S6;
const ENTER_SCALE: f32 = 1.04;
const ENTER_SECONDS: f32 = 0.16;
const LEAVE_SECONDS: f32 = 0.14;

impl MemberState {
    /// What a tile that needs the user says it is doing.
    pub fn needs(self) -> Option<&'static str> {
        match self {
            MemberState::Waiting => Some("waits"),
            MemberState::Approval => Some("approve?"),
            _ => None,
        }
    }
}

impl Overview {
    /// The room between the layer's edge and what it holds.
    pub const PADDING: f32 = Space::S24.pt();
    /// How far apart two strips start.
    pub const STRIP_STEP: f32 = STRIP + Space::S4.pt();
    /// How tall a line is.
    pub const LINE: f32 = LINE;
    /// The room between the row and what stands above or below it, which the bar of a column on
    /// screen fits in.
    pub const GAP: f32 = Space::S12.pt();

    /// The layer keyed `key`, over everything in its parent.
    pub fn layer(key: &str, t: &Tokens) -> El {
        let motion = Transition::scale(ENTER_SCALE).springs(SpringPair::new(
            Spring::new(ENTER_SECONDS, 0.0),
            Spring::new(LEAVE_SECONDS, 0.0),
        ));
        door::clip(el().key(key))
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .fill(t.bg_app)
            .transition(motion)
    }

    /// The ground of a column's miniatures at `rect`.
    pub fn column(key: String, [x, y, w, h]: [f32; 4], t: &Tokens) -> El {
        let edge = legible(t.text_tertiary, t.bg_app, MARK_READS, t);
        door::clip_round(el().key(key), COLUMN_CORNER)
            .absolute(x, y)
            .w(w)
            .h(h)
            .fill(t.bg_panel)
            .outline(LABEL_EDGE, 0.0, edge)
    }

    /// The bar under the column at `rect`, which is on screen.
    pub fn on_screen(key: String, [x, y, w, h]: [f32; 4], t: &Tokens) -> El {
        bar(w, t).key(key).absolute(x, y + h + BAR_GAP).inert()
    }

    /// The words an empty desktop shows in place of its row, at `rect`.
    pub fn empty([x, y, w, h]: [f32; 4], t: &Tokens) -> El {
        row()
            .key("overview.empty")
            .absolute(x, y)
            .w(w)
            .h(h)
            .center()
            .child(words(
                "No tiles on this desktop",
                Type::Body,
                legible(t.text_tertiary, t.bg_app, READS, t),
            ))
    }

    /// A desktop's strip: its `number`, its name, a mark per tile in the groups its columns
    /// make, and what they count to.
    pub fn strip(
        key: String,
        number: Option<String>,
        name: &str,
        columns: &[Vec<ShelfMember>],
        counts: &str,
        t: &Tokens,
    ) -> El {
        let ground = Ground::of(t.bg_app, t);
        let marks = columns.iter().map(|column| {
            door::gap(row().items_center(), Space::S2)
                .children(column.iter().map(|member| mark(member, &ground, t)))
        });
        let marks = door::clip(
            door::gap(row().items_center(), MARK_GAP)
                .min_w(0.0)
                .a11y_hidden()
                .children(marks),
        );
        strip(key, number.unwrap_or_default(), name, t)
            .a11y_value(counts.to_owned())
            .child(marks)
            .child(
                ground
                    .words(counts.to_owned(), Type::Small, t.text_secondary, t)
                    .single_line()
                    .a11y_hidden(),
            )
    }

    /// The strip that makes a desktop.
    pub fn new_desktop(key: &str, t: &Tokens) -> El {
        strip(key.to_owned(), "+".to_owned(), "New desktop", t)
    }

    /// The line of a tile that needs the user and what it `asks`.
    pub fn line(key: String, member: &ShelfMember, asks: &str, t: &Tokens) -> El {
        let ground = Ground::of(t.bg_app, t);
        let line = door::gap(row().key(key).w_full().items_center(), Space::S8)
            .role("button")
            .label(format!("{}: {asks}", member.name))
            .h(LINE)
            .border_sides([0.0, 0.0, 0.0, LINE_EDGE], member.ink(&ground, t).lit)
            .child(mark(member, &ground, t))
            .child(
                ground
                    .words(member.name.clone(), Type::Name, t.text_primary, t)
                    .single_line()
                    .a11y_hidden(),
            )
            .child(
                ground
                    .words(asks.to_owned(), Type::Body, t.text_primary, t)
                    .single_line()
                    .truncate()
                    .shrink()
                    .a11y_hidden(),
            );
        ground.pressable(door::sides(line, Space::S12, Space::S8), 0.0)
    }

    /// The keys the overview has and what each does, after what the bar means.
    pub fn legend(hints: impl IntoIterator<Item = (Vec<String>, String)>, t: &Tokens) -> El {
        let t = &Tokens {
            text_tertiary: legible(t.text_tertiary, t.bg_app, READS, t),
            ..*t
        };
        let swatch = door::gap(row().items_center(), Space::S6)
            .child(bar(SWATCH_W, t))
            .child(words("on screen", Type::Small, t.text_tertiary));
        door::gap(
            row().key("overview.legend").w_full().items_center(),
            Space::S16,
        )
        .h(LINE)
        .child(swatch)
        .children(
            hints
                .into_iter()
                .map(|(keys, what)| keys_hint(keys, what, t)),
        )
    }
}

impl Miniature {
    /// How tall a tile's header stands at full size, which the label takes the place of.
    pub const HEADER: f32 = LABEL_FULL;

    /// How tall a tile's label stands over a row drawn at `scale`.
    pub fn label_height(scale: f32) -> f32 {
        (LABEL_FULL * scale).round().max(LABEL_LEAST)
    }

    /// The tile keyed `key` at `rect` of its column, with `picture` under its label.
    pub fn tile(
        key: String,
        member: &ShelfMember,
        highlighted: bool,
        [x, y, w, h]: [f32; 4],
        scale: f32,
        picture: El,
        t: &Tokens,
    ) -> El {
        let label = label(member, highlighted, Self::label_height(scale), t);
        let edge = if highlighted {
            Some((HIGHLIGHT_EDGE, t.text_primary))
        } else {
            member
                .needs_user()
                .then(|| (NEEDS_EDGE, needing(member, t)))
        };
        let outline = edge.map(|(width, ink)| {
            door::clip_round(el(), COLUMN_CORNER)
                .absolute(0.0, 0.0)
                .w_full()
                .h_full()
                .border(width, ink)
                .inert()
        });
        door::clip(col().key(key))
            .role("option")
            .label(member.name.clone())
            .absolute(x, y)
            .w(w)
            .h(h)
            .child(label)
            .child(picture.w_full().grow(1.0).min_h(0.0))
            .children(outline)
    }

    /// What a tile with no picture shows: its icon and `title`.
    pub fn card(glyph: Glyph, title: &str, t: &Tokens) -> El {
        let face = match glyph {
            Glyph::Icon(drawn) => icon(drawn, IconSize::Large, t.text_tertiary),
            Glyph::Text(letter) => words(letter, Type::Heading, t.text_tertiary),
        };
        let ink = legible(t.text_secondary, t.bg_panel, READS, t);
        door::inset(door::gap(col().center(), Space::S6), Space::S8, Space::S4)
            .fill(t.bg_panel)
            .child(face)
            .child(
                words(title.to_owned(), Type::Small, ink)
                    .single_line()
                    .truncate()
                    .shrink(),
            )
    }

    /// A tile's own `body`, laid out `size` points large and drawn at `scale`.
    pub fn body(body: El, size: (f32, f32), scale: f32, t: &Tokens) -> El {
        let body = col()
            .absolute(0.0, 0.0)
            .w(size.0)
            .h(size.1)
            .origin(0.0, 0.0)
            .scale(scale)
            .inert()
            .a11y_hidden()
            .child(body);
        door::clip(el()).fill(t.bg_app).child(body)
    }
}

fn bar(width: f32, t: &Tokens) -> El {
    door::round(el().w(width).min_w(width).h(BAR), BAR_CORNER).fill(legible(
        t.text_secondary,
        t.bg_app,
        MARK_READS,
        t,
    ))
}

fn needing(member: &ShelfMember, t: &Tokens) -> rune::Color {
    member.ink(&Ground::of(t.bg_panel, t), t).rest
}

fn strip(key: String, cap: String, name: &str, t: &Tokens) -> El {
    let ground = Ground::of(t.bg_app, t);
    let strip = door::gap(row().key(key).w_full().items_center(), Space::S8)
        .role("button")
        .label(name.to_owned())
        .h(STRIP)
        .child(
            row()
                .min_w(STRIP)
                .justify_center()
                .a11y_hidden()
                .children((!cap.is_empty()).then(|| keycaps([cap], t))),
        )
        .child(
            ground
                .words(name.to_owned(), Type::Name, t.text_primary, t)
                .single_line()
                .truncate()
                .shrink()
                .a11y_hidden(),
        );
    ground.pressable(strip, STRIP_CORNER)
}

fn label(member: &ShelfMember, highlighted: bool, height: f32, t: &Tokens) -> El {
    let (ground, ink) = if highlighted {
        (t.text_primary, t.bg_panel)
    } else {
        (t.bg_panel, t.text_primary)
    };
    let (width, edge) = match (highlighted, member.needs_user()) {
        (true, _) => (LABEL_EDGE, t.text_primary),
        (false, true) => (NEEDS_EDGE, needing(member, t)),
        (false, false) => (LABEL_EDGE, legible(t.text_tertiary, ground, MARK_READS, t)),
    };
    let word = member.state.and_then(MemberState::needs).map(|word| {
        words(
            word,
            Type::Control,
            legible(member.ink(&Ground::of(ground, t), t).rest, ground, READS, t),
        )
        .single_line()
    });
    let label = door::gap(row().w_full().items_center(), MARK_GAP)
        .h(height)
        .min_h(height)
        .fill(ground)
        .border_sides([0.0, 0.0, width, 0.0], edge)
        .a11y_hidden()
        .child(mark(member, &Ground::of(ground, t), t))
        .child(
            words(
                member.name.clone(),
                Type::Name,
                legible(ink, ground, READS, t),
            )
            .single_line()
            .truncate()
            .grow(1.0)
            .min_w(0.0),
        )
        .children(word);
    door::sides(label, Space::S8, Space::S8)
}
