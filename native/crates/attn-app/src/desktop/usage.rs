use attn_protocol::{Session, SessionUsage, SessionUsageModel};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::{El, Tx, col, row, text};
use rune_kit::Placement;

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
    session.usage.as_ref().filter(|u| measured(u))
}

/// Whether `usage` is measured whole and has any, so a badge can say it.
pub(crate) fn measured(usage: &SessionUsage) -> bool {
    usage.measurement_incomplete != Some(true) && usage.total_tokens > 0 && !usage.models.is_empty()
}

pub(crate) fn badge(session: &Session) -> Option<Badge> {
    shown(session).map(badge_of)
}

/// The badge of `usage`, measured or not.
pub(crate) fn badge_of(usage: &SessionUsage) -> Badge {
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
    Badge {
        label: format!("Session usage {text}{detail}"),
        text,
    }
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

pub(crate) fn grouped(n: i64) -> String {
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
/// on a press, lit while its breakdown is open.
pub(crate) fn piece(
    leaf: &str,
    session: &str,
    badge: &Badge,
    open: bool,
    font: rune::TextStyle,
    t: &Tokens,
) -> El {
    let pressed = (leaf.to_owned(), session.to_owned());
    let ink = if open {
        t.text_primary
    } else {
        t.text_tertiary
    };
    let badge = header_pop::link(
        badge.label.clone(),
        badge.text.clone(),
        font,
        ink,
        t,
        move |state, tx| header_pop::press(state, &pressed.0, &pressed.1, What::Usage, tx),
    )
    .key(header_pop::anchor(leaf, What::Usage))
    .expanded(open);
    let badge = if open { badge.fill(t.bg_button) } else { badge };
    header_pop::hovers(badge, leaf, session, What::Usage)
}

/// The badge's width in `font`.
pub(super) fn width(badge: &Badge, font: rune::TextStyle) -> super::fit::Widths {
    super::fit::Widths {
        full: super::fit::text_w(&badge.text, font),
        short: None,
        icon: None,
    }
}

/// The weight of `.session-usage-summary strong` and `.session-usage-model header strong`.
const STRONG: rune::TextStyle = rune::TextStyle {
    weight: 600.0,
    ..type_::LABEL
};

/// The breakdown of the open usage popover, under its badge.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let usage = shown(session)?;
    let summary = row()
        .w_full()
        .padding(10.0)
        .gap(12.0)
        .child(
            col()
                .grow(1.0)
                .gap(4.0)
                .child(text("Session usage").font(STRONG).color(t.text_primary))
                .child(caption(
                    &format!("{} tokens", grouped(usage.total_tokens)),
                    t,
                )),
        )
        .children(usage.cost_usd.map(|cost| {
            text(format!(
                "{}{}",
                dollars(cost),
                unpriced_mark(usage.has_unpriced_usage)
            ))
            .font(STRONG)
            .color(t.text_primary)
        }));
    let surface = col()
        .key(pop.surface())
        .role("dialog")
        .label("Session usage breakdown")
        .w(390.0)
        .padding(6.0)
        .focusable(true)
        .no_focus_ring()
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        })
        .child(summary)
        .child(
            col()
                .w_full()
                .gap(6.0)
                .children(usage.models.iter().map(|m| model(m, t))),
        )
        .children(
            pop.pinned
                .then(|| caption("esc close", t).pt(8.0).px(10.0).pb(3.0)),
        );
    let surface = if pop.pinned {
        surface
            .modal_tx(true, |state: &mut State, _: &mut Tx| {
                header_pop::close(state)
            })
            .modal_press_through(true)
    } else {
        surface
    };
    Some(rune_kit::popover::anchored(
        attn_ui::kit(t),
        attn_ui::Frame::panel(t).draw(surface),
        &pop.anchor(),
        Placement::BottomEnd,
        4.0,
    ))
}

/// One model's card, `.session-usage-model`: its name and total over its counts in two
/// columns.
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
            .w_frac(0.5)
            .shrink()
            .min_w(0.0)
            .gap(8.0)
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
    let pair = |left: El, right: El| row().w_full().gap(18.0).child(left).child(right);
    col()
        .w_full()
        .padding(10.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .radius(7.0)
        .child(
            row()
                .w_full()
                .items_center()
                .gap(12.0)
                .child(
                    text(name)
                        .font(STRONG)
                        .color(t.text_primary)
                        .truncate()
                        .grow(1.0),
                )
                .child(caption(
                    &format!("{} tokens{cost}", grouped(model.total_tokens)),
                    t,
                )),
        )
        .child(
            col()
                .w_full()
                .mt(10.0)
                .gap(7.0)
                .child(pair(
                    count("Input", model.input_tokens),
                    count("Output", model.output_tokens),
                ))
                .child(pair(
                    count("Cache read", model.cache_read_tokens),
                    count("Cache write, 5m", model.cache_write_5m_tokens),
                ))
                .child(pair(
                    count("Cache write, 1h", model.cache_write_1h_tokens),
                    count(
                        "Cache write, unknown",
                        model.cache_write_unclassified_tokens,
                    ),
                )),
        )
        .children(model.has_unpriced_usage.then(|| {
            let reason = model
                .unpriced_reason
                .as_deref()
                .filter(|r| !r.is_empty())
                .unwrap_or("Some usage has no price.");
            caption(&format!("* {reason}"), t).mt(9.0)
        }))
}

fn caption(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(attn_ui::kit(t).ty.caption())
        .color(t.text_tertiary)
}
