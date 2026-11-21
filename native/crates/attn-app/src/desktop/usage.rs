use attn_protocol::{Session, SessionUsage, SessionUsageModel};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Card, HeaderPiece, Inline, Popover, Stack};
use attn_ui::system::{Type, keys_hint, words};
use rune::{El, Tx};
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
pub(crate) fn piece(leaf: &str, session: &str, badge: &Badge, open: bool, t: &Tokens) -> El {
    let pressed = (leaf.to_owned(), session.to_owned());
    let ink = if open {
        t.text_primary
    } else {
        t.text_tertiary
    };
    let badge = HeaderPiece::new()
        .words(badge.text.clone(), ink)
        .button(
            header_pop::anchor(leaf, What::Usage),
            badge.label.clone(),
            t,
        )
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::press(state, &pressed.0, &pressed.1, What::Usage, tx)
        })
        .expanded(open);
    let badge = if open { badge.fill(t.bg_button) } else { badge };
    header_pop::hovers(badge, leaf, session, What::Usage)
}

/// The badge's width.
pub(super) fn width(badge: &Badge) -> super::fit::Widths {
    super::fit::Widths {
        full: HeaderPiece::new()
            .words(badge.text.clone(), rune::Color::TRANSPARENT)
            .button_width(),
        short: None,
        icon: None,
    }
}

/// How wide the breakdown stands: its counts in two columns, "Cache write, unknown" beside a
/// count of seven digits, keep one line each.
const BREAKDOWN_W: f32 = 390.0;

/// The breakdown of the open usage popover, under its badge.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let usage = shown(session)?;
    let key = pop.surface();
    let total = words(
        format!("{} tokens", grouped(usage.total_tokens)),
        Type::Small,
        t.text_tertiary,
    );
    let mut breakdown = Popover::new(key.clone(), "Session usage")
        .push(Block::custom(format!("{key}.total"), total))
        .push_all(
            usage
                .models
                .iter()
                .enumerate()
                .map(|(i, m)| model(&format!("{key}.model.{i}"), m, t)),
        );
    if let Some(cost) = usage.cost_usd {
        breakdown = breakdown.aside(words(
            format!(
                "{}{}",
                dollars(cost),
                unpriced_mark(usage.has_unpriced_usage)
            ),
            Type::Name,
            t.text_primary,
        ));
    }
    if pop.pinned {
        breakdown = breakdown.actions(Actions::new("close").push(keys_hint(["esc"], "close", t)));
    }
    let surface = breakdown
        .build(t)
        .label("Session usage breakdown")
        .w(BREAKDOWN_W)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        });
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
        surface,
        &pop.anchor(),
        Placement::BottomEnd,
        4.0,
    ))
}

/// One model's card: its name and total over its counts in two columns.
fn model(key: &str, model: &SessionUsageModel, t: &Tokens) -> Block {
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
    let head = Inline::joined()
        .w_full()
        .child(
            words(name, Type::Name, t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0)
                .min_w(0.0),
        )
        .child(words(
            format!("{} tokens{cost}", grouped(model.total_tokens)),
            Type::Small,
            t.text_tertiary,
        ));
    let count = |label: &str, value: i64| {
        Inline::joined()
            .w_frac(0.5)
            .shrink()
            .min_w(0.0)
            .child(
                words(label.to_owned(), Type::Small, t.text_tertiary)
                    .single_line()
                    .grow(1.0),
            )
            .child(words(grouped(value), Type::Small, t.text_secondary))
    };
    let pair = |left: El, right: El| Inline::top().child(left).child(right);
    let counts = Stack::tight()
        .w_full()
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
        ));
    let unpriced = model.has_unpriced_usage.then(|| {
        let reason = model
            .unpriced_reason
            .as_deref()
            .filter(|r| !r.is_empty())
            .unwrap_or("Some usage has no price.");
        words(format!("* {reason}"), Type::Small, t.text_tertiary).wrap()
    });
    let card = Card::new(key)
        .push(head)
        .push(counts)
        .push_all(unpriced)
        .build(t);
    Block::custom(format!("{key}.card"), card)
}
