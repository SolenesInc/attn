mod model;
pub(crate) mod pull_requests;

use attn_ui::Tokens;
use attn_ui::blocks::{Block, Board, BoardCard, Card, Inline, Prose, Stack};
use attn_ui::system::{
    self, Button, Check, DotSize, GroupHead, IconSize, ListRow, Look, Reveal, Size, Type,
    icon_button, words,
};
use rune::{El, Image, Tx, col, el, memo};

use crate::asks::Item as AskItem;
use crate::sidebar::{open_agent, state_mark};
use crate::{Action, State};
use model::{Group, Home, HomeAgent};

pub(crate) use model::next_change;

/// attn's app icon, the cat on its tile, at twice the size the header draws it.
pub(crate) const LOGO: &[u8] = include_bytes!("../../../../../app/src/assets/icon.png");
const LOGO_SIDE: f32 = 64.0;

/// How narrow a Home card gets before the other card moves under it, and how short it stands
/// with nothing in it, as the Tauri app's `.dashboard-card`.
pub(crate) const CARD_LEAST: (f32, f32) = (300.0, 200.0);

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
    let home = model::home(facts, window.now_ms);
    let mut board = Board::new("home", "Home").brand(
        logo(state.pictures.logo()),
        "attn",
        "attention hub",
        settings(t),
    );
    if let Some(banner) = crate::whats_new::banner(state, t) {
        board = board.top(Block::custom("home.intro-banner.slot", banner));
    }
    if let Some(running) = &home.all_settled {
        board = board.top(Block::custom(
            "home.all-settled.slot",
            all_settled(running, window.follow_next_turn, t),
        ));
    }
    if let Some(limited) = pull_requests::rate_limit_notice(state, t) {
        board = board.top(limited);
    }
    let sessions = memo("home.sessions", reads, || {
        let asks = crate::asks::Lists::of(facts, window.now_ms).live;
        sessions_card(&home, &asks, window.home_snoozed_open, t)
    });
    board
        .card(sessions)
        .card(pull_requests::home_card(state, t))
        .build(t)
}

fn logo(logo: Option<&Image>) -> Option<El> {
    logo.map(|logo| {
        el().key("home.logo")
            .role("image")
            .label("attn")
            .size(LOGO_SIDE, LOGO_SIDE)
            .pixels(logo.pixels())
    })
}

fn settings(t: &Tokens) -> El {
    icon_button(
        "home.settings",
        "Settings",
        &attn_ui::icon::SETTINGS,
        IconSize::Large,
    )
    .on_press_tx(|state: &mut State, tx: &mut Tx| {
        crate::dispatch(state, Action::UiOpenSettings, tx);
    })
    .build(t)
    .into()
}

const FOLLOW: &str = "Take me to the next agent that needs you";

fn all_settled(running: &str, follow: bool, t: &Tokens) -> El {
    Card::new("home.all-settled")
        .push(
            Inline::top()
                .child(system::glyph(
                    attn_ui::glyph::CHECK,
                    IconSize::Regular,
                    t.success,
                ))
                .child(
                    Stack::tight()
                        .grow(1.0)
                        .min_w(0.0)
                        .child(words("All settled", Type::Name, t.text_primary))
                        .child(
                            words(running.to_owned(), Type::Small, t.text_tertiary)
                                .key("home.all-settled.running"),
                        )
                        .child(El::from(
                            Check::new("home.follow", FOLLOW).checked(follow).build(
                                t,
                                |state: &mut State, _: &mut Tx| {
                                    state.window.follow_next_turn = !state.window.follow_next_turn
                                },
                            ),
                        )),
                ),
        )
        .build(t)
        .role("group")
        .label("All settled")
}

fn sessions_card(home: &Home, asks: &[AskItem<'_>], snoozed_open: bool, t: &Tokens) -> El {
    let mut card = BoardCard::new("home.sessions", "Sessions")
        .least(CARD_LEAST.0, CARD_LEAST.1)
        .action(
            Button::new("New")
                .look(Look::Plain)
                .size(Size::Compact)
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    crate::dispatch(state, Action::SessionNew, tx);
                })
                .build(t)
                .key("home.new")
                .label("New session"),
        );
    if !asks.is_empty() {
        card = card.push(group(
            "home.group.asks",
            "Asks",
            Some(asks.len()),
            None,
            asks.iter().map(|item| ask_row(item, t)).collect(),
            t,
        ));
    }
    if home.empty {
        return card
            .push(Block::custom(
                "home.empty.slot",
                Prose::empty("home.empty", "No active sessions").el(t),
            ))
            .build(t);
    }
    if !home.turns.is_empty() {
        let unreachable = home.turns.iter().filter(|a| a.agent.offline).count();
        card = card.push(group(
            "home.group.turns",
            "Your turn",
            Some(home.turns.len() - unreachable),
            (unreachable > 0).then(|| format!("{unreachable} unreachable")),
            home.turns.iter().map(|a| session_row(a, t)).collect(),
            t,
        ));
    }
    if home.settled_heading {
        card = card.push(Block::custom(
            "home.settled.slot",
            GroupHead::new("home.settled", "Settled")
                .name_el(words("Settled", Type::Name, t.text_secondary))
                .heading()
                .build(t),
        ));
    }
    for Group { key, label, rows } in &home.groups {
        card = card.push(group(
            &format!("home.group.{key}"),
            label,
            None,
            None,
            rows.iter().map(|a| session_row(a, t)).collect(),
            t,
        ));
    }
    if !home.snoozed.is_empty() {
        card = card.push(snoozed(&home.snoozed, snoozed_open, t));
    }
    card.build(t)
}

/// What a group's head says after its name: a count, and what the count leaves out.
fn said_after(key: &str, count: Option<usize>, apart: Option<String>, t: &Tokens) -> Vec<El> {
    let said = |key: String, said: String| {
        words(said.clone(), Type::Mono, t.text_tertiary)
            .key(key)
            .label(said)
    };
    count
        .map(|n| said(format!("{key}.count"), n.to_string()))
        .into_iter()
        .chain(apart.map(|apart| said(format!("{key}.apart"), apart)))
        .collect()
}

/// A group of rows under its heading, its count and what that count leaves out.
fn group(
    key: &str,
    label: &str,
    count: Option<usize>,
    apart: Option<String>,
    rows: Vec<El>,
    t: &Tokens,
) -> Block {
    let mut head = GroupHead::new(format!("{key}.head"), label)
        .section(label.to_owned())
        .heading();
    for aside in said_after(key, count, apart, t) {
        head = head.aside(aside);
    }
    Block::custom(
        format!("{key}.slot"),
        col()
            .key(key.to_owned())
            .role("list")
            .label(label.to_owned())
            .w_full()
            .child(head.build(t))
            .children(rows),
    )
}

fn snoozed(rows: &[HomeAgent], open: bool, t: &Tokens) -> Block {
    let key = "home.group.snoozed";
    let mut head = GroupHead::new(format!("{key}.toggle"), "Snoozed")
        .section("Snoozed")
        .open(open);
    for aside in said_after(key, Some(rows.len()), None, t) {
        head = head.aside(aside);
    }
    Block::custom(
        format!("{key}.slot"),
        col()
            .key(key)
            .role("list")
            .label("Snoozed")
            .w_full()
            .child(head.build(t).on_press(|state: &mut State| {
                state.window.home_snoozed_open = !state.window.home_snoozed_open
            }))
            .children(
                open.then(|| rows.iter().map(|agent| session_row(agent, t)))
                    .into_iter()
                    .flatten(),
            ),
    )
}

/// The most of an ask's line the asker's name takes, leaving the rest to what is asked.
const ASKER_SHARE: f32 = 0.5;

fn ask_row(item: &AskItem<'_>, t: &Tokens) -> El {
    let id = item.ask.id.clone();
    let key = format!("home.ask.{id}");
    ListRow::new(key.clone(), format!("{}: {}", item.from, item.ask.subject))
        .lead(system::glyph("?", IconSize::Regular, t.accent))
        .title_el(
            Inline::joined()
                .grow(1.0)
                .child(
                    words(item.from.clone(), Type::Body, t.text_primary)
                        .key(format!("{key}.from"))
                        .single_line()
                        .truncate()
                        .shrink()
                        .max_w_frac(ASKER_SHARE),
                )
                .child(
                    words(item.ask.subject.clone(), Type::Body, t.text_secondary)
                        .key(format!("{key}.subject"))
                        .single_line()
                        .truncate()
                        .grow_from(1.0, 0.0),
                ),
        )
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| crate::asks::show(state, Some(&id), tx))
}

fn session_row(a: &HomeAgent, t: &Tokens) -> El {
    let key = format!("home.session.{}", a.agent.id);
    memo(key.clone(), (a, *t), || {
        let id = a.agent.id.clone();
        let name = crate::sidebar::flagged(&key, &a.agent, t).shrink().child(
            words(a.agent.name.clone(), Type::Body, t.text_primary)
                .single_line()
                .truncate()
                .shrink(),
        );
        let mut row = ListRow::new(key.clone(), format!("Open {}", a.agent.name))
            .lead(state_mark(&key, &a.agent, DotSize::Small, t))
            .title_el(name)
            .tint(a.agent.offline.then_some(t.error));
        if let Some(activity) = &a.activity {
            let said = match &activity.stale_since {
                Some(at) => format!("{}, as of {at}", activity.line),
                None => activity.line.clone(),
            };
            let ink = if activity.stale_since.is_some() {
                t.text_muted
            } else {
                t.text_tertiary
            };
            row = row.subtitle_el(
                rune::row().min_w(0.0).child(
                    words(activity.line.clone(), Type::Small, ink)
                        .key(format!("{key}.activity"))
                        .label(said)
                        .single_line()
                        .truncate()
                        .shrink(),
                ),
            );
        }
        if a.chief {
            row = row.aside(words("chief", Type::Mono, t.accent).key(format!("{key}.chief")));
        }
        if let Some(when) = [&a.age, &a.snoozed_until]
            .into_iter()
            .find(|s| !s.is_empty())
        {
            row = row
                .aside(words(when.clone(), Type::Mono, t.text_dimmed).key(format!("{key}.when")));
        }
        if !a.snoozed_until.is_empty() && !a.agent.offline {
            let id = id.clone();
            row = row.controls(
                [icon_button(
                    format!("{key}.unsnooze"),
                    format!("Unsnooze {}", a.agent.name),
                    "↩",
                    IconSize::Regular,
                )
                .size(Size::Small)
                .on_press(move |state: &mut State| crate::sidebar::wake(state, &id))
                .build(t)
                .into()],
                Reveal::Always,
            );
        }
        row.build(t)
            .a11y_value(a.agent.state.wire())
            .on_press_tx(move |state: &mut State, tx: &mut Tx| open_agent(state, &id, tx))
    })
}
