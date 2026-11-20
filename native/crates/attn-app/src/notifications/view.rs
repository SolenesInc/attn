use attn_client::Notifications;
use attn_protocol::Notification;
use attn_ui::blocks::{Actions, Block, Disclosure, List, Notice, Prose, Sheet, Stack};
use attn_ui::frame::Frame;
use attn_ui::system::{
    Button, Control, DotShape, DotSize, IconSize, ListRow, Size, Tone, Type, dot, icon_button, tag,
    words,
};
use attn_ui::{Tokens, glyph, icon};
use rune::{El, Tx, col};
use rune_kit::{Placement, popover};

use super::{Act, Severity};
use crate::State;

pub(crate) const PANEL: &str = "notifications.panel";
const BELL: &str = "titlebar.notifications";
const BELL_SLOT: &str = "titlebar.notifications.slot";
const WIDTH: f32 = 360.0;
const MAX_HEIGHT: f32 = 520.0;

/// The title bar's bell: it opens or closes the panel, and marks unread and critical
/// notifications.
pub(crate) fn title_button(state: &State, t: &Tokens) -> El {
    let counts = &state.facts.notifications;
    let open = super::is_open(state);
    let label = if open {
        "Hide Notifications"
    } else {
        "Show Notifications"
    };
    let unread = (counts.unread > 0).then_some(if counts.unread_critical > 0 {
        t.error
    } else {
        t.brand
    });
    let bell = El::from(
        icon_button(BELL, label, &icon::BELL, IconSize::Large)
            .expanded(open)
            .dot(unread)
            .edged()
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::toggle(state, tx))
            .build(t),
    )
    .a11y_value(counts.unread.to_string());
    crate::shell::title_bar_slot(BELL_SLOT, bell)
}

/// The strip that names the newest unread critical notification above the sidebar's rows,
/// while one is unread.
pub(crate) fn critical_strip(counts: &Notifications, t: &Tokens) -> Option<El> {
    let count = counts.unread_critical;
    if count == 0 {
        return None;
    }
    let title = counts
        .critical_title
        .clone()
        .filter(|title| !title.is_empty())
        .unwrap_or_else(|| "Critical notification".into());
    let name = if count == 1 {
        format!("1 unread critical notification: {title}. Open notifications.")
    } else {
        format!("{count} unread critical notifications, newest: {title}. Open notifications.")
    };
    let badge = (count > 1).then(|| count.to_string());
    let mut row = ListRow::new("sidebar.critical", name)
        .lead(dot(t.error, DotShape::Filled, DotSize::Regular))
        .title(title)
        .tint(Some(t.error));
    if let Some(n) = &badge {
        row = row.aside(words(n.clone(), Type::Mono, t.error));
    }
    Some(
        row.build(t)
            .a11y_value(badge.unwrap_or_default())
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::open(state, tx)),
    )
}

pub(crate) fn panel(state: &State, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let panel = &state.window.notifications;
    let unread = panel
        .list
        .iter()
        .flatten()
        .filter(|n| n.read_at.is_empty())
        .count();
    let mark_all = Button::new("Mark all read")
        .size(Size::Compact)
        .disabled(unread == 0)
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::mark_all_read(state, tx))
        .build(t)
        .key("notifications.mark-all");
    let close = icon_button(
        "notifications.close",
        "Close",
        glyph::TIMES,
        IconSize::Large,
    )
    .on_press_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx))
    .build(t);
    let sheet = Sheet::new("notifications.sheet", "Notifications")
        .fits(MAX_HEIGHT)
        .aside(mark_all)
        .close(close)
        .push(body(state, t))
        .build(t);
    let surface = col()
        .key(PANEL)
        .role("dialog")
        .label("Notifications")
        .focusable(true)
        .no_focus_ring()
        .focus_return(BELL)
        .modal_tx(true, |state: &mut State, tx: &mut Tx| {
            super::close(state, tx)
        })
        .w(WIDTH)
        .child(sheet);
    popover::anchored(
        k,
        Frame::panel(t).draw(surface),
        crate::shell::TITLE_BAR_LEFT,
        Placement::BottomStart,
        8.0,
    )
}

fn body(state: &State, t: &Tokens) -> Block {
    let panel = &state.window.notifications;
    if let Some(error) = &panel.error {
        return Notice::error("notifications.state", error.clone())
            .action(
                Button::new("Try again")
                    .size(Size::Compact)
                    .on_press_tx(|state: &mut State, tx: &mut Tx| super::read(state, tx))
                    .build(t)
                    .key("notifications.retry"),
            )
            .build(t);
    }
    let Some(list) = &panel.list else {
        return Prose::lead("notifications.state", "Loading…")
            .quiet(true)
            .build(t);
    };
    list.iter()
        .fold(
            List::new("notifications.list", "Notifications"),
            |shown, n| {
                let (row, under) = notification(state, n, t);
                match under {
                    Some((key, under)) => shown.row_with_keyed(row, key, under),
                    None => shown.row(row.build(t)),
                }
            },
        )
        .empty("notifications.state", "No notifications.", None)
        .build(t)
}

fn notification(
    state: &State,
    n: &Notification,
    t: &Tokens,
) -> (ListRow, Option<(String, Vec<Block>)>) {
    let panel = &state.window.notifications;
    let key = format!("notifications.row.{}", n.id);
    let expanded = panel.expanded.as_deref() == Some(n.id.as_str());
    let unread = n.read_at.is_empty();
    let severity = Severity::of(n);
    let tone = match severity {
        Severity::Critical => Tone::Bad,
        Severity::Warning => Tone::Warn,
        Severity::Info => Tone::Neutral,
    };
    let ago = crate::garden::model::ago(&n.created_at, state.window.now_ms);
    let word = severity.word();
    let id = n.id.clone();
    let preview = if n.impact.is_empty() {
        &n.body
    } else {
        &n.impact
    };
    let value = if unread {
        format!("{word}, unread")
    } else {
        word.to_owned()
    };
    let mut row = ListRow::new(key.clone(), n.title.clone())
        .tag(tag(format!("{key}.severity"), word, tone, t))
        .aside(words(ago.clone(), Type::Small, t.text_tertiary))
        .dimmed(!unread)
        .expanded(expanded)
        .select(
            format!("{key}.head"),
            format!("{} · {word} · {ago}", n.title),
            move |state: &mut State, tx: &mut Tx| super::toggle_row(state, &id, tx),
        );
    row = if unread {
        row.title(n.title.clone())
    } else {
        row.quiet_title(n.title.clone())
    };
    let row = row.item_value(value);
    let under = if expanded {
        Some((format!("{key}.detail"), detail(state, n, &key, t)))
    } else {
        (!preview.is_empty()).then(|| {
            let said = words(preview.clone(), Type::Small, t.text_secondary)
                .key(format!("{key}.preview"))
                .label(preview.clone())
                .wrap();
            (
                format!("{key}.under"),
                vec![Block::custom(format!("{key}.preview.block"), said)],
            )
        })
    };
    (row, under)
}

fn detail(state: &State, n: &Notification, key: &str, t: &Tokens) -> Vec<Block> {
    let fact = |part: &str, heading: Option<&str>, said: &str| {
        Block::custom(
            format!("{key}.{part}.block"),
            Stack::tight()
                .key(format!("{key}.{part}"))
                .w_full()
                .children(heading.map(|h| words(h.to_owned(), Type::Label, t.text_tertiary)))
                .child(words(said.to_owned(), Type::Body, t.text_primary).wrap_anywhere()),
        )
    };
    let mut parts = Vec::new();
    if !n.trigger.is_empty() {
        parts.push(fact("trigger", Some("Trigger"), &n.trigger));
    }
    if !n.impact.is_empty() {
        parts.push(fact("impact", Some("Impact"), &n.impact));
    } else if !n.body.is_empty() {
        parts.push(fact("impact", None, &n.body));
    }
    if !n.cause.is_empty() {
        parts.push(fact("cause", Some("Cause"), &n.cause));
    }
    if !n.diagnostic.is_empty() {
        parts.push(
            Disclosure::new(
                format!("{key}.diagnostic"),
                "Diagnostic output",
                state.window.notifications.diagnostic,
                |state: &mut State, _: &mut Tx| super::toggle_diagnostic(state),
            )
            .push(Block::custom(
                format!("{key}.diagnostic.output"),
                output(&n.diagnostic, t),
            ))
            .build(t),
        );
    } else if n.cause.is_empty() && !n.detail.is_empty() {
        parts.push(Block::custom(
            format!("{key}.error.block"),
            output(&n.detail, t).key(format!("{key}.error")),
        ));
    }
    let actions = n
        .actions
        .iter()
        .flatten()
        .filter_map(|a| Some((a, Act::of(n, &a.kind, &a.target_id)?)))
        .enumerate()
        .fold(
            Actions::new(format!("{key}.actions")),
            |actions, (i, (a, act))| {
                actions.push(action(state, n, &a.label, act, t).key(format!("{key}.action.{i}")))
            },
        );
    parts.push(actions.build());
    parts
}

fn output(said: &str, t: &Tokens) -> El {
    words(said.to_owned(), Type::Mono, t.text_secondary).wrap_anywhere()
}

fn action(state: &State, n: &Notification, label: &str, act: Act, t: &Tokens) -> Control {
    let panel = &state.window.notifications;
    let (label, busy) = match &act {
        Act::Retry { .. } if panel.retrying.contains(&n.id) => ("Retrying…".to_owned(), true),
        Act::InstallCodexUpdate if panel.installing_codex => ("Installing…".to_owned(), true),
        _ => (label.to_owned(), false),
    };
    Button::new(label)
        .size(Size::Compact)
        .disabled(busy)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run(state, act.clone(), tx))
        .build(t)
}
