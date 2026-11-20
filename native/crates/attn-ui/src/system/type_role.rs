use rune::{Color, El, Span, rich, text};

use super::door;
use super::said::Said;

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

/// `words` set as `kind` in `ink`, upper-cased where the role is. Copy written into the source
/// sets what stands between backticks in `Mono`; text built at run time is drawn as written.
pub fn words(words: impl Into<Said>, kind: Type, ink: Color) -> El {
    let words = words.into();
    let (shown, code) = words.shown();
    let shown = shown.into_owned();
    if kind.upper() {
        return door::font(text(shown.to_uppercase()).label(shown), kind).color(ink);
    }
    if code.is_empty() {
        return door::font(text(shown.clone()).label(shown), kind).color(ink);
    }
    let spans: Vec<Span> = code
        .into_iter()
        .map(|(start, end)| door::mono_span(Span::new(start, end)))
        .collect();
    door::font(rich(shown.clone(), spans).label(shown), kind).color(ink)
}

/// How wide `words` set as `kind` stand on one line, as [`words`] draws them.
pub fn width_of(words: impl Into<Said>, kind: Type) -> f32 {
    measured(&words.into(), kind, None).0
}

/// How many lines `words` set as `kind` take wrapped at `width`, as [`words`] draws them.
pub(crate) fn lines_at(words: impl Into<Said>, kind: Type, width: f32) -> f32 {
    (measured(&words.into(), kind, Some(width)).1 / door::style(kind).line_px()).round()
}

fn measured(words: &Said, kind: Type, width: Option<f32>) -> (f32, f32) {
    let (shown, code) = words.shown();
    let shown = if kind.upper() {
        shown.to_uppercase()
    } else {
        shown.into_owned()
    };
    let spans: Vec<Span> = code
        .into_iter()
        .map(|(start, end)| door::mono_span(Span::new(start, end)))
        .collect();
    rune::text::measure(&shown, &spans, door::style(kind), width)
}
