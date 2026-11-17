//! The pieces every ledger list is drawn from, as the Tauri app's `LedgerPrimitives.tsx` and
//! `LedgerSurface.css` draw them: rows, the segmented scope, the query well, the inspector and
//! its fields, verbs, links and the status line.

use attn_ui::Tokens;
use attn_ui::type_;
use rune::{Color, Cycle, El, Shadow, TextStyle, col, el, row, text};
use rune_kit::{Button, ButtonVariant};

/// A row's mark, as the Tauri app's `.ledger-glyph.is-*`.
#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) enum Glyph {
    Live(Color),
    Working(Color),
    Closed,
    Refreshing,
    Pinned,
    Scheduled,
    Dirty,
    Clean,
    Removed,
    Error,
}

const GLYPH: f32 = 8.0;
const GLYPH_COLUMN: f32 = 22.0;

/// `ledger-pulse`: full, then dim at the half, over 1.4 s.
const PULSE: Cycle = Cycle {
    period: 1.4,
    phase: 0.0,
    stops: &[(0.0, 1.0), (0.5, 0.55), (1.0, 1.0)],
    still: 1.0,
    fps: 30.0,
};

pub(crate) fn glyph(g: Glyph, t: &Tokens) -> El {
    let (ring, fill) = match g {
        Glyph::Live(ink) | Glyph::Working(ink) => (ink, Some(ink)),
        Glyph::Dirty => (t.warning, Some(t.warning)),
        Glyph::Closed | Glyph::Clean => (t.text_muted, None),
        Glyph::Refreshing => (t.text_tertiary, None),
        Glyph::Pinned => (t.success, Some(t.success)),
        Glyph::Scheduled => (t.warning, None),
        Glyph::Removed => (t.text_muted, Some(t.text_muted)),
        Glyph::Error => (t.error, Some(t.error)),
    };
    let mark = el()
        .w(GLYPH)
        .h(GLYPH)
        .radius(if g == Glyph::Pinned { 2.0 } else { GLYPH / 2.0 })
        .border(1.5, ring)
        .fill(fill.unwrap_or(Color::TRANSPARENT))
        .a11y_hidden();
    match g {
        Glyph::Working(_) | Glyph::Refreshing => mark.cycle(PULSE),
        Glyph::Removed => mark.opacity(0.5),
        _ => mark,
    }
}

/// One list row, `.ledger-row`: the mark centered in its column, the body, then what trails it,
/// over a hairline.
pub(crate) fn list_row(
    key: String,
    label: String,
    selected: bool,
    mark: Glyph,
    body: El,
    trailing: Vec<El>,
    t: &Tokens,
) -> El {
    row()
        .key(key)
        .role("option")
        .label(label)
        .a11y_value(if selected { "selected" } else { "" })
        .hover_group()
        .w_full()
        .min_h(42.0)
        .items_center()
        .gap(8.0)
        .pl(10.0)
        .pr(12.0)
        .py(6.0)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .fill(if selected {
            t.brand.alpha(0.09)
        } else {
            Color::TRANSPARENT
        })
        .box_shadow(Shadow {
            x: 2.0,
            color: if selected {
                t.brand
            } else {
                Color::TRANSPARENT
            },
            inset: true,
            ..Shadow::default()
        })
        .hover(t.bg_elevated.alpha(0.6))
        .child(row().w(GLYPH_COLUMN).justify_center().child(glyph(mark, t)))
        .child(body.grow(1.0).min_w(0.0))
        .child(row().items_center().gap(6.0).children(trailing))
}

/// A row's title over its meta line, then its note, `.ledger-row-body`.
pub(crate) fn row_body(title: String, dim: bool, meta: El, note: Option<El>, t: &Tokens) -> El {
    col()
        .gap(1.0)
        .child(
            text(title)
                .font(TextStyle {
                    weight: 500.0,
                    ..type_::BODY
                })
                .color(if dim {
                    t.text_secondary
                } else {
                    t.text_primary
                })
                .single_line()
                .truncate(),
        )
        .child(meta.color(t.text_tertiary).single_line().truncate())
        .children(note)
}

/// A row's meta line in the meta face, its parts apart by dots.
pub(crate) fn meta(words: String) -> El {
    text(words).font(META)
}

pub(crate) const META: TextStyle = TextStyle {
    weight: 400.0,
    ..type_::LABEL
};

/// A row's age, `.ledger-stamp`.
pub(crate) fn stamp(words: String, t: &Tokens) -> El {
    text(words)
        .font(type_::MONO_CAPTION)
        .color(t.text_muted)
        .single_line()
        .mr(4.0)
}

const VERB: TextStyle = TextStyle {
    weight: 400.0,
    ..type_::LABEL
};

/// How a verb reads, as `.ledger-verb`, `.is-primary` and `.is-danger`.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Ink {
    Quiet,
    Primary,
    Danger,
}

fn verb_face(key: String, words: &str, ink: Ink, raised: bool, pad: f32, t: &Tokens) -> El {
    let color = match ink {
        Ink::Danger => t.error,
        Ink::Primary => t.text_primary,
        Ink::Quiet if raised => t.text_primary,
        Ink::Quiet => t.text_tertiary,
    };
    let raised = raised || ink == Ink::Primary;
    let face = words.to_owned();
    Button::new(words)
        .variant(ButtonVariant::GhostMuted)
        .label_with(move |_, _| text(face).font(VERB).color(color).single_line())
        .build(attn_ui::kit(t))
        .key(key)
        .h(VERB.line_px() + 8.0)
        .px(pad)
        .radius(6.0)
        .fill(if raised {
            t.bg_elevated
        } else {
            Color::TRANSPARENT
        })
        .border(1.0, if raised { t.border } else { Color::TRANSPARENT })
}

/// A verb in the inspector, `.ledger-verb`: quiet text at its own width until it is the
/// primary one.
pub(crate) fn verb(key: impl Into<String>, words: &str, ink: Ink, t: &Tokens) -> El {
    verb_face(key.into(), words, ink, false, 9.0, t).self_start()
}

/// A verb on a row: dimmed on a row at rest, raised on the selected row, full on a hovered one.
pub(crate) fn row_verb(key: String, words: &str, selected: bool, t: &Tokens) -> El {
    rested(
        verb_face(key, words, Ink::Quiet, selected, 9.0, t),
        selected,
    )
}

/// A row's More button, `.ledger-more`.
pub(crate) fn row_more(key: String, selected: bool, t: &Tokens) -> El {
    rested(
        verb_face(key, "···", Ink::Quiet, selected, 6.0, t),
        selected,
    )
}

fn rested(verb: El, selected: bool) -> El {
    if selected {
        verb
    } else {
        verb.group_opacity(0.55, 1.0)
    }
}

/// A link in the inspector, `.ledger-link`: accent text at its own width.
pub(crate) fn link(key: impl Into<String>, words: &str, t: &Tokens) -> El {
    let face = words.to_owned();
    let ink = t.accent;
    Button::new(words)
        .variant(ButtonVariant::GhostMuted)
        .label_with(move |_, _| text(face).font(type_::BODY).color(ink).wrap())
        .build(attn_ui::kit(t))
        .key(key.into())
        .h(type_::BODY.line_px())
        .px(0.0)
        .self_start()
}

/// The capsule a list's scopes sit in, `.ledger-segmented`.
pub(crate) fn segmented(
    key: &str,
    role: &'static str,
    label: &str,
    choices: Vec<El>,
    t: &Tokens,
) -> El {
    row()
        .key(key.to_owned())
        .role(role)
        .label(label.to_owned())
        .padding(2.0)
        .radius(7.0)
        .fill(t.bg_app)
        .border(1.0, t.border_subtle)
        .children(choices)
}

const SEGMENT: TextStyle = TextStyle {
    weight: 500.0,
    ..type_::LABEL
};

/// One choice of a [`segmented`] capsule, raised while chosen.
pub(crate) fn segment(key: String, words: &str, on: bool, t: &Tokens) -> El {
    let face = words.to_owned();
    let ink = if on { t.text_primary } else { t.text_tertiary };
    let segment = Button::new(words)
        .variant(ButtonVariant::GhostMuted)
        .label_with(move |_, _| text(face).font(SEGMENT).color(ink).single_line())
        .build(attn_ui::kit(t))
        .key(key)
        .a11y_value(if on { "pressed" } else { "" })
        .h(SEGMENT.line_px() + 6.0)
        .px(10.0)
        .radius(5.0);
    if on {
        segment
            .fill(t.bg_elevated)
            .shadow(1.0, 0.0, Color::BLACK.alpha(0.25))
    } else {
        segment.fill(Color::TRANSPARENT)
    }
}

/// A list's toolbar, `.ledger-toolbar`.
pub(crate) fn toolbar(children: Vec<El>, t: &Tokens) -> El {
    row()
        .w_full()
        .items_center()
        .gap(10.0)
        .px(12.0)
        .py(8.0)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .children(children)
}

/// The query field's well, `.ledger-query`: a slash, the field, then its chips.
pub(crate) fn query_well(field: El, chips: Vec<El>, t: &Tokens) -> El {
    row()
        .grow(1.0)
        .min_w(0.0)
        .h(28.0)
        .items_center()
        .gap(8.0)
        .pl(10.0)
        .pr(8.0)
        .radius(7.0)
        .fill(t.bg_app)
        .border(1.0, t.border_subtle)
        .child(text("/").font(type_::MONO_CAPTION).color(t.text_muted))
        .child(field)
        .children(chips)
}

/// The inspector beside a list, `.ledger-inspector`: its head, then its fields.
pub(crate) fn inspector(head: El, fields: Vec<El>, t: &Tokens) -> El {
    col()
        .key("ledger.inspector")
        .role("complementary")
        .label("Details")
        .w_frac(0.3)
        .min_w(280.0)
        .max_w(380.0)
        .h_full()
        .gap(10.0)
        .pt(14.0)
        .px(16.0)
        .pb(20.0)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .child(head.mb(2.0))
        .children(fields)
}

const INSPECTOR_TITLE: TextStyle = TextStyle {
    size: 15.0,
    weight: 600.0,
    tracking: -0.15,
    ..type_::BODY
};

/// The inspector's title and the kicker under it, `.ledger-inspector-head`.
pub(crate) fn head(title: &str, kicker: Option<El>, t: &Tokens) -> El {
    col()
        .gap(3.0)
        .child(
            text(title.to_owned())
                .role("heading")
                .font(INSPECTOR_TITLE)
                .color(t.text_primary)
                .wrap(),
        )
        .children(kicker)
}

/// The kicker, `.ledger-inspector-kicker`: the row's mark before its words.
pub(crate) fn kicker(mark: Glyph, words: &str, t: &Tokens) -> El {
    row()
        .key("ledger.inspector.kicker")
        .items_center()
        .gap(6.0)
        .child(glyph(mark, t))
        .child(
            text(words.to_owned())
                .font(META)
                .color(t.text_tertiary)
                .wrap(),
        )
}

const FIELD_LABEL: TextStyle = TextStyle {
    weight: 600.0,
    tracking: 0.5,
    ..type_::SANS_CAPS
};

pub(crate) fn label(words: &str, t: &Tokens) -> El {
    type_::upper(words, FIELD_LABEL).color(t.text_dimmed)
}

pub(crate) fn body(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::BODY)
        .color(t.text_primary)
        .wrap()
}

pub(crate) fn muted(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(META)
        .color(t.text_tertiary)
        .wrap()
}

/// A labelled group of the inspector, `.ledger-field`, whose value its caller adds.
pub(crate) fn group(key: &str, name: &str, t: &Tokens) -> El {
    col()
        .key(format!("ledger.inspector.{key}"))
        .role("group")
        .label(name.to_owned())
        .gap(2.0)
        .child(label(name, t))
}

/// A field whose value is plain text.
pub(crate) fn field(name: &str, value: &str, t: &Tokens) -> El {
    group(&name.to_lowercase().replace(' ', "-"), name, t).child(body(value, t))
}

/// A value with an aside after it in smaller muted text, as a date and its age.
pub(crate) fn value_with(value: &str, aside: &str, t: &Tokens) -> El {
    let line = format!("{value} {aside}");
    let start = value.len() + 1;
    rune::rich(
        line,
        vec![
            rune::Span::new(start, start + aside.len())
                .size(META.size)
                .color(t.text_tertiary),
        ],
    )
    .font(type_::BODY)
    .color(t.text_primary)
    .wrap()
}

const STATUS: TextStyle = TextStyle {
    size: 11.0,
    ..type_::MONO_CAPTION
};

/// The status bar's parts, `.ledger-status-left`: each after a dot.
pub(crate) fn status_line(parts: Vec<El>, t: &Tokens) -> El {
    let mut line = row().items_center().min_w(0.0);
    for (at, part) in parts.into_iter().enumerate() {
        if at > 0 {
            line = line.child(text("·").font(STATUS).color(t.text_muted).px(8.0));
        }
        line = line.child(part);
    }
    line
}

/// Status text in the status bar's face.
pub(crate) fn status_text(words: String, ink: Color) -> El {
    text(words).font(STATUS).color(ink).single_line()
}

/// A press in the status bar, `.ledger-status-link`: its words, no frame.
pub(crate) fn status_link(key: &str, words: String, t: &Tokens) -> El {
    let face = words.clone();
    let ink = t.text_tertiary;
    Button::new(words)
        .variant(ButtonVariant::GhostMuted)
        .label_with(move |_, _| text(face).font(STATUS).color(ink).single_line())
        .build(attn_ui::kit(t))
        .key(key.to_owned())
        .h(STATUS.line_px())
        .px(0.0)
}

const TAB: TextStyle = TextStyle {
    size: 15.0,
    weight: 600.0,
    tracking: -0.15,
    ..type_::BODY
};

/// A list's tab in the header, `.ledger-tabs button`: raised while shown.
pub(crate) fn tab(key: String, words: &str, on: bool, t: &Tokens) -> El {
    let face = words.to_owned();
    let ink = if on { t.text_primary } else { t.text_tertiary };
    Button::new(words)
        .variant(ButtonVariant::GhostMuted)
        .label_with(move |_, _| text(face).font(TAB).color(ink).single_line())
        .build(attn_ui::kit(t))
        .key(key)
        .a11y_value(if on { "pressed" } else { "" })
        .h(TAB.line_px() + 10.0)
        .px(10.0)
        .radius(6.0)
        .fill(if on {
            t.bg_elevated
        } else {
            Color::TRANSPARENT
        })
}
