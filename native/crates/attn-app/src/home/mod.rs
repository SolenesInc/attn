mod model;

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, ScrollSpec, Tx, col, memo, row, text};

use crate::asks::Item as AskItem;
use crate::sidebar::{open_agent, state_mark};
use crate::{Action, State};
use model::{Group, Home, HomeAgent};

pub(crate) use model::next_change;

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let window = &state.window;
    let reads = (
        std::sync::Arc::as_ptr(&facts.sessions) as usize,
        std::sync::Arc::as_ptr(&facts.desktops) as usize,
        std::sync::Arc::as_ptr(&facts.profiles) as usize,
        std::sync::Arc::as_ptr(&facts.settings) as usize,
        std::sync::Arc::as_ptr(&facts.asks) as usize,
        std::sync::Arc::as_ptr(&facts.garden) as usize,
        window.now_ms,
        window.follow_next_turn,
        window.home_snoozed_open,
        *t,
    );
    memo("home", reads, || {
        let home = model::home(facts, window.now_ms);
        let asks = crate::asks::Lists::of(facts, window.now_ms).live;
        col()
            .key("home")
            .role("main")
            .label("Home")
            .grow(1.0)
            .h_full()
            .min_w(0.0)
            .padding(space::S6)
            .gap(space::S3)
            .fill(t.bg_app)
            .child(header(t))
            .children(
                home.all_settled
                    .as_ref()
                    .map(|running| all_settled(running, window.follow_next_turn, t)),
            )
            .child(sessions_card(&home, &asks, window.home_snoozed_open, t))
    })
}

fn header(t: &Tokens) -> El {
    row()
        .key("home.header")
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(
            col()
                .grow(1.0)
                .child(text("attn").font(type_::TITLE).color(t.text_primary))
                .child(
                    text("attention hub")
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                ),
        )
        .child(
            button("home.settings", "Settings", attn_ui::icon::SETTINGS, t).on_press_tx(
                |state: &mut State, tx: &mut Tx| {
                    crate::dispatch(state, Action::UiOpenSettings, tx);
                },
            ),
        )
}

fn all_settled(running: &str, follow: bool, t: &Tokens) -> El {
    row()
        .key("home.all-settled")
        .role("group")
        .label("All settled")
        .w_full()
        .padding(space::S3)
        .gap(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(text("✓").font(type_::TITLE).color(t.success))
        .child(
            col()
                .gap(space::S1)
                .grow(1.0)
                .child(
                    text("All settled")
                        .font(type_::SECTION)
                        .color(t.text_primary),
                )
                .child(
                    text(running.to_owned())
                        .key("home.all-settled.running")
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                )
                .child(follow_check(follow, t)),
        )
}

fn follow_check(follow: bool, t: &Tokens) -> El {
    const LABEL: &str = "Take me to the next agent that needs you";
    row()
        .key("home.follow")
        .role("checkbox")
        .label(LABEL)
        .checked(follow)
        .focusable(true)
        .items_center()
        .gap(space::S2)
        .on_press(|state: &mut State| {
            state.window.follow_next_turn = !state.window.follow_next_turn
        })
        .child(
            text(if follow {
                attn_ui::icon::CHECKED
            } else {
                attn_ui::icon::UNCHECKED
            })
            .font(type_::BODY)
            .color(if follow { t.accent } else { t.text_tertiary }),
        )
        .child(text(LABEL).font(type_::LABEL).color(t.text_secondary))
}

fn sessions_card(home: &Home, asks: &[AskItem<'_>], snoozed_open: bool, t: &Tokens) -> El {
    let mut body = col()
        .key("home.sessions.body")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .gap(space::S3)
        .scroll(ScrollSpec {
            stick_bottom: false,
        });
    if !asks.is_empty() {
        body = body.child(
            col()
                .key("home.group.asks")
                .role("list")
                .label("Asks")
                .w_full()
                .gap(2.0)
                .child(group_label("Asks", Some(asks.len()), t))
                .children(asks.iter().map(|item| ask_row(item, t))),
        );
    }
    if home.empty {
        body = body.child(
            text("No active sessions")
                .key("home.empty")
                .font(type_::BODY)
                .color(t.text_muted),
        );
    } else {
        if !home.turns.is_empty() {
            body = body.child(group(
                "home.group.turns",
                "Your turn",
                Some(home.turns.len()),
                &home.turns,
                t,
            ));
        }
        if home.settled_heading {
            body = body.child(
                text("Settled")
                    .key("home.settled")
                    .role("heading")
                    .font(type_::SECTION)
                    .color(t.text_secondary),
            );
        }
        body = body.children(home.groups.iter().map(|Group { key, label, rows }| {
            group(&format!("home.group.{key}"), label, None, rows, t)
        }));
        if !home.snoozed.is_empty() {
            body = body.child(snoozed(&home.snoozed, snoozed_open, t));
        }
    }
    col()
        .key("home.sessions")
        .role("region")
        .label("Sessions")
        .w_full()
        .max_w(720.0)
        .grow(1.0)
        .min_h(0.0)
        .padding(space::S3)
        .gap(space::S3)
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            row()
                .w_full()
                .items_center()
                .child(
                    text("Sessions")
                        .role("heading")
                        .font(type_::SECTION)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(button("home.new", "New session", "+ New", t).on_press_tx(
                    |state: &mut State, tx: &mut Tx| {
                        crate::dispatch(state, Action::SessionNew, tx);
                    },
                )),
        )
        .child(body)
}

fn group(key: &str, label: &str, count: Option<usize>, rows: &[HomeAgent], t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .role("list")
        .label(label.to_owned())
        .w_full()
        .gap(2.0)
        .child(group_label(label, count, t))
        .children(rows.iter().map(|agent| session_row(agent, t)))
}

fn group_label(label: &str, count: Option<usize>, t: &Tokens) -> El {
    row()
        .gap(space::S2)
        .items_center()
        .child(
            text(label.to_owned())
                .font(type_::SECTION)
                .color(t.text_tertiary),
        )
        .children(count.map(|n| {
            text(n.to_string())
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }))
}

fn snoozed(rows: &[HomeAgent], open: bool, t: &Tokens) -> El {
    col()
        .key("home.group.snoozed")
        .role("list")
        .label("Snoozed")
        .w_full()
        .gap(2.0)
        .child(
            row()
                .key("home.group.snoozed.toggle")
                .role("button")
                .label("Snoozed")
                .expanded(open)
                .focusable(true)
                .gap(space::S1)
                .items_center()
                .radius(4.0)
                .hover(t.bg_elevated)
                .on_press(|state: &mut State| {
                    state.window.home_snoozed_open = !state.window.home_snoozed_open
                })
                .child(
                    text(if open {
                        attn_ui::icon::CARET_DOWN
                    } else {
                        attn_ui::icon::CARET_RIGHT
                    })
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
                )
                .child(group_label("Snoozed", Some(rows.len()), t)),
        )
        .children(
            open.then(|| rows.iter().map(|agent| session_row(agent, t)))
                .into_iter()
                .flatten(),
        )
}

fn ask_row(item: &AskItem<'_>, t: &Tokens) -> El {
    let id = item.ask.id.clone();
    row()
        .key(format!("home.ask.{id}"))
        .role("button")
        .label(format!("{}: {}", item.from, item.ask.subject))
        .w_full()
        .min_h(32.0)
        .px(space::S2)
        .py(space::S1)
        .gap(space::S2)
        .items_center()
        .radius(4.0)
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| crate::asks::show(state, Some(&id), tx))
        .child(text("?").font(type_::BODY).color(t.accent))
        .child(
            text(item.from.clone())
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line(),
        )
        .child(
            text(item.ask.subject.clone())
                .font(type_::BODY)
                .color(t.text_secondary)
                .grow(1.0)
                .min_w(0.0)
                .single_line()
                .truncate(),
        )
}

fn session_row(a: &HomeAgent, t: &Tokens) -> El {
    let key = format!("home.session.{}", a.agent.id);
    memo(key.clone(), (a, *t), || {
        let id = a.agent.id.clone();
        let name = a.agent.shown_name();
        let unsnooze = (!a.snoozed_until.is_empty()).then(|| {
            let id = id.clone();
            button(
                &format!("{key}.unsnooze"),
                &format!("Unsnooze {}", a.agent.name),
                "↩",
                t,
            )
            .on_press(move |state: &mut State| crate::sidebar::wake(state, &id))
        });
        let trailing = [&a.age, &a.snoozed_until]
            .into_iter()
            .find(|s| !s.is_empty())
            .cloned();
        row()
            .key(key.clone())
            .role("button")
            .label(format!("Open {}", a.agent.name))
            .a11y_value(a.agent.state.wire())
            .w_full()
            .min_h(32.0)
            .px(space::S2)
            .py(space::S1)
            .gap(space::S2)
            .items_center()
            .radius(4.0)
            .hover(t.bg_elevated)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
            .child(state_mark(&key, &a.agent, t))
            .child(
                col()
                    .grow(1.0)
                    .min_w(0.0)
                    .child(
                        text(name)
                            .font(type_::BODY)
                            .color(t.text_primary)
                            .single_line()
                            .truncate(),
                    )
                    .children(a.activity.as_ref().map(|activity| {
                        let said = match &activity.stale_since {
                            Some(at) => format!("{}, as of {at}", activity.line),
                            None => activity.line.clone(),
                        };
                        text(activity.line.clone())
                            .key(format!("{key}.activity"))
                            .label(said)
                            .font(type_::LABEL)
                            .color(if activity.stale_since.is_some() {
                                t.text_muted
                            } else {
                                t.text_tertiary
                            })
                            .single_line()
                            .truncate()
                    })),
            )
            .children(a.chief.then(|| {
                text("chief")
                    .key(format!("{key}.chief"))
                    .font(type_::MONO_CAPTION)
                    .color(t.accent)
            }))
            .children(trailing.map(|when| {
                text(when)
                    .key(format!("{key}.when"))
                    .font(type_::MONO_CAPTION)
                    .color(t.text_tertiary)
            }))
            .children(unsnooze)
    })
}

fn button(key: &str, label: &str, glyph: &str, t: &Tokens) -> El {
    row()
        .key(key.to_owned())
        .role("button")
        .label(label.to_owned())
        .focusable(true)
        .px(space::S2)
        .h(24.0)
        .items_center()
        .radius(4.0)
        .hover(t.bg_elevated)
        .child(
            text(glyph.to_owned())
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
}
