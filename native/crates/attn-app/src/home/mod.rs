mod model;

use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens};
use rune::tok::space;
use rune::{El, Image, ScrollSpec, Tx, col, el, memo, row, text};
use rune_kit::{Button, ButtonVariant};

use crate::asks::Item as AskItem;
use crate::sidebar::{open_agent, state_mark};
use crate::{Action, State};
use model::{Group, Home, HomeAgent};

pub(crate) use model::next_change;

/// attn's app icon, the cat on its tile, at twice the size the header draws it.
pub(crate) const LOGO: &[u8] = include_bytes!("../../../../../app/src/assets/icon.png");
const LOGO_SIDE: f32 = 64.0;

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let window = &state.window;
    let reads = (
        std::sync::Arc::as_ptr(&facts.sessions) as usize,
        std::sync::Arc::as_ptr(&facts.desktops) as usize,
        std::sync::Arc::as_ptr(&facts.profiles) as usize,
        std::sync::Arc::as_ptr(&facts.settings) as usize,
        std::sync::Arc::as_ptr(&facts.asks) as usize,
        (
            std::sync::Arc::as_ptr(&facts.garden) as usize,
            std::sync::Arc::as_ptr(&facts.outposts) as usize,
        ),
        window.now_ms,
        window.follow_next_turn,
        window.home_snoozed_open,
        window.whats_new.banner_dismissed(),
        state.pictures.logo().map(Image::id),
        *t,
    );
    let top = memo("home.top", reads, || {
        let home = model::home(facts, window.now_ms);
        col()
            .w_full()
            .child(header(state.pictures.logo(), t).mb(24.0))
            .children(crate::whats_new::banner(state, t).map(|banner| banner.mb(20.0)))
            .children(
                home.all_settled
                    .as_ref()
                    .map(|running| all_settled(running, window.follow_next_turn, t).mb(20.0)),
            )
    });
    let sessions = memo("home.sessions", reads, || {
        let home = model::home(facts, window.now_ms);
        let asks = crate::asks::Lists::of(facts, window.now_ms).live;
        sessions_card(&home, &asks, window.home_snoozed_open, t)
    });
    col()
        .key("home")
        .role("main")
        .label("Home")
        .grow(1.0)
        .h_full()
        .min_w(0.0)
        .py(24.0)
        .px(28.0)
        .fill(t.bg_app)
        .child(top)
        .child(
            col()
                .key("home.cards.scroll")
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .scroll(ScrollSpec {
                    stick_bottom: false,
                })
                .child(
                    row()
                        .key("home.cards")
                        .w_full()
                        .gap(16.0)
                        .flex_wrap()
                        .child(sessions)
                        .child(crate::attention::home_card(state, t)),
                ),
        )
}

fn header(logo: Option<&Image>, t: &Tokens) -> El {
    row()
        .key("home.header")
        .w_full()
        .items_center()
        .gap(10.0)
        .children(logo.map(|logo| {
            el().key("home.logo")
                .role("image")
                .label("attn")
                .size(LOGO_SIDE, LOGO_SIDE)
                .pixels(logo.pixels())
        }))
        .child(
            col()
                .grow(1.0)
                .child(
                    text("attn")
                        .font(rune::TextStyle::new(24.0, 700.0).mono())
                        .color(t.brand),
                )
                .child(type_::sans_caps("attention hub").color(t.text_dimmed)),
        )
        .child(
            GlyphButton::new(&attn_ui::icon::SETTINGS, "Settings")
                .icon_size(16.0)
                .color(t.text_dimmed)
                .side(32.0)
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    crate::dispatch(state, Action::UiOpenSettings, tx);
                })
                .build(attn_ui::kit(t))
                .key("home.settings")
                .self_start()
                .radius(6.0)
                .hover(t.bg_elevated),
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
        .preserve_focus_on_press(true)
        .items_center()
        .gap(space::S2)
        .on_press(|state: &mut State| {
            state.window.follow_next_turn = !state.window.follow_next_turn
        })
        .child(
            text(if follow {
                attn_ui::glyph::CHECKED
            } else {
                attn_ui::glyph::UNCHECKED
            })
            .font(type_::BODY)
            .color(if follow { t.accent } else { t.text_tertiary }),
        )
        .child(text(LABEL).font(type_::LABEL).color(t.text_secondary))
}

fn sessions_card(home: &Home, asks: &[AskItem<'_>], snoozed_open: bool, t: &Tokens) -> El {
    let mut body = col().key("home.sessions.body").w_full().gap(16.0);
    if !asks.is_empty() {
        body = body.child(
            col()
                .key("home.group.asks")
                .role("list")
                .label("Asks")
                .w_full()
                .gap(0.0)
                .child(
                    group_label("home.group.asks", "Asks", Some(asks.len()), None, t).mb(space::S2),
                )
                .children(asks.iter().map(|item| ask_row(item, t))),
        );
    }
    if home.empty {
        body = body.child(card_empty("home.empty", "No active sessions", t));
    } else {
        if !home.turns.is_empty() {
            let unreachable = home.turns.iter().filter(|a| a.agent.offline).count();
            body = body.child(group(
                "home.group.turns",
                "Your turn",
                Some(home.turns.len() - unreachable),
                (unreachable > 0).then(|| format!("{unreachable} unreachable")),
                &home.turns,
                t,
            ));
        }
        if home.settled_heading {
            body = body.child(
                type_::upper(
                    "Settled",
                    rune::TextStyle {
                        weight: 600.0,
                        ..type_::SANS_CAPS
                    },
                )
                .key("home.settled")
                .role("heading")
                .color(t.text_muted)
                .w_full()
                .mt(4.0)
                .px(space::S2)
                .pb(6.0)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border),
            );
        }
        body = body.children(home.groups.iter().map(|Group { key, label, rows }| {
            group(&format!("home.group.{key}"), label, None, None, rows, t)
        }));
        if !home.snoozed.is_empty() {
            body = body.child(snoozed(&home.snoozed, snoozed_open, t));
        }
    }
    card(
        "home.sessions",
        "Sessions",
        vec![card_action(
            "home.new",
            "New session",
            "+ New",
            t,
            |state, tx| {
                crate::dispatch(state, Action::SessionNew, tx);
            },
        )],
        body,
        t,
    )
}

/// A Home card, as the Tauri app's `.dashboard-card`: a header band holding the heading and
/// `actions`, ruled off from `body`. The cards share a row of the grid, so each is as tall as
/// the taller one's content, and never under 200 points.
pub(crate) fn card(key: &str, heading: &str, actions: Vec<El>, body: El, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .role("region")
        .label(heading.to_owned())
        .grow(1.0)
        .min_w(300.0)
        .min_h(200.0)
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .child(
            row()
                .key(format!("{key}.header"))
                .w_full()
                .items_center()
                .gap(space::S2)
                .padding(16.0)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border)
                .child(
                    type_::caps(heading)
                        .role("heading")
                        .color(t.text_dimmed)
                        .grow(1.0),
                )
                .children(actions),
        )
        .child(body.padding(12.0))
}

/// What a card says when it has nothing to list, as the Tauri app's `.card-empty`.
pub(crate) fn card_empty(key: &str, said: &str, t: &Tokens) -> El {
    text(said.to_owned())
        .key(key.to_owned())
        .font(type_::BODY)
        .color(t.text_dimmed)
        .align(rune::Align::Center)
        .wrap()
        .w_full()
        .padding(20.0)
}

/// A card's count, as the Tauri app's `.card-count`: a brand pill.
pub(crate) fn card_count(key: &str, count: usize, label: String, t: &Tokens) -> El {
    text(count.to_string())
        .key(key.to_owned())
        .role("status")
        .label(label)
        .font(rune::TextStyle {
            weight: 700.0,
            ..type_::CAPTION
        })
        .color(t.on_brand)
        .py(2.0)
        .px(8.0)
        .radius(10.0)
        .fill(t.brand)
}

/// A bordered button in a card's header, as the Tauri app's `.card-action`.
fn card_action(
    key: &str,
    label: &str,
    shown: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let (shown, ink) = (shown.to_owned(), t.text_tertiary);
    Button::new(label)
        .variant(ButtonVariant::Ghost)
        .label_with(move |_, _| text(shown).font(type_::CAPTION).color(ink))
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key.to_owned())
        .h(22.0)
        .px(10.0)
        .radius(4.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .hover(t.border)
}

/// A group of agents under its label, its count and what that count leaves out.
fn group(
    key: &str,
    label: &str,
    count: Option<usize>,
    apart: Option<String>,
    rows: &[HomeAgent],
    t: &Tokens,
) -> El {
    col()
        .key(key.to_owned())
        .role("list")
        .label(label.to_owned())
        .w_full()
        .gap(0.0)
        .child(group_label(key, label, count, apart, t).mb(space::S2))
        .children(rows.iter().map(|agent| session_row(agent, t)))
}

fn group_label(
    key: &str,
    label: &str,
    count: Option<usize>,
    apart: Option<String>,
    t: &Tokens,
) -> El {
    let caption = |key: String, said: String| {
        text(said.clone())
            .key(key)
            .label(said)
            .font(type_::MONO_CAPTION)
            .color(t.text_tertiary)
    };
    row()
        .gap(space::S2)
        .px(space::S2)
        .items_center()
        .child(type_::sans_caps(label).color(t.text_muted))
        .children(count.map(|n| caption(format!("{key}.count"), n.to_string())))
        .children(apart.map(|said| caption(format!("{key}.apart"), said)))
}

fn snoozed(rows: &[HomeAgent], open: bool, t: &Tokens) -> El {
    let (tokens, count) = (*t, rows.len());
    col()
        .key("home.group.snoozed")
        .role("list")
        .label("Snoozed")
        .w_full()
        .gap(0.0)
        .child(
            Button::new("Snoozed")
                .variant(ButtonVariant::Ghost)
                .on_press(|state: &mut State| {
                    state.window.home_snoozed_open = !state.window.home_snoozed_open
                })
                .label_with(move |_, _| {
                    row()
                        .gap(space::S1)
                        .items_center()
                        .child(
                            text(if open {
                                attn_ui::glyph::CARET_DOWN
                            } else {
                                attn_ui::glyph::CARET_RIGHT
                            })
                            .font(type_::LABEL)
                            .color(tokens.text_tertiary),
                        )
                        .child(group_label(
                            "home.group.snoozed",
                            "Snoozed",
                            Some(count),
                            None,
                            &tokens,
                        ))
                })
                .build(attn_ui::kit(t))
                .expanded(open)
                .key("home.group.snoozed.toggle")
                .h(20.0)
                .px(0.0)
                .self_start()
                .radius(4.0)
                .hover(t.bg_elevated),
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
        .focusable(true)
        .label(format!("{}: {}", item.from, item.ask.subject))
        .w_full()
        .min_h(ROW_HEIGHT)
        .px(12.0)
        .gap(10.0)
        .items_center()
        .radius(6.0)
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| crate::asks::show(state, Some(&id), tx))
        .child(
            text("?")
                .font(rune::TextStyle {
                    weight: 700.0,
                    ..type_::BODY
                })
                .color(t.accent)
                .w(8.0),
        )
        .child(
            text(item.from.clone())
                .key(format!("home.ask.{}.from", item.ask.id))
                .font(type_::BODY)
                .color(t.text_primary)
                .grow(1.0)
                .min_w(0.0)
                .single_line()
                .truncate(),
        )
        .child(
            text(item.ask.subject.clone())
                .key(format!("home.ask.{}.subject", item.ask.id))
                .font(type_::BODY)
                .color(t.text_secondary)
                .grow(1.0)
                .min_w(0.0)
                .single_line()
                .truncate(),
        )
}

/// A row of the sessions card, as the Tauri app's `.session-row`: 10 points of padding over
/// and under a line of text.
const ROW_HEIGHT: f32 = 36.0;

/// An agent's state dot on the card, as the Tauri app's `.state-dot`: 6 points across, or the
/// sidebar's struck-through ring while its machine is unreachable.
fn dot(key: &str, a: &HomeAgent, t: &Tokens) -> El {
    let mark = state_mark(key, &a.agent, crate::sidebar::MARK, t);
    if a.agent.offline {
        mark
    } else {
        mark.size(6.0, 6.0).radius(3.0)
    }
}

fn session_row(a: &HomeAgent, t: &Tokens) -> El {
    let key = format!("home.session.{}", a.agent.id);
    memo(key.clone(), (a, *t), || {
        let id = a.agent.id.clone();
        let name = a.agent.shown_name();
        let unsnooze = (!a.snoozed_until.is_empty() && !a.agent.offline).then(|| {
            let id = id.clone();
            button(
                &format!("{key}.unsnooze"),
                &format!("Unsnooze {}", a.agent.name),
                "↩",
                t,
                move |state, _| crate::sidebar::wake(state, &id),
            )
        });
        let trailing = [&a.age, &a.snoozed_until]
            .into_iter()
            .find(|s| !s.is_empty())
            .cloned();
        row()
            .key(key.clone())
            .role("button")
            .focusable(true)
            .label(format!("Open {}", a.agent.name))
            .a11y_value(a.agent.state.wire())
            .w_full()
            .min_h(ROW_HEIGHT)
            .px(12.0)
            .gap(10.0)
            .items_center()
            .radius(6.0)
            .fill(if a.agent.offline {
                t.error.alpha(0.07)
            } else {
                rune::Color::TRANSPARENT
            })
            .hover(t.bg_elevated)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
            .child(dot(&key, a, t))
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
                    .color(t.text_dimmed)
            }))
            .children(unsnooze)
    })
}

fn button(
    key: &str,
    label: &str,
    glyph: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    GlyphButton::new(glyph, label)
        .font(type_::LABEL)
        .side(24.0)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key.to_owned())
        .px(space::S2)
        .radius(4.0)
        .hover(t.bg_elevated)
}
