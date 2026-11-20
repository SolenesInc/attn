use rune::{Color, El, Span, rich, text};

use super::door;

/// What a run of text is, which decides its face, size, weight and case.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Type {
    /// A page's or a dialog's name: 18/700.
    PageTitle,
    /// A section's name inside a page: 15/600.
    Heading,
    /// The name of a thing in a row: a role, a setting, an option. 13/600.
    Name,
    /// Reading text: a lead, a paragraph, a field's value. 13/400.
    Body,
    /// What a row or a control says about itself. 12/400.
    Small,
    /// A field's or a column's name: 12/600 mono, upper case.
    Label,
    /// A tag's word: 12/600 mono, upper case, on a 14 line.
    Tag,
    /// A value read as data: a model, an id, a path. 12/400 mono.
    Mono,
    /// A button's or a segment's word: 12/600.
    Control,
    /// What a person writes as the main thing on a surface, such as a quick message: 18/400.
    Compose,
}

impl Type {
    pub(crate) fn upper(self) -> bool {
        matches!(self, Type::Label | Type::Tag)
    }
}

/// `words` set as `kind` in `ink`, upper-cased where the role is and read as written. Text
/// between backticks is set in `Mono`.
pub fn words(words: impl Into<String>, kind: Type, ink: Color) -> El {
    let words = words.into();
    if kind.upper() {
        return door::font(text(words.to_uppercase()).label(words), kind).color(ink);
    }
    if !words.contains('`') {
        return door::font(text(words.clone()).label(words), kind).color(ink);
    }
    let (shown, commands) = commands_in(&words);
    let spans: Vec<Span> = commands
        .into_iter()
        .map(|(start, end)| door::mono_span(Span::new(start, end)))
        .collect();
    door::font(rich(shown.clone(), spans).label(shown), kind).color(ink)
}

/// `words` set as `kind` in `ink` exactly as written, backticks and all: text that comes from
/// outside, such as an error, a name or a path, is never taken for markup.
pub fn verbatim(words: impl Into<String>, kind: Type, ink: Color) -> El {
    let words = words.into();
    let shown = if kind.upper() {
        words.to_uppercase()
    } else {
        words.clone()
    };
    door::font(text(shown).label(words), kind).color(ink)
}

/// `said`, then `code` in `Mono`, both read as written: a value such as a path is never taken
/// for markup, however it is spelled.
pub(crate) fn words_and_code(said: &str, code: &str, kind: Type, ink: Color) -> El {
    let shown = format!("{said} {code}");
    let start = said.len() + 1;
    let span = door::mono_span(Span::new(start, shown.len()));
    door::font(rich(shown.clone(), vec![span]).label(shown), kind).color(ink)
}

/// How wide `words` set as `kind` stand on one line, as [`words`] draws them.
pub(crate) fn width_of(words: &str, kind: Type) -> f32 {
    measured(words, kind, None).0
}

/// How many lines `words` set as `kind` take wrapped at `width`, as [`words`] draws them.
pub(crate) fn lines_at(words: &str, kind: Type, width: f32) -> f32 {
    (measured(words, kind, Some(width)).1 / door::style(kind).line_px()).round()
}

fn measured(words: &str, kind: Type, width: Option<f32>) -> (f32, f32) {
    let (shown, commands) = commands_in(words);
    let shown = if kind.upper() {
        shown.to_uppercase()
    } else {
        shown
    };
    let spans: Vec<Span> = commands
        .into_iter()
        .map(|(start, end)| door::mono_span(Span::new(start, end)))
        .collect();
    rune::text::measure(&shown, &spans, door::style(kind), width)
}

fn commands_in(words: &str) -> (String, Vec<(usize, usize)>) {
    let mut shown = String::with_capacity(words.len());
    let mut commands = Vec::new();
    for (i, part) in words.split('`').enumerate() {
        let start = shown.len();
        shown.push_str(part);
        if i % 2 == 1 && !part.is_empty() {
            commands.push((start, shown.len()));
        }
    }
    (shown, commands)
}
