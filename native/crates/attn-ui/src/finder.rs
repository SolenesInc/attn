use rune::{El, col, el, row};
use rune_kit::{ScrollBar, TextInput};

use crate::blocks::{Block, Inline};
use crate::keycap::Keys;
use crate::system::{ListRow, Part, Radius, Said, Size, Space, Type, door, focus, words};
use crate::{Backdrop, Frame, Tokens};

/// How wide a finder is, by what its rows hold.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum FinderSize {
    /// A file or a note by its name over its path: a project's whole path shows.
    Narrow,
    /// Rows with columns after the name, such as a place's branch, machine and age: the
    /// launcher and the classic picker.
    Wide,
    /// The palette: as wide as `Wide`, its rows an agent's slot, state, age and keys, opened
    /// lower, under the tile headers, so the window's top stays in view.
    Palette,
}

impl FinderSize {
    /// As tall as the finder's box gets before its list scrolls.
    pub fn max_height(self) -> f32 {
        match self {
            FinderSize::Narrow => 520.0,
            FinderSize::Wide | FinderSize::Palette => 620.0,
        }
    }

    fn width(self) -> f32 {
        match self {
            FinderSize::Narrow => 560.0,
            FinderSize::Wide | FinderSize::Palette => 760.0,
        }
    }

    /// How far below its parent's top the finder opens: a tenth of the parent's width for the
    /// small finders, 80 for the launcher and 110 for the palette, their places on main.
    fn drop(self) -> El {
        match self {
            FinderSize::Narrow => el().w_full().aspect_ratio(10.0),
            FinderSize::Wide => el().w_full().h(80.0),
            FinderSize::Palette => el().w_full().h(110.0),
        }
    }
}

/// A finder's box on the surface over the window, as wide as `size` at most, shrinking to the
/// height it is given.
pub fn framed(t: &Tokens, panel: El, size: FinderSize) -> El {
    Frame::dialog(t)
        .draw_clipped(panel.shrink().min_h(0.0))
        .w_full()
        .max_w(size.width())
        .shrink()
        .min_h(0.0)
}

/// A finder over its parent: the finder's wash behind, and `panel` placed as [`placed`] puts
/// it.
pub fn layer(t: &Tokens, key: impl Into<String>, panel: El, size: FinderSize) -> El {
    let key = key.into();
    el().key(key.clone())
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(Backdrop::finder(t).draw(format!("{key}.scrim")))
        .child(placed(panel, size))
}

/// `panel` centered over its parent, as wide as `size` at most and as far down as it says,
/// shrinking to the parent's height.
pub fn placed(panel: El, size: FinderSize) -> El {
    let column = col()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .items_center()
        .child(panel.w_full().max_w(size.width()));
    col()
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(size.drop())
        .child(door::frame(column, Space::S0, Space::S16, Space::S16))
}

/// A small card in its parent's bottom right corner, keyed `key`, that says what is under way.
pub fn corner_card(t: &Tokens, key: impl Into<String>, content: El) -> El {
    let card = crate::Frame::panel(t).draw(door::inset(content, Space::S12, Space::S8));
    door::inset(
        col()
            .key(key.into())
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .items_end()
            .justify_end(),
        Space::S12,
        Space::S12,
    )
    .child(card)
}

/// A finder's box: its input row over a rule, its list, and its footer under a rule when it
/// has one. `panel` is the column that carries the box's key, role and keys.
pub fn panel(t: &Tokens, panel: El, input: El, list: El, footer: Option<El>) -> El {
    let panel = panel
        .child(input.border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle))
        .child(list.scroll_bar_styled(crate::scroll_bar(t)))
        .children(footer.map(|f| door::rule_above(f, t.border_subtle)));
    Frame::dialog(t).draw_clipped(panel)
}

/// A finder's query field: one line of body text a regular control high, with no box of its
/// own, since the finder's input row is its box.
pub fn field(input: TextInput) -> TextInput {
    door::line(input, Type::Body).height(Size::Regular.height())
}

/// A finder's input row: `field` between what goes before and after it, its words on the
/// line the options' words start on.
pub fn input_row(prefix: Option<El>, field: El, suffix: Option<El>) -> El {
    let field = door::inset(
        el().grow(1.0)
            .min_w(0.0)
            .child(door::hang_left(field.w_full(), PLAIN_FIELD_INSET)),
        Space::S0,
        Space::S8,
    );
    let line = door::gap(row().w_full().items_center(), Space::S8)
        .children(prefix)
        .child(field)
        .children(suffix);
    door::sides(line, OPTION_LINE, OPTION_LINE)
}

/// How far a plain text field sets its words in from its own edge.
const PLAIN_FIELD_INSET: Space = Space::S2;

/// Where everything in a finder starts: its title, its field's box, its headings, its rows'
/// words and its footer. A row's ground reaches out from it by the row's own inset.
const OPTION_LINE: Space = Space::S16;
const LIST_INSET: Space = Space::S8;

/// A finder's list of `rows`: it scrolls once the box reaches its height.
pub fn list(key: impl Into<String>, rows: Vec<El>) -> El {
    scrolling(key, LIST_INSET, LIST_INSET, col().w_full().children(rows)).role("listbox")
}

/// Scrolls the list keyed `list_key` so its highlighted row shows whole after the keyboard
/// moves the highlight: to the list's top, its heading with it, when `row_key` is `None` for the
/// first row or no row, or until the row keyed `row_key` is in view, with the list's inset
/// around it.
pub fn reveal(tx: &mut rune::Tx, list_key: impl Into<String>, row_key: Option<String>) {
    match row_key {
        None => tx.scroll_to_y(list_key, 0.0),
        Some(row_key) => tx.scroll_into_view_in(list_key, row_key),
    }
}

/// `content` set in by `x` and `y` inside a viewport keyed `key` that scrolls once its box
/// reaches its height. The inset scrolls with the content and is kept as the scroll margin, so
/// scrolled to either end, by hand or by a row scrolled into view, the content keeps the room
/// it has at rest.
fn scrolling(key: impl Into<String>, x: Space, y: Space, content: El) -> El {
    col()
        .key(key)
        .w_full()
        .shrink()
        .min_h(0.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .scroll_margin(y.pt())
        .child(door::inset(content, x, y))
}

/// What a list says when it lists nothing.
pub fn empty(t: &Tokens, key: impl Into<String>, message: impl Into<Said>) -> El {
    door::inset(row().w_full().justify_center(), Space::S16, Space::S16).child(
        words(message, Type::Small, t.text_muted)
            .key(key)
            .wrap()
            .align(rune::Align::Center),
    )
}

/// An option of a title over its path, keyed `key`: a list row of two lines, on the selected
/// surface while it is the keyboard's.
pub fn path_option(
    t: &Tokens,
    key: impl Into<String>,
    title: &str,
    path: &str,
    highlighted: bool,
) -> El {
    ListRow::new(key, format!("{title} {path}"))
        .role("option")
        .unfocusable()
        .cursor(highlighted)
        .selected(highlighted)
        .title(title.to_owned())
        .subtitle(Said::parts([Part::Code(path.to_owned())]), t)
        .build(t)
}

/// One hint of a footer: words and the keys among them, and whether it sits at the
/// footer's far end, as the hint that closes the finder does.
pub struct Hint {
    pieces: Vec<(Said, bool)>,
    at_end: bool,
}

impl Hint {
    /// `keys` between `before` and `after`.
    pub fn keys(before: impl Into<Said>, keys: &str, after: impl Into<Said>) -> Hint {
        let pieces = [
            (before.into().trimmed(), false),
            (Said::from(keys.to_owned()), true),
            (after.into().trimmed(), false),
        ]
        .into_iter()
        .filter(|(said, _)| !said.is_empty())
        .collect();
        Hint {
            pieces,
            at_end: false,
        }
    }

    pub fn plain(said: impl Into<Said>) -> Hint {
        Hint {
            pieces: vec![(said.into(), false)],
            at_end: false,
        }
    }

    /// Words and keys in turn, each piece `(text, is_keys)`.
    pub fn pieces(pieces: &[(&'static str, bool)]) -> Hint {
        Hint {
            pieces: pieces
                .iter()
                .map(|(said, keys)| {
                    let piece = if *keys {
                        Said::from((*said).to_owned())
                    } else {
                        Said::from(*said)
                    };
                    (piece, *keys)
                })
                .collect(),
            at_end: false,
        }
    }

    /// The hint sits at the footer's far end.
    pub fn at_end(mut self) -> Self {
        self.at_end = true;
        self
    }

    fn said(&self) -> String {
        self.pieces
            .iter()
            .map(|(said, _)| said.text())
            .collect::<Vec<_>>()
            .join(" ")
    }
}

/// A finder's footer, keyed `{finder}.footer`: hints, each keyed `{finder}.hint.{i}`, their
/// keys drawn as keycaps among their words, the hints marked so at the far end.
pub fn footer(t: &Tokens, finder: &str, hints: Vec<Hint>) -> El {
    let hint = |(i, h): (usize, Hint)| {
        let said = h.said();
        Inline::line()
            .key(format!("{finder}.hint.{i}"))
            .role("note")
            .label(said)
            .children(h.pieces.into_iter().map(|(piece, keys)| {
                if keys {
                    Keys::outlined(&[piece.text()]).build(t).a11y_hidden()
                } else {
                    words(piece, Type::Small, t.text_tertiary)
                        .single_line()
                        .a11y_hidden()
                }
            }))
    };
    let (end, start): (Vec<_>, Vec<_>) = hints.into_iter().enumerate().partition(|(_, h)| h.at_end);
    let start = door::gaps(
        row().grow(1.0).min_w(0.0).flex_wrap(),
        Space::S4,
        Space::S16,
    )
    .children(start.into_iter().map(hint));
    let end =
        door::gaps(row().flex_wrap(), Space::S4, Space::S16).children(end.into_iter().map(hint));
    let line = door::gap(
        row()
            .key(format!("{finder}.footer"))
            .w_full()
            .items_center(),
        Space::S16,
    )
    .child(start)
    .child(end);
    door::inset(line, OPTION_LINE, Space::S8)
}

/// A section heading among a finder's rows, its words on the line the rows' words start on.
pub fn heading(head: El) -> El {
    door::sides(col().w_full(), HEADING_HANG, HEADING_HANG).child(head)
}

/// What a group heading's own inset falls short of a row's.
const HEADING_HANG: Space = Space::S4;

/// A bar across a finder's top, over a rule, such as the choices that shape what it starts:
/// its parts across, onto further lines when the line runs out.
pub fn bar(t: &Tokens, parts: Vec<El>) -> El {
    let line = door::gaps(
        row().w_full().items_center().justify_between().flex_wrap(),
        Space::S8,
        Space::S16,
    )
    .children(parts);
    door::inset(line, OPTION_LINE, Space::S8).border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
}

/// A finder's query under its title: the title in `Label`, the query's box, and a line under
/// it that says how the query reads, over a rule.
pub fn query(t: &Tokens, title: El, field: El, status: Option<El>) -> El {
    let column = door::gap(col().w_full(), Space::S8)
        .child(title)
        .child(field)
        .children(status);
    door::inset(column, OPTION_LINE, Space::S12).border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
}

/// A finder's title, keyed `key`, which reads as its heading.
pub fn title(t: &Tokens, key: impl Into<String>, said: impl Into<Said>) -> El {
    words(said, Type::Label, t.text_tertiary)
        .key(key.into())
        .role("heading")
}

/// A query's box: `prefix`, such as the place already chosen, then the query's `field`, and
/// `ghost` over it, the rest of a completion; a regular control high, ringed while it holds
/// focus.
pub fn query_box(t: &Tokens, prefix: Option<El>, field: El, ghost: Option<El>) -> El {
    let line = door::gap(
        row().w_full().h(Size::Regular.height()).items_center(),
        Space::S8,
    )
    .fill(t.bg_input)
    .border(1.0, t.border)
    .children(prefix)
    .child(door::hang_left(
        field.grow(1.0).min_w(0.0),
        PLAIN_FIELD_INSET,
    ))
    .children(ghost);
    let line = door::corner(
        door::inset(line, Size::Regular.inset(), Space::S0),
        Radius::Control,
    );
    focus::ring_within(line, t)
}

/// A query's field in a [`query_box`]: one line of `Mono`, for paths and addresses.
pub fn query_field(input: TextInput) -> TextInput {
    door::line(input, Type::Mono).height(Size::Regular.height())
}

/// The rest of a completion after `typed`, keyed `key`, laid over a [`query_box`] without a
/// prefix where the typed words end.
pub fn ghost(key: impl Into<String>, typed: &str, rest: String, ink: rune::Color) -> El {
    let left = Size::Regular.inset().pt() + rune::text::advance(typed, door::style(Type::Mono));
    row()
        .pin(Some(left), Some(0.0), Some(0.0), Some(0.0))
        .items_center()
        .child(
            words(rest.clone(), Type::Mono, ink)
                .key(key.into())
                .label(rest)
                .single_line(),
        )
}

/// A chip that names what a query is inside, such as the project chosen before its place. It
/// takes at most half the query's line, its name cut short past that, so the query beside it
/// keeps the other half.
pub fn chip(t: &Tokens, key: impl Into<String>, said: impl Into<Said>) -> El {
    let chip = words(said, Type::Mono, t.text_secondary)
        .key(key.into())
        .truncate()
        .min_w(0.0)
        .max_w_frac(0.5)
        .fill(t.bg_elevated)
        .border(1.0, t.border);
    door::corner(door::inset(chip, Space::S6, Space::S2), Radius::Tag)
}

/// What a finder says between its query and its footer, such as what went wrong or what is
/// under way, spaced as blocks.
pub fn notices(blocks: Vec<Block>) -> El {
    door::inset(crate::blocks::stacked(blocks), OPTION_LINE, NOTICES_INSET)
}

/// [`notices`] that can outgrow a short window, keyed `key`: they give way and scroll so the
/// footer under them stays whole, and a row scrolled into view at either end keeps the
/// notices' inset between it and the edge.
pub fn scrolling_notices(t: &Tokens, key: impl Into<String>, blocks: Vec<Block>) -> El {
    scrolling(
        key,
        OPTION_LINE,
        NOTICES_INSET,
        crate::blocks::stacked(blocks),
    )
    .scroll_bar_styled(crate::scroll_bar(t))
}

const NOTICES_INSET: Space = Space::S12;

/// A finder's list of groups, each under its heading: it scrolls past `max` points, or
/// sooner where the window is shorter.
pub fn grouped(key: impl Into<String>, label: &str, rows: Vec<El>, max: f32) -> El {
    list(key, rows).label(label.to_owned()).max_h(max)
}
