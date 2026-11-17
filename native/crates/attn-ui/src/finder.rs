use std::ops::Range;

use rune::{El, Span, TextStyle, col, el, row, text};

use crate::colors::mix_srgb;
use crate::{Backdrop, Frame, Tokens, frame};

/// A finder's box at most this wide: `.palette-box`.
pub const WIDTH: f32 = 560.0;

/// A finder field's text: `.palette-input`.
pub const FIELD_TEXT: TextStyle = TextStyle::new(14.0, 400.0);
/// A `.palette-box` at most this tall; its list scrolls past it.
pub const MAX_H: f32 = 520.0;

/// A footer's type; its keys are this at 600: `.palette-footer`.
const HINT_TYPE: TextStyle = TextStyle::new(12.0, 400.0);
const PATH_TYPE: TextStyle = TextStyle::new(10.0, 400.0).mono().lh(13.0);
const EMPTY_TYPE: TextStyle = TextStyle::new(12.0, 400.0);

/// A finder over its parent, as the Tauri app's `.palette` lays one out: the finder's wash
/// behind, and `panel` centered, at most `WIDTH` wide, a tenth of the parent's width down.
pub fn layer(t: &Tokens, key: impl Into<String>, panel: El) -> El {
    let key = key.into();
    el().key(key.clone())
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(Backdrop::finder(t).draw(format!("{key}.scrim")))
        .child(
            col()
                .w_full()
                .h_full()
                .child(el().w_full().aspect_ratio(10.0))
                .child(
                    col()
                        .w_full()
                        .grow(1.0)
                        .min_h(0.0)
                        .px(16.0)
                        .pb(16.0)
                        .items_center()
                        .child(panel.w_full().max_w(WIDTH)),
                ),
        )
}

/// A finder's box, `.palette-box`: its input row over a rule, its list, and its footer under
/// a rule when it has one. `panel` is the column that carries the box's key, role and keys.
pub fn panel(t: &Tokens, panel: El, input: El, list: El, footer: Option<El>) -> El {
    let panel = panel
        .child(input.border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle))
        .child(list)
        .children(footer.map(|f| f.border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)));
    Frame {
        fill: t.bg_elevated,
        edge: t.border,
        radius: 10.0,
        shadow: frame::shadow(t, 18.0, 60.0, 0.5),
    }
    .draw(panel)
    .clip_rounded(10.0)
}

/// A finder's input row, `.palette-input-row`: `field`, a query input in `FIELD_TEXT` one
/// line tall, between what goes before and after it.
pub fn input_row(prefix: Option<El>, field: El, suffix: Option<El>) -> El {
    let lead = if prefix.is_some() { 12.0 } else { 0.0 };
    let field = el()
        .grow(1.0)
        .min_w(0.0)
        .py(12.0)
        .pl(if prefix.is_some() { 0.0 } else { 14.0 })
        .pr(14.0)
        .child(field.w_full());
    row()
        .w_full()
        .items_center()
        .gap(8.0)
        .pl(lead)
        .pr(12.0)
        .children(prefix)
        .child(field)
        .children(suffix)
}

/// A finder's list, `.palette-list`: it scrolls once the box reaches its height.
pub fn list(key: impl Into<String>) -> El {
    col()
        .key(key)
        .role("listbox")
        .w_full()
        .shrink()
        .min_h(0.0)
        .padding(6.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
}

/// What a list says when it lists nothing, `.palette-empty`.
pub fn empty(t: &Tokens, key: impl Into<String>, message: &str) -> El {
    row().w_full().padding(14.0).justify_center().child(
        text(message)
            .key(key)
            .font(EMPTY_TYPE)
            .color(t.text_muted)
            .wrap()
            .align(rune::Align::Center),
    )
}

/// `option` as one of a list's options, `.palette-option`, filled while it is the highlighted
/// one.
pub fn option(t: &Tokens, option: El, highlighted: bool) -> El {
    option
        .w_full()
        .px(10.0)
        .py(7.0)
        .radius(7.0)
        .focus_selected(highlighted)
        .fill(if highlighted {
            mix_srgb(t.bg_elevated, t.brand, 0.18)
        } else {
            t.bg_elevated
        })
}

/// An option of a title over its path, `.palette-option-title` over `.palette-option-path`,
/// keyed `key`.
pub fn path_option(
    t: &Tokens,
    key: impl Into<String>,
    title: &str,
    path: &str,
    highlighted: bool,
) -> El {
    let option = col().key(key).gap(2.0).child(
        text(title)
            .font(rune::tok::type_::BODY)
            .color(title_color(t, highlighted))
            .single_line()
            .truncate(),
    );
    let option = option.child(
        text(path)
            .font(PATH_TYPE)
            .color(t.brand)
            .single_line()
            .truncate(),
    );
    self::option(t, option, highlighted)
}

/// An option's title: secondary, and primary on the highlighted option.
pub fn title_color(t: &Tokens, highlighted: bool) -> rune::Color {
    if highlighted {
        t.text_primary
    } else {
        t.text_secondary
    }
}

/// One hint of a footer: what it says, and which part of it names keys.
pub struct Hint {
    said: String,
    keys: Option<Range<usize>>,
}

impl Hint {
    /// `keys` between `before` and `after`.
    pub fn keys(before: &str, keys: &str, after: &str) -> Hint {
        let start = before.len();
        Hint {
            said: format!("{before}{keys}{after}"),
            keys: Some(start..start + keys.len()),
        }
    }

    pub fn plain(said: impl Into<String>) -> Hint {
        Hint {
            said: said.into(),
            keys: None,
        }
    }
}

/// A finder's footer, `.palette-footer`, keyed `{finder}.footer`: hints, each keyed
/// `{finder}.hint.{i}`, the keys they name picked out.
pub fn footer(t: &Tokens, finder: &str, hints: Vec<Hint>) -> El {
    row()
        .key(format!("{finder}.footer"))
        .w_full()
        .gap(12.0)
        .px(12.0)
        .py(7.0)
        .children(hints.into_iter().enumerate().map(|(i, h)| {
            let spans: Vec<Span> = h
                .keys
                .map(|k| {
                    Span::new(k.start, k.end)
                        .weight(600.0)
                        .color(t.text_secondary)
                })
                .into_iter()
                .collect();
            text(h.said)
                .key(format!("{finder}.hint.{i}"))
                .font(HINT_TYPE)
                .color(t.text_tertiary)
                .spans(spans)
                .single_line()
        }))
}
