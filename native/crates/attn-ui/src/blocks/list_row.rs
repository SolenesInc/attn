use std::borrow::Cow;

use rune::{Color, El, GrowthSpec, col, row};
use rune_kit::ScrollBar;

use super::Facts;
use crate::Tokens;
use crate::system::{Radius, Space, Type, door, dot_on_line, focus, layers};

/// One thing in a list that a reader goes to: a dot for its state, its title with what it
/// signals at the end of the title's line, then the facts about it and a line of what matched.
/// Its words sit on the line of the text around the list; its box reaches past them on each
/// side by its inset, so a selected or hovered row reads as one band.
pub struct ListRow {
    key: String,
    label: String,
    title: El,
    role: &'static str,
    value: String,
    mark: Option<Color>,
    signals: Vec<El>,
    facts: Option<Facts>,
    snippet: Option<El>,
    end: Option<El>,
    selected: bool,
}

impl ListRow {
    /// `title` is the row's words, built with [`crate::system::words`] or
    /// [`crate::system::marked`] as `Body`; `label` names the row for assistive tools.
    pub fn new(key: impl Into<String>, label: impl Into<String>, title: El) -> ListRow {
        ListRow {
            key: key.into(),
            label: label.into(),
            title,
            role: "button",
            value: String::new(),
            mark: None,
            signals: Vec::new(),
            facts: None,
            snippet: None,
            end: None,
            selected: false,
        }
    }

    /// What the row is to assistive tools: a button by default, an option in a list of choices.
    pub fn role(mut self, role: &'static str) -> Self {
        self.role = role;
        self
    }

    /// What assistive tools read after the row's name, such as its state.
    pub fn value(mut self, value: impl Into<String>) -> Self {
        self.value = value.into();
        self
    }

    /// The dot before the title, in its state's ink.
    pub fn mark(mut self, ink: Color) -> Self {
        self.mark = Some(ink);
        self
    }

    /// A word at the end of the title's line, such as "parked" or "blocked".
    pub fn signal(mut self, signal: El) -> Self {
        self.signals.push(signal);
        self
    }

    pub fn facts(mut self, facts: Facts) -> Self {
        self.facts = Some(facts);
        self
    }

    /// A line of what a search matched, cut short at the row's end.
    pub fn snippet(mut self, snippet: El) -> Self {
        self.snippet = Some(snippet);
        self
    }

    /// What the row holds at its far end, on the title's line, such as a count and a chevron.
    pub fn end(mut self, end: El) -> Self {
        self.end = Some(end);
        self
    }

    pub fn selected(mut self, selected: bool) -> Self {
        self.selected = selected;
        self
    }

    /// How tall a row with a one-line title and facts stands: what a long list guesses for a
    /// row it has not measured yet.
    pub fn height() -> f32 {
        2.0 * INSET_Y.pt()
            + door::line_height(Type::Body)
            + LINES.pt()
            + door::line_height(Type::Small)
    }

    pub fn build(self, t: &Tokens) -> El {
        let line = door::line_height(Type::Body);
        let head = door::gap(row().w_full().items_start(), Space::S8)
            .child(self.title.wrap().grow(1.0).min_w(0.0))
            .children(self.signals);
        let content = door::gap(col().grow(1.0).min_w(0.0), LINES)
            .child(head)
            .children(self.facts.map(|facts| facts.build(t)))
            .children(self.snippet.map(|s| s.truncate()));
        let end = self.end.map(|end| row().h(line).items_center().child(end));
        let fill = if self.selected {
            t.bg_selected
        } else {
            Color::TRANSPARENT
        };
        let row = row()
            .key(self.key)
            .role(self.role)
            .label(self.label)
            .a11y_value(self.value)
            .focusable(true)
            .w_full()
            .items_start()
            .fill(fill)
            .children(self.mark.map(|ink| dot_on_line(ink, Type::Body)))
            .child(content)
            .children(end);
        let row = door::gap(row, Space::S8);
        let row = door::hang(door::inset(row, INSET_X, INSET_Y), INSET_X);
        let row = door::corner(row, Radius::Control);
        let surface = if self.selected { fill } else { t.bg_panel };
        focus::ring(layers::pressable(row, surface, t), t)
    }
}

const INSET_X: Space = Space::S8;
const INSET_Y: Space = Space::S6;
const LINES: Space = Space::S2;

/// A long list of [`ListRow`]s, `count` of them, that builds only the rows in view: `key_of`
/// names row `i` and `row` builds it.
pub fn rows<S: 'static>(
    key: impl Into<String>,
    role: &'static str,
    label: impl Into<String>,
    count: usize,
    key_of: impl for<'a> Fn(&'a S, usize) -> Cow<'a, str> + 'static,
    row: impl Fn(&S, usize) -> El + 'static,
) -> El {
    col()
        .key(key.into())
        .role(role)
        .label(label.into())
        .self_stretch()
        .growth(GrowthSpec::default())
        .virtual_rows(count, ListRow::height(), key_of, row)
}

/// [`ListRow`]s side by side, each `width` wide, in a strip that scrolls sideways under a rule.
pub fn strip(
    key: impl Into<String>,
    role: &'static str,
    label: impl Into<String>,
    width: f32,
    rows: impl IntoIterator<Item = El>,
    t: &Tokens,
) -> El {
    let strip = row()
        .key(key.into())
        .role(role)
        .label(label.into())
        .w_full()
        .items_start()
        .scroll_x()
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .children(rows.into_iter().map(|r| r.w(width).grow(0.0)));
    door::gap(door::inset(strip, Space::S16, Space::S6), Space::S4)
        .scroll_bar_styled(crate::thin_scroll_bar(t).horizontal())
}
