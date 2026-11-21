use rune::{Color, El, Span, col, row};
use rune_kit::{ScrollBar, TextInput};

use crate::system::{IconSize, Size, Space, Type, door, icon_button, words};
use crate::{Frame, Tokens};

/// How far the bars keep from the terminal's top, and from its right edge, where the
/// terminal's scroll bar runs.
const FROM_TOP: Space = Space::S6;
const FROM_SCROLL_BAR: f32 = 14.0;
/// The widest the bars and their list stand, so a found line on a wide terminal stays a line a
/// person reads.
const MEASURE: f32 = 640.0;
/// The narrowest a bar's field gets, room for a short query, before the count gives way.
const FIELD_FLOOR: f32 = 80.0;
/// How much of its count a bar keeps whole while the field gives way: "11 lines" and "3 of 12"
/// whole, a count of thousands cut short.
const COUNT_KEEPS: f32 = 64.0;

/// A bar over a terminal's top right corner that finds or filters what the terminal shows: its
/// field, its toggles and buttons, and how many things it found.
pub struct TerminalBar {
    key: String,
    field: Option<El>,
    items: Vec<El>,
}

impl TerminalBar {
    pub fn new(key: impl Into<String>) -> TerminalBar {
        TerminalBar {
            key: key.into(),
            field: None,
            items: Vec::new(),
        }
    }

    /// The field the bar is typed into, `width` wide while the terminal has room and narrower
    /// where it does not, its words in `Mono`.
    pub fn field(mut self, input: TextInput, width: f32, t: &Tokens) -> Self {
        let field = door::line(input.plain(), Type::Mono)
            .height(door::line_height(Type::Mono))
            .width(None)
            .build(crate::system::kit(t))
            .w_full();
        self.field = Some(row().w(width).shrink().min_w(FIELD_FLOOR).child(field));
        self
    }

    /// A button or a toggle on the bar, such as Match case, or Close.
    pub fn push(mut self, item: impl Into<El>) -> Self {
        self.items.push(item.into());
        self
    }

    /// How many things the bar found, as "3 of 12".
    pub fn count(mut self, key: impl Into<String>, said: impl Into<String>, t: &Tokens) -> Self {
        let said = said.into();
        let keeps = crate::system::width_of(said.clone(), Type::Mono).min(COUNT_KEEPS);
        self.items.push(
            words(said, Type::Mono, t.text_secondary)
                .key(key.into())
                .single_line()
                .truncate()
                .shrink()
                .min_w(keeps),
        );
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let bar = door::gap(
            row().key(self.key).min_w(0.0).justify_end().items_center(),
            Space::S4,
        )
        .children(self.field)
        .children(self.items);
        Frame::terminal_bar(t).draw_clipped(door::inset(bar, Space::S8, Space::S4))
    }

    /// A button on a bar keyed `key` and read as `label`, its mark `mark`, set in while `on`.
    pub fn button(
        key: impl Into<String>,
        label: impl Into<String>,
        mark: &'static str,
        on: bool,
    ) -> crate::system::IconButton {
        icon_button(key, label, mark, IconSize::Small)
            .size(Size::Small)
            .selected(on)
    }

    /// The list a filter finds under its bar, `max_height` tall at most: each line a press
    /// away from where it stands.
    pub fn list(key: impl Into<String>, max_height: f32, lines: Vec<El>, t: &Tokens) -> El {
        let list = col()
            .key(key.into())
            .max_h(max_height)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .children(lines)
            .scroll_bar_styled(crate::scroll_bar(t));
        Frame::terminal_bar(t).draw(door::inset(col().child(list), Space::S4, Space::S4))
    }

    /// What the list says when nothing matched.
    pub fn none(key: impl Into<String>, said: impl Into<String>, t: &Tokens) -> El {
        door::inset(
            words(said.into(), Type::Small, t.text_secondary).key(key.into()),
            Space::S8,
            Space::S6,
        )
    }

    /// One line the list found, its matches washed in `wash`, read as `label`.
    pub fn line<S: 'static>(
        key: impl Into<String>,
        label: impl Into<String>,
        said: String,
        matches: &[(usize, usize)],
        wash: Color,
        t: &Tokens,
        press: impl Fn(&mut S) + 'static,
    ) -> El {
        let ink = t.text_primary;
        let spans: Vec<Span> = matches
            .iter()
            .map(|&(start, end)| Span {
                bg: Some(wash),
                ..Span::new(start, end)
            })
            .collect();
        let line = rune_kit::Button::new(label)
            .variant(rune_kit::ButtonVariant::Ghost)
            .on_press(press)
            .label_with(move |_, _| {
                words(said, Type::Mono, ink)
                    .single_line()
                    .truncate()
                    .spans(spans)
                    .w_full()
            })
            .build(crate::system::kit(t))
            .key(key.into())
            .w_full()
            .h(door::line_height(Type::Mono) + Space::S4.pt());
        door::corner(
            door::inset(line, Space::S6, Space::S0),
            crate::system::Radius::Tag,
        )
    }

    /// How far down its terminal a corner of bars `height` tall reaches, measured from the
    /// terminal's top.
    pub fn reach(height: f32) -> f32 {
        FROM_TOP.pt() + height
    }

    /// `bars` one under another over a terminal's top right corner, each as wide as it needs up
    /// to a reading line, so a short list leaves the terminal's output beside it visible. A bar
    /// narrower than its smallest overflows past the terminal's left edge, never its right,
    /// where its Close stands.
    pub fn corner(bars: Vec<El>) -> El {
        let bars = door::gap(
            col().items_stretch().shrink().min_w(0.0).max_w(MEASURE),
            Space::S4,
        )
        .children(bars.into_iter().map(|bar| {
            row()
                .justify_end()
                .min_w(0.0)
                .child(bar.shrink().min_w(0.0))
        }));
        row()
            .pin(
                Some(FROM_TOP.pt()),
                Some(FROM_TOP.pt()),
                Some(FROM_SCROLL_BAR),
                None,
            )
            .justify_end()
            .child(bars)
    }
}
