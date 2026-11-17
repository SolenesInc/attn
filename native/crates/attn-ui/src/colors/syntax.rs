//! The colors of highlighted code: GitHub's Default themes, which the Tauri app's Shiki draws
//! code in whatever the color scheme.

use rune_kit::syntax::{Syntax, SyntaxTheme};

use super::c;

/// GitHub Dark Default's or GitHub Light Default's token colors, each kind as the theme colors
/// its TextMate scopes; plain names and punctuation take the text color.
pub fn github(dark: bool) -> SyntaxTheme {
    let [
        text,
        keyword,
        string,
        constant,
        comment,
        entity,
        function,
        heading,
        inserted,
        deleted,
    ] = if dark {
        [
            "#e6edf3", "#ff7b72", "#a5d6ff", "#79c0ff", "#8b949e", "#ffa657", "#d2a8ff", "#1f6feb",
            "#aff5b4", "#ffdcd7",
        ]
    } else {
        [
            "#1f2328", "#cf222e", "#0a3069", "#0550ae", "#6e7781", "#953800", "#8250df", "#0550ae",
            "#116329", "#82071e",
        ]
    }
    .map(c);
    [
        (Syntax::Keyword, keyword),
        (Syntax::Operator, keyword),
        (Syntax::String, string),
        (Syntax::Regex, string),
        (Syntax::Link, string),
        (Syntax::Number, constant),
        (Syntax::Property, constant),
        (Syntax::Attribute, constant),
        (Syntax::Comment, comment),
        (Syntax::Type, entity),
        (Syntax::Function, function),
        (Syntax::Heading, heading),
        (Syntax::Inserted, inserted),
        (Syntax::Deleted, deleted),
        (Syntax::Punctuation, text),
        (Syntax::Parameter, text),
        (Syntax::Variable, text),
    ]
    .into_iter()
    .fold(SyntaxTheme::neutral(dark), |theme, (kind, color)| {
        theme.with(kind, color)
    })
    .with_weight(Syntax::Heading, 700.0)
}
