use attn_protocol::{Session, SessionUsage, SessionUsageModel};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Side, Tx, col, row, text};

use super::header_pop::{self, Pop, What};
use crate::State;

/// What the header says of a session's usage: its badge, and how it reads aloud.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Badge {
    pub(crate) text: String,
    pub(crate) label: String,
}

/// The usage a session's header shows, once it is measured whole and has any.
pub(crate) fn shown(session: &Session) -> Option<&SessionUsage> {
    session.usage.as_ref().filter(|u| {
        u.measurement_incomplete != Some(true) && u.total_tokens > 0 && !u.models.is_empty()
    })
}

pub(crate) fn badge(session: &Session) -> Option<Badge> {
    let usage = shown(session)?;
    let text = match usage.cost_usd {
        Some(cost) => format!(
            "{}{}",
            dollars(cost),
            unpriced_mark(usage.has_unpriced_usage)
        ),
        None => compact_tokens(usage.total_tokens),
    };
    let detail = if usage.has_unpriced_usage {
        ", some usage has no price"
    } else {
        ""
    };
    Some(Badge {
        label: format!("Session usage {text}{detail}"),
        text,
    })
}

fn unpriced_mark(unpriced: bool) -> &'static str {
    if unpriced { "*" } else { "" }
}

fn dollars(cost: f64) -> String {
    if cost > 0.0 && cost < 0.01 {
        return "<$0.01".into();
    }
    let cents = (cost * 100.0).round() as i64;
    format!("${}.{:02}", grouped(cents / 100), cents % 100)
}

fn compact_tokens(tokens: i64) -> String {
    let compact = |divisor: f64, unit: &str| {
        let scaled = tokens as f64 / divisor;
        let digits = if scaled >= 100.0 || scaled.fract() == 0.0 {
            0
        } else {
            1
        };
        let number = format!("{scaled:.digits$}");
        format!(
            "{}{unit} tokens",
            number.strip_suffix(".0").unwrap_or(&number)
        )
    };
    match tokens {
        t if t >= 1_000_000_000 => compact(1e9, "b"),
        t if t >= 1_000_000 => compact(1e6, "m"),
        t if t >= 1_000 => compact(1e3, "k"),
        t => format!("{} tokens", grouped(t)),
    }
}

fn grouped(n: i64) -> String {
    let digits = n.abs().to_string();
    let mut out = String::new();
    for (i, c) in digits.chars().enumerate() {
        if i > 0 && (digits.len() - i).is_multiple_of(3) {
            out.push(',');
        }
        out.push(c);
    }
    if n < 0 { format!("-{out}") } else { out }
}

/// The usage badge in the header of `leaf`, which previews the breakdown on hover and pins it
/// on a press.
pub(crate) fn piece(
    leaf: &str,
    session: &str,
    badge: &Badge,
    font: rune::TextStyle,
    t: &Tokens,
) -> El {
    let (hovered, pressed) = (
        (leaf.to_owned(), session.to_owned()),
        (leaf.to_owned(), session.to_owned()),
    );
    text(badge.text.clone())
        .key(header_pop::anchor(leaf, What::Usage))
        .role("button")
        .label(badge.label.clone())
        .font(font)
        .color(t.text_tertiary)
        .single_line()
        .on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover(state, &hovered.0, &hovered.1, What::Usage, inside, tx)
        })
        .on_press(move |state: &mut State| {
            header_pop::press(state, &pressed.0, &pressed.1, What::Usage)
        })
}

/// The breakdown of the open usage popover, under its badge.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let usage = shown(session)?;
    let summary = row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(
            text("Session usage")
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .child(
            text(format!("{} tokens", grouped(usage.total_tokens)))
                .font(type_::LABEL)
                .color(t.text_secondary)
                .grow(1.0),
        )
        .children(usage.cost_usd.map(|cost| {
            text(format!(
                "{}{}",
                dollars(cost),
                unpriced_mark(usage.has_unpriced_usage)
            ))
            .font(type_::LABEL)
            .color(t.text_primary)
        }));
    let surface = col()
        .key(format!("{}.breakdown", pop.anchor()))
        .role("dialog")
        .label("Session usage breakdown")
        .w(300.0)
        .padding(space::S2)
        .gap(space::S2)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        })
        .child(summary)
        .children(usage.models.iter().map(|m| model(m, t)))
        .children(
            pop.pinned
                .then(|| text("esc close").font(type_::LABEL).color(t.text_muted)),
        );
    let surface = if pop.pinned {
        surface.modal_tx(true, |state: &mut State, _: &mut Tx| {
            header_pop::close(state)
        })
    } else {
        surface
    };
    Some(surface.anchored(pop.anchor(), Side::Bottom, rune::Align::End, 4.0))
}

fn model(model: &SessionUsageModel, t: &Tokens) -> El {
    let name = if model.purpose == "guardian" {
        format!("Guardian · {}", model.model)
    } else {
        model.model.clone()
    };
    let cost = model
        .cost_usd
        .map(|c| {
            format!(
                " · {}{}",
                dollars(c),
                unpriced_mark(model.has_unpriced_usage)
            )
        })
        .unwrap_or_default();
    let count = |label: &str, value: i64| {
        row()
            .w_full()
            .child(
                text(label.to_owned())
                    .font(type_::LABEL)
                    .color(t.text_tertiary)
                    .grow(1.0),
            )
            .child(
                text(grouped(value))
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
    };
    col()
        .w_full()
        .gap(2.0)
        .child(
            row()
                .w_full()
                .gap(space::S2)
                .child(
                    text(name)
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(
                    text(format!("{} tokens{cost}", grouped(model.total_tokens)))
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                ),
        )
        .child(count("Input", model.input_tokens))
        .child(count("Output", model.output_tokens))
        .child(count("Cache read", model.cache_read_tokens))
        .child(count("Cache write, 5m", model.cache_write_5m_tokens))
        .child(count("Cache write, 1h", model.cache_write_1h_tokens))
        .child(count(
            "Cache write, unknown",
            model.cache_write_unclassified_tokens,
        ))
        .children(model.has_unpriced_usage.then(|| {
            let reason = model
                .unpriced_reason
                .as_deref()
                .filter(|r| !r.is_empty())
                .unwrap_or("Some usage has no price.");
            text(format!("* {reason}"))
                .font(type_::LABEL)
                .color(t.text_tertiary)
        }))
}
