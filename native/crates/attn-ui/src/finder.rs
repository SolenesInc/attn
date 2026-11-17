use rune::{El, TextStyle, col, el, row, text};

use crate::colors::mix_srgb;
use crate::{Backdrop, Frame, Tokens, frame, type_};

/// A finder's box at most this wide: `.palette-box`.
pub const WIDTH: f32 = 560.0;

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

/// A finder field's text: `.palette-input`.
pub const FIELD_TEXT: TextStyle = TextStyle::new(14.0, 400.0);
/// How far a finder's list scrolls before it stops growing.
pub const LIST_MAX: f32 = 520.0;

/// A finder's box, as the Tauri app's `.palette-box` frames one: `field` over a rule, then
/// `list`, raised on its shadow.
pub fn panel(t: &Tokens, field: El, list: El) -> El {
    Frame {
        fill: t.bg_elevated,
        edge: t.border,
        radius: 10.0,
        shadow: frame::shadow(t, 18.0, 60.0, 0.5),
    }
    .draw(col())
    .clip_rounded(10.0)
    .child(
        row()
            .w_full()
            .items_center()
            .px(14.0)
            .py(12.0)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
            .child(field.grow(1.0)),
    )
    .child(list.w_full().padding(6.0))
}

/// One of a finder's rows, `.palette-option`: its title over the path it opens, the
/// highlighted one tinted.
pub fn option(t: &Tokens, title: String, path: String, highlighted: bool) -> El {
    col()
        .w_full()
        .gap(2.0)
        .px(10.0)
        .py(7.0)
        .radius(7.0)
        .fill(if highlighted {
            mix_srgb(t.bg_elevated, t.brand, 0.18)
        } else {
            t.bg_elevated
        })
        .child(
            text(title)
                .font(type_::BODY)
                .color(if highlighted {
                    t.text_primary
                } else {
                    t.text_secondary
                })
                .single_line()
                .truncate(),
        )
        .child(
            text(path)
                .font(type_::MONO_CAPTION)
                .color(t.brand)
                .single_line()
                .truncate(),
        )
}

/// What a finder's list says when it lists nothing: `.palette-empty`.
pub fn empty(t: &Tokens, words: &str) -> El {
    text(words.to_owned())
        .font(type_::LABEL)
        .color(t.text_muted)
        .w_full()
        .align(rune::Align::Center)
        .padding(14.0)
}
