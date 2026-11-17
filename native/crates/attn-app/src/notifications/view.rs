use attn_client::Notifications;
use attn_protocol::Notification;
use attn_ui::frame::{self, Frame};
use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens, glyph, icon};
use rune::tok::space;
use rune::{El, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, Collapsible, ControlSize, Placement, popover};

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
    let dot = (counts.unread > 0).then_some(if counts.unread_critical > 0 {
        t.error
    } else {
        t.brand
    });
    let ring = t.bg_panel;
    let bell = Button::new(label)
        .variant(ButtonVariant::GhostMuted)
        .expanded(open)
        .label_with(move |_, ink| {
            rune::icon(&icon::BELL, 13.0)
                .color(ink)
                .children(dot.map(|dot| unread_dot(dot, ring)))
        })
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::toggle(state, tx))
        .build(attn_ui::kit(t))
        .key(BELL)
        .a11y_value(counts.unread.to_string())
        .h(crate::sidebar::TITLE_BUTTON)
        .min_w(crate::sidebar::TITLE_BUTTON)
        .radius(6.0)
        .hover(t.bg_elevated);
    let bell = crate::sidebar::framed(if open { bell.fill(t.bg_elevated) } else { bell }, t);
    crate::shell::title_bar_slot(BELL_SLOT, bell)
}

/// The Tauri app's `.title-bar-notification-dot`: 6 points, 2 points in from the button's top
/// right corner, ringed in the title bar's fill.
fn unread_dot(dot: rune::Color, ring: rune::Color) -> El {
    row()
        .pin(None, Some(-5.5), Some(-5.5), None)
        .size(10.0, 10.0)
        .radius(5.0)
        .fill(ring)
        .center()
        .inert()
        .child(el().size(6.0, 6.0).radius(3.0).fill(dot))
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
    let shown = badge.clone();
    let t = *t;
    Some(
        Button::new(name)
            .variant(ButtonVariant::Ghost)
            .label_with(move |font, _| {
                row()
                    .w_full()
                    .items_center()
                    .gap(space::S2)
                    .child(el().size(8.0, 8.0).radius(4.0).fill(t.error))
                    .child(
                        text(title)
                            .font(font)
                            .color(t.text_primary)
                            .single_line()
                            .truncate()
                            .grow(1.0),
                    )
                    .children(shown.map(|n| text(n).font(type_::MONO_CAPTION).color(t.error)))
            })
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::open(state, tx))
            .build(attn_ui::kit(&t))
            .key("sidebar.critical")
            .a11y_value(badge.unwrap_or_default())
            .w_full()
            .fill(t.error.alpha(0.12)),
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
    let header = row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S3)
        .py(11.0)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            type_::upper("Notifications", type_::BOLD_CAPS)
                .color(t.text_primary)
                .grow(1.0),
        )
        .child(
            Button::new("Mark all read")
                .variant(ButtonVariant::Ghost)
                .control_size(ControlSize::Small)
                .label_with({
                    let ink = t.text_secondary;
                    move |_, _| text("Mark all read").font(type_::CAPTION).color(ink)
                })
                .disabled(unread == 0)
                .on_press_tx(|state: &mut State, tx: &mut Tx| super::mark_all_read(state, tx))
                .build(k)
                .key("notifications.mark-all")
                .radius(6.0)
                .border(1.0, t.border),
        )
        .child(
            GlyphButton::new(glyph::TIMES, "Close")
                .font(glyph::LARGE)
                .color(t.text_muted)
                .on_press_tx(|state: &mut State, tx: &mut Tx| super::close(state, tx))
                .build(k)
                .key("notifications.close"),
        );
    let body = col()
        .w_full()
        .min_h(0.0)
        .shrink()
        .child(popover::fit_scroll(
            col()
                .key("notifications.list")
                .role("list")
                .label("Notifications")
                .w_full()
                .padding(space::S2)
                .gap(space::S1)
                .children(body(state, k, t)),
        ));
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
        .max_h(MAX_HEIGHT)
        .child(header)
        .child(body);
    popover::anchored(
        k,
        Frame {
            fill: t.bg_elevated,
            radius: 12.0,
            shadow: frame::shadow(t, 18.0, 48.0, 0.36),
            ..Frame::menu(t)
        }
        .draw(surface),
        crate::shell::TITLE_BAR_LEFT,
        Placement::BottomStart,
        8.0,
    )
}

fn body(state: &State, k: &rune_kit::Kit, t: &Tokens) -> Vec<El> {
    let panel = &state.window.notifications;
    let note = |words: String| {
        text(words)
            .key("notifications.state")
            .font(type_::BODY)
            .color(t.text_secondary)
            .wrap()
    };
    if let Some(error) = &panel.error {
        return vec![
            row()
                .w_full()
                .items_center()
                .gap(space::S2)
                .py(space::S2)
                .child(note(error.clone()).grow(1.0))
                .child(
                    Button::new("Try again")
                        .control_size(ControlSize::Small)
                        .on_press_tx(|state: &mut State, tx: &mut Tx| super::read(state, tx))
                        .build(k)
                        .key("notifications.retry"),
                ),
        ];
    }
    let Some(list) = &panel.list else {
        return vec![note("Loading…".into()).py(space::S2)];
    };
    if list.is_empty() {
        return vec![
            row()
                .w_full()
                .justify_center()
                .py(16.0)
                .child(note("No notifications.".into()).color(t.text_dimmed)),
        ];
    }
    list.iter().map(|n| notification(state, n, k, t)).collect()
}

fn notification(state: &State, n: &Notification, k: &rune_kit::Kit, t: &Tokens) -> El {
    let panel = &state.window.notifications;
    let key = format!("notifications.row.{}", n.id);
    let expanded = panel.expanded.as_deref() == Some(n.id.as_str());
    let unread = n.read_at.is_empty();
    let severity = Severity::of(n);
    let tone = match severity {
        Severity::Critical => t.error,
        Severity::Warning => t.warning,
        Severity::Info => t.accent,
    };
    let ago = crate::garden::model::ago(&n.created_at, state.window.now_ms);
    let (title, word) = (n.title.clone(), severity.word());
    let id = n.id.clone();
    let tt = *t;
    let head = Button::new(format!("{} · {word} · {ago}", n.title))
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Large)
        .label_with(move |font, _| {
            row()
                .w_full()
                .items_center()
                .gap(space::S2)
                .child(el().size(6.0, 6.0).radius(3.0).fill(if unread {
                    tone
                } else {
                    rune::Color::TRANSPARENT
                }))
                .child(
                    text(title)
                        .font(font)
                        .color(if unread {
                            tt.text_primary
                        } else {
                            tt.text_secondary
                        })
                        .single_line()
                        .truncate()
                        .grow(1.0),
                )
                .child(text(word).font(type_::MONO_CAPTION).color(tone))
                .child(text(ago).font(type_::LABEL).color(tt.text_tertiary))
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::toggle_row(state, &id, tx))
        .build(k)
        .expanded(expanded)
        .key(format!("{key}.head"))
        .w_full();
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
    col()
        .key(key.clone())
        .role("listitem")
        .label(n.title.clone())
        .a11y_value(value)
        .w_full()
        .radius(6.0)
        .fill(if expanded {
            t.bg_elevated
        } else {
            rune::Color::TRANSPARENT
        })
        .child(head)
        .children(if expanded {
            Some(detail(state, n, &key, k, t))
        } else {
            (!preview.is_empty()).then(|| {
                text(preview.clone())
                    .key(format!("{key}.preview"))
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .wrap()
                    .px(space::S3)
                    .pb(space::S1)
            })
        })
}

fn detail(state: &State, n: &Notification, key: &str, k: &rune_kit::Kit, t: &Tokens) -> El {
    let fact = |part: &str, heading: Option<&str>, words: &str| {
        col()
            .key(format!("{key}.{part}"))
            .w_full()
            .gap(2.0)
            .children(heading.map(|h| text(h).font(type_::LABEL).color(t.text_tertiary)))
            .child(
                text(words.to_owned())
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .wrap(),
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
            Collapsible::new("Diagnostic output", state.window.notifications.diagnostic)
                .ghost()
                .child(output(&n.diagnostic, t))
                .on_toggle(|state: &mut State| super::toggle_diagnostic(state))
                .build(k)
                .key(format!("{key}.diagnostic")),
        );
    } else if n.cause.is_empty() && !n.detail.is_empty() {
        parts.push(output(&n.detail, t).key(format!("{key}.error")));
    }
    let actions: Vec<El> = n
        .actions
        .iter()
        .flatten()
        .filter_map(|a| Some((a, Act::of(n, &a.kind, &a.target_id)?)))
        .enumerate()
        .map(|(i, (a, act))| action(state, n, &a.label, act, k).key(format!("{key}.action.{i}")))
        .collect();
    if !actions.is_empty() {
        parts.push(row().w_full().gap(space::S2).flex_wrap().children(actions));
    }
    col()
        .key(format!("{key}.detail"))
        .w_full()
        .px(space::S3)
        .pb(space::S2)
        .gap(space::S2)
        .children(parts)
}

fn output(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::MONO_CAPTION)
        .color(t.text_secondary)
        .wrap()
        .w_full()
        .padding(space::S2)
        .radius(4.0)
        .fill(t.bg_app)
}

fn action(state: &State, n: &Notification, label: &str, act: Act, k: &rune_kit::Kit) -> El {
    let panel = &state.window.notifications;
    let (label, busy) = match &act {
        Act::Retry { .. } if panel.retrying.contains(&n.id) => ("Retrying…".to_owned(), true),
        Act::InstallCodexUpdate if panel.installing_codex => ("Installing…".to_owned(), true),
        _ => (label.to_owned(), false),
    };
    Button::new(label)
        .control_size(ControlSize::Small)
        .disabled(busy)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::run(state, act.clone(), tx))
        .build(k)
}
