mod model;
pub(crate) mod pull_requests;

use std::hash::{Hash, Hasher};

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
pub(crate) const LOGO: &[u8] = include_bytes!("../../assets/icon.png");
const LOGO_SIDE: f32 = 64.0;

/// How narrow a Home card gets before the other card moves under it, and how short it stands
/// with nothing in it.
pub(crate) const CARD_LEAST: (f32, f32) = (300.0, 200.0);

/// One of attn's own views, which Home draws and a tile can show on any desktop: a markdown
/// tile whose params are `{"view": name}`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum BuiltInView {
    Attn,
    Sessions,
    PullRequests,
}

impl BuiltInView {
    pub const ALL: [BuiltInView; 3] = [
        BuiltInView::Attn,
        BuiltInView::Sessions,
        BuiltInView::PullRequests,
    ];

    pub fn name(self) -> &'static str {
        match self {
            BuiltInView::Attn => "attn",
            BuiltInView::Sessions => "sessions",
            BuiltInView::PullRequests => "pull-requests",
        }
    }

    pub fn title(self) -> &'static str {
        match self {
            BuiltInView::Attn => "attn",
            BuiltInView::Sessions => "Sessions",
            BuiltInView::PullRequests => "Pull requests",
        }
    }

    pub fn params(self) -> String {
        serde_json::json!({ "view": self.name() }).to_string()
    }

    /// The view a markdown tile's params name, when they name one.
    pub fn named_by(params: &str) -> Option<BuiltInView> {
        let named: serde_json::Value = serde_json::from_str(params).ok()?;
        let name = named.get("view")?.as_str()?;
        BuiltInView::ALL
            .into_iter()
            .find(|view| view.name() == name)
    }

    /// The view a tile of `kind` with `params` shows, when it shows one.
    pub(crate) fn shown_by(kind: &str, params: Option<&str>) -> Option<BuiltInView> {
        (kind == "markdown")
            .then_some(params)
            .flatten()
            .and_then(BuiltInView::named_by)
    }

    /// The view a desktop's leaf shows, when it shows one.
    pub(crate) fn shown_in(leaf: &attn_client::layout::Leaf) -> Option<BuiltInView> {
        let (kind, params) = leaf.tile()?;
        BuiltInView::shown_by(kind, Some(params))
    }

    /// The id of the tile that shows this view on a desktop.
    pub(crate) fn tile_id(self) -> String {
        format!("tile-view-{}", self.name())
    }

    fn onto(self, board: Board, state: &State, t: &Tokens) -> Board {
        match self {
            BuiltInView::Attn => {
                let board = board.brand(
                    logo(state.pictures.logo()),
                    "attn",
                    "attention hub",
                    settings(t),
                );
                match crate::whats_new::banner(state, t) {
                    Some(banner) => board.top(banner),
                    None => board,
                }
            }
            BuiltInView::Sessions => sessions(board, state, t),
            BuiltInView::PullRequests => {
                let board = match pull_requests::rate_limit_notice(state, t) {
                    Some(limited) => board.top(limited),
                    None => board,
                };
                board.card(pull_requests::home_card(state, t))
            }
        }
    }

    /// What drawing this view reads, as one number that changes when the drawing may.
    fn reads(self, state: &State) -> u64 {
        let mut hasher = std::hash::DefaultHasher::new();
        match self {
            BuiltInView::Attn => (
                state.window.whats_new.banner_dismissed(),
                state.pictures.logo().map(Image::id),
            )
                .hash(&mut hasher),
            BuiltInView::Sessions => {
                let home = model::home(&state.facts, state.window.now_ms);
                sessions_reads(state, &home).hash(&mut hasher)
            }
            BuiltInView::PullRequests => (
                pull_requests::reads(state),
                pull_requests::rate_limit_left_ms(&state.facts, state.window.now_ms)
                    .map(|left| left / 1000),
            )
                .hash(&mut hasher),
        }
        hasher.finish()
    }
}

pub(crate) fn view(state: &State, t: &Tokens) -> El {
    BuiltInView::ALL
        .into_iter()
        .fold(Board::new("home", "Home"), |board, view| {
            view.onto(board, state, t)
        })
        .build(t)
}

/// `view` drawn in a tile's box, as Home draws it.
pub(crate) fn tile(view: BuiltInView, state: &State, t: &Tokens) -> El {
    view.onto(
        Board::new(format!("view.{}", view.name()), view.title()),
        state,
        t,
    )
    .build(t)
    .role("region")
}

/// What drawing the views among `shown` reads; none when no view is among them.
pub(crate) fn tiles_read(
    state: &State,
    shown: impl IntoIterator<Item = BuiltInView>,
) -> Option<Vec<(BuiltInView, u64)>> {
    let mut views: Vec<BuiltInView> = shown.into_iter().collect();
    views.sort();
    views.dedup();
    (!views.is_empty()).then(|| {
        views
            .into_iter()
            .map(|view| (view, view.reads(state)))
            .collect()
    })
}

/// Shows `view` on the current desktop: the tile that already shows it there, or a new one
/// docked beside the active tile.
pub(crate) fn show_tile(state: &mut State, view: BuiltInView, tx: &mut Tx) {
    let Some(desktop) = state.facts.current_desktop() else {
        return;
    };
    let shown = shown_on(desktop).find(|(_, shown)| *shown == view);
    let desktop = desktop.id.clone();
    if let Some((tile, _)) = shown {
        state.window.show_desktop(tx);
        crate::desktop::show_leaf(state, &desktop, &tile, tx);
        return;
    }
    let tile_id = view.tile_id();
    let docked = (desktop.clone(), tile_id.clone());
    state.act(
        move |request_id| attn_protocol::DesktopDockTileMessage {
            request_id,
            desktop_id: desktop,
            tile_id,
            tile_kind: "markdown".into(),
            tile_params: Some(view.params()),
            edge: "right".into(),
            ..attn_protocol::DesktopDockTileMessage::new()
        },
        crate::requests::Refused::Prefixed("Could not show the view"),
        move |state, _, tx| {
            let (desktop, tile) = docked;
            state.window.show_desktop(tx);
            crate::desktop::show_leaf(state, &desktop, &tile, tx);
        },
        tx,
    );
}

/// Whether `desktop` shows the Sessions view, whose "All settled" offers to follow the next
/// turn from there as from Home.
pub(crate) fn shows_sessions(desktop: Option<&attn_client::Desktop>) -> bool {
    desktop.is_some_and(|desktop| shown_on(desktop).any(|(_, view)| view == BuiltInView::Sessions))
}

/// Each tile on `desktop` that shows a view, with the view.
pub(crate) fn shown_on(
    desktop: &attn_client::Desktop,
) -> impl Iterator<Item = (String, BuiltInView)> + '_ {
    desktop
        .leaves()
        .into_iter()
        .filter_map(|leaf| Some((leaf.id.clone(), BuiltInView::shown_in(leaf)?)))
}

fn sessions_reads<'a>(state: &'a State, home: &'a model::Home) -> impl Hash + 'a {
    let facts = &state.facts;
    let window = &state.window;
    (
        std::sync::Arc::as_ptr(&facts.sessions) as usize,
        std::sync::Arc::as_ptr(&facts.desktops) as usize,
        std::sync::Arc::as_ptr(&facts.profiles) as usize,
        std::sync::Arc::as_ptr(&facts.settings) as usize,
        std::sync::Arc::as_ptr(&facts.asks) as usize,
        (
            std::sync::Arc::as_ptr(&facts.garden) as usize,
            std::sync::Arc::as_ptr(&facts.outposts) as usize,
            std::sync::Arc::as_ptr(&facts.harnesses) as usize,
        ),
        home,
        window.follow_next_turn,
        window.home_snoozed_open,
    )
}

/// Sessions' notice that nothing is owed, above its card, and the card.
fn sessions(board: Board, state: &State, t: &Tokens) -> Board {
    let (facts, window) = (&state.facts, &state.window);
    let home = model::home(facts, window.now_ms);
    let board = match &home.all_settled {
        Some(running) => board.top(Block::custom(
            "home.all-settled.slot",
            all_settled(running, window.follow_next_turn, t),
        )),
        None => board,
    };
    let reads = (sessions_reads(state, &home), *t);
    board.card(memo("home.sessions", reads, || {
        let asks = crate::asks::Lists::of(facts, window.now_ms).live;
        sessions_card(&home, &asks, window.home_snoozed_open, t)
    }))
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
    let subject = crate::asks::inline::Line::plain(&item.ask.subject);
    ListRow::new(key.clone(), format!("{}: {subject}", item.from))
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
                    words(subject, Type::Body, t.text_secondary)
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
