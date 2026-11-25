use attn_client::Desktop;
use attn_protocol::session_id::short;
use attn_protocol::{DesktopPane, Session};
use attn_term::mouse;
use attn_ui::Tokens;
use attn_ui::blocks::{HeaderPiece, Settling, SettlingChip, Shelf, Tabs, TileHeader};
use attn_ui::colors::TerminalColors;
use attn_ui::system::{Button as SystemButton, IconSize, Look, Size, icon_button};
use rune::{Drag, El, Mods, PointerButton, Tx, col, el, memo};

use super::Area;
use super::fit::{Level, Piece, Row, Widths, fit};
use super::header_pop::What;
use super::layout::{Direction, Leaf, LeafKind, Node};
use crate::State;
use crate::shell::Holder;
use crate::sidebar::queue;
use crate::terminal::{Release, Terminals, accelerator};

/// The tile area's element.
pub(super) const TILES: &str = "tiles";
/// The strip over the line between two tiles that drags it.
pub(super) const DIVIDER_PX: f32 = 9.0;
/// The rows a terminal keeps clear under the notices over it: three whole rows, main's at the
/// smallest window with the largest text, and the one the band's foot may cut.
const ROWS_UNDER_NOTICES: f32 = 4.0;

/// What an agent's header says about its turn settling on its own.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Settle {
    /// Settles when its countdown runs out, the deadline named.
    Counting(String),
    /// The countdown stands still until the host comes back.
    Held,
    /// The person kept the next turn: it will not settle on its own.
    Kept,
    /// Its machine is unreachable: the countdown stands still and cannot be kept until the
    /// machine is back.
    Frozen,
}

impl Settle {
    fn of(
        session: &attn_protocol::Session,
        outposts: &[attn_protocol::OutpostInfo],
    ) -> Option<Settle> {
        let counts = session
            .auto_settle_fires_at
            .as_deref()
            .is_some_and(|at| !at.is_empty())
            || session.auto_settle_held == Some(true);
        if crate::outpost::offline(session, outposts) {
            return counts.then_some(Settle::Frozen);
        }
        match &session.auto_settle_fires_at {
            Some(at) if !at.is_empty() => Some(Settle::Counting(at.clone())),
            _ if session.auto_settle_held == Some(true) => Some(Settle::Held),
            _ if session.auto_settle_dismiss_armed == Some(true) => Some(Settle::Kept),
            _ => None,
        }
    }
}

/// The tile area: the current desktop's screen of its row, each tile with its header.
pub(crate) fn area(state: &State, t: &Tokens, scale: f32) -> El {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let desktop = facts.current_desktop();
    let holder = state.window.keyboard.clone();
    let composing = state.window.keyboard_terminal(facts).and_then(|id| {
        let terminals = &state.terminals;
        let font = terminals
            .cell_font(&id)
            .map(|(size, h)| [size, h].map(f32::to_bits));
        Some((terminals.preedit(&id)?.to_owned(), font))
    });
    let row_state = desktop.map(|d| desk.row(&d.id));
    let area_bits = desk
        .area()
        .map(|a| [a.w, a.h, a.px_per_col].map(f32::to_bits));
    let dragging = desk
        .drag
        .as_ref()
        .and_then(|d| d.dragging())
        .map(str::to_owned);
    let keep = header_keys(state).keep;
    let sublines = desktop.map_or_else(Vec::new, |d| sublines(d, facts, desk, keep.as_deref()));
    let focus_style = super::FocusStyle::chosen();
    let reads = (
        (
            version(&facts.desktops),
            version(&facts.profiles),
            version(&facts.settings),
            version(&facts.harnesses),
        ),
        holder.clone(),
        desk.shown_row.clone(),
        composing,
        state.terminals.pointing_link().map(str::to_owned),
        row_state,
        area_bits,
        dragging.clone(),
        (
            ratio_bits(&desk.ratios),
            desk.peeks.unsent(),
            sublines.clone(),
        ),
        (*t, scale.to_bits(), focus_style),
    );
    let settles = desktop.map_or_else(Vec::new, |d| settles(d, facts));
    let keys = header_keys(state);
    let names: Vec<crate::sidebar::AgentRow> = desktop
        .map(|d| {
            d.panes
                .iter()
                .filter_map(|p| facts.sessions.iter().find(|s| s.id == p.session_id))
                .map(|s| crate::sidebar::AgentRow::of(s, &facts.harnesses, &facts.outposts, false))
                .collect()
        })
        .unwrap_or_default();
    let reads = (
        reads,
        settles.clone(),
        keys.clone(),
        names,
        state.terminals.find_bars(),
        state.terminals.filter_panels(),
        state.terminals.annotation_layers(),
        (
            stale_builds(&facts.sessions, desk),
            desktop.map(|d| {
                super::shared_conversation::notices(&facts.sessions, &facts.desktops, &d.id)
            }),
            desktop.map(|d| crate::review::setup::reads(facts, &d.id, state.window.now_ms)),
        ),
        desk.held_split().map(str::to_owned),
        (
            super::arrival::keys(&desk.rings),
            desk.refusals.keys(),
            reaches(state, desktop),
        ),
        facts.tile_contents.revision,
        (
            outpost_names(&facts.outposts),
            state.pictures.revision,
            (
                std::sync::Arc::as_ptr(&facts.garden) as usize,
                state.window.garden.revision,
                desktop
                    .is_some_and(holds_a_seed)
                    .then_some(state.window.now_ms / 60_000),
                (desktop.is_some_and(holds_a_seed) && crate::garden::v2::enabled(state)).then(
                    || {
                        (
                            version(&facts.sessions),
                            version(&facts.asks),
                            crate::setting::on(&facts.settings, queue::QUEUE_MODE),
                            crate::setting::on(&facts.settings, queue::QUEUE_CREW),
                        )
                    },
                ),
                crate::garden::model::has_chief(&facts.sessions, &facts.profiles.selected),
            ),
            (
                state.drafts.revision,
                state.readings.revision,
                desk.header_pop.mark_anchor(),
                state.window.hints.shown().cloned(),
            ),
            (state.notebook.revision, state.browser.revision()),
        ),
    );
    let launcher = desktop
        .filter(|d| super::holds_nothing(d))
        .and_then(|_| crate::picker::inline(state, t));
    let content = launcher.unwrap_or_else(|| {
        memo("tiles.area", reads, || {
            col().w_full().h_full().min_w(0.0).min_h(0.0).child(screen(
                desktop,
                desk,
                facts,
                (&settles, &keys, &sublines),
                holder.as_ref(),
                (
                    &state.terminals,
                    &state.pictures,
                    &state.notebook,
                    &seed_reads(state),
                    (&state.drafts, &state.readings),
                    &state.browser,
                    state.window.hints.shown(),
                ),
                (t, scale, state.window.now_ms, focus_style),
            ))
        })
    });
    let tabs = desktop.and_then(|d| review_tabs(d, desk.row(&d.id).view.clone(), t));
    let tiles = col()
        .key(TILES)
        .on_drag(|state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            super::drag::gesture(state, d, tx, super::drag::Owner::Tiles)
        })
        .role("main")
        .label("Tiles")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
            let area = Some(Area::at(
                rect,
                state.terminals.metrics().cell_points(scale).0,
            ));
            if state.window.desk.reported_area == area {
                tx.live_only();
                return;
            }
            state.window.desk.reported_area = area;
            super::settle(state, tx);
        })
        .child(content);
    moves_with_the_row(col().key("tiles.column"))
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .children(tabs)
        .child(attn_ui::blocks::tile_area(tiles))
}

/// A tab for each document on `desktop` while one of them is shown alone and it holds more
/// than one: a press shows that document alone.
fn review_tabs(desktop: &Desktop, view: super::layout::View, t: &Tokens) -> Option<El> {
    let super::layout::View::Focused(shown) = view else {
        return None;
    };
    let documents: Vec<(String, String)> = desktop
        .leaves()
        .into_iter()
        .filter_map(|leaf| match leaf.tile()? {
            (kind @ ("markdown" | "seed"), params) => Some((
                leaf.id.clone(),
                crate::sidebar::model::document_tab(kind, params),
            )),
            _ => None,
        })
        .collect();
    if documents.len() < 2 || !documents.iter().any(|(id, _)| *id == shown) {
        return None;
    }
    let tabs = documents.into_iter().fold(
        Tabs::new("review.tabs", "Open review documents"),
        |tabs, (id, name)| {
            let (desktop, leaf) = (desktop.id.clone(), id.clone());
            tabs.tab(
                format!("review.tab.{id}"),
                name,
                id == shown,
                t,
                move |state: &mut State, tx: &mut Tx| {
                    super::actions::focus_leaf(state, &desktop, &leaf, tx)
                },
            )
        },
    );
    Some(tabs.build(t))
}

/// What an agent header's second line shows, each piece at the level its tile's width leaves
/// it, and the chips its first line fits beside the title.
#[derive(Clone, Debug, PartialEq, Hash)]
struct Subline {
    seed: Option<super::seed_chip::Chip>,
    role: Option<super::chain::Trigger>,
    host: Option<String>,
    usage: Option<super::usage::Badge>,
    usage_open: bool,
    provenance: Option<super::provenance::Line>,
    levels: Vec<(Piece, Level)>,
    ask: Option<crate::asks::Chip>,
    headline: Vec<(Piece, Level)>,
}

impl Subline {
    fn level(&self, piece: Piece) -> Level {
        self.levels
            .iter()
            .find(|(p, _)| *p == piece)
            .map_or(Level::Full, |(_, level)| *level)
    }

    fn shows(&self, piece: Piece) -> Option<Level> {
        Some(self.level(piece)).filter(|level| *level != Level::Hidden)
    }

    fn headline_level(&self, piece: Piece) -> Level {
        self.headline
            .iter()
            .find(|(p, _)| *p == piece)
            .map_or(Level::Full, |(_, level)| *level)
    }
}

/// The state dot at the headline's start.
const HEADLINE_DOT: f32 = attn_ui::system::DotSize::Small.side();

/// How much a document or browser tile's header shows, by the tile's width: under 720 pt its
/// overall-note button goes and its Focus pill becomes an icon, and under 480 pt its notes
/// count loses its word.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Room {
    Narrow,
    Medium,
    Wide,
}

impl Room {
    /// The room tile `leaf`'s header has at its last layout; medium before the tile is laid out.
    pub(crate) fn of(leaf: &str) -> Room {
        rune::memo::layout_with(&format!("tile.{leaf}"), |[_, _, w, _]| {
            if w <= 480.0 {
                Room::Narrow
            } else if w <= 720.0 {
                Room::Medium
            } else {
                Room::Wide
            }
        })
        .unwrap_or(Room::Medium)
    }
}

/// Each agent pane's subline, fitted to its tile's width. A piece whose popover is open keeps
/// its place, so the popover keeps its anchor.
fn sublines(
    desktop: &Desktop,
    facts: &attn_client::Facts,
    desk: &super::Desk,
    keep_key: Option<&str>,
) -> Vec<(String, Subline)> {
    let settles = settles(desktop, facts);
    let seeds = facts.garden.of(&facts.profiles.selected);
    desktop
        .panes
        .iter()
        .filter_map(|p| {
            let s = facts.sessions.iter().find(|s| s.id == p.session_id)?;
            let mut sub = Subline {
                seed: super::seed_chip::of(
                    s,
                    seeds,
                    &crate::sidebar::display_name(s, &facts.harnesses),
                ),
                role: super::chain::trigger(s, facts),
                host: super::host::name(s, &facts.outposts),
                usage: super::usage::badge(s),
                usage_open: desk.header_pop.open.as_ref().is_some_and(|pop| {
                    pop.on(
                        &super::header_pop::Host::Tile(p.pane_id.clone()),
                        super::header_pop::What::Usage,
                    )
                }),
                provenance: super::provenance::line(s),
                levels: Vec::new(),
                ask: crate::asks::chip_of(facts, s),
                headline: Vec::new(),
            };
            let tile_w = desk
                .rects
                .get(&format!("tile.{}", p.pane_id))
                .map(|[_, _, w, _]| *w);
            let settle = settles
                .iter()
                .find(|(l, _)| *l == p.pane_id)
                .map(|(_, s)| s);
            if let Some(tile_w) = tile_w {
                let pieces: Vec<(Piece, Widths)> = [
                    settle.map(|settle| (Piece::Settling, settling_widths(settle, keep_key))),
                    sub.ask
                        .as_ref()
                        .map(|chip| (Piece::Ask, crate::asks::chip_widths(chip))),
                ]
                .into_iter()
                .flatten()
                .collect();
                sub.headline = fit(&Row {
                    available: TileHeader::headline_room(tile_w),
                    gap: TileHeader::headline_gap(),
                    fixed: &[HEADLINE_DOT, super::fit::TITLE_MINIMUM],
                    pieces: &pieces,
                    rank: super::fit::HEADLINE_RANK,
                    keep: &[],
                });
            }
            let available = tile_w.map(TileHeader::subline_room);
            if let Some(available) = available {
                let pieces: Vec<(Piece, Widths)> = [
                    sub.seed
                        .as_ref()
                        .map(|c| (Piece::Seed, super::seed_chip::widths(c))),
                    sub.role
                        .as_ref()
                        .map(|r| (Piece::Role, super::chain::widths(r))),
                    sub.host
                        .as_deref()
                        .map(|h| (Piece::Host, super::host::widths(h))),
                    sub.usage
                        .as_ref()
                        .map(|u| (Piece::Usage, super::usage::width(u))),
                    sub.provenance
                        .as_ref()
                        .map(|l| (Piece::Provenance, super::provenance::widths(l))),
                    Some((Piece::Id, id_width(&s.id))),
                ]
                .into_iter()
                .flatten()
                .collect();
                let keep: Vec<Piece> = desk
                    .header_pop
                    .open
                    .as_ref()
                    .filter(|pop| {
                        pop.host == crate::desktop::header_pop::Host::Tile(p.pane_id.clone())
                    })
                    .and_then(|pop| match pop.what {
                        What::Usage => Some(Piece::Usage),
                        What::Details => Some(Piece::Provenance),
                        What::Chain => Some(Piece::Role),
                        What::Ask => Some(Piece::Ask),
                        What::Seed => Some(Piece::Seed),
                        What::SeedCard => None,
                    })
                    .into_iter()
                    .chain(crate::outpost::offline(s, &facts.outposts).then_some(Piece::Host))
                    .collect();
                sub.levels = fit(&Row {
                    available,
                    gap: TileHeader::subline_gap(),
                    fixed: &[],
                    pieces: &pieces,
                    rank: super::fit::SUBLINE_RANK,
                    keep: &keep,
                });
            }
            Some((p.pane_id.clone(), sub))
        })
        .collect()
}

fn id_face(short: String, ink: rune::Color) -> HeaderPiece {
    let width = attn_ui::system::width_of(short.clone(), attn_ui::system::Type::Mono);
    HeaderPiece::new().drawn(
        attn_ui::system::words(short, attn_ui::system::Type::Mono, ink).single_line(),
        width,
    )
}

fn id_width(session: &str) -> Widths {
    Widths {
        full: id_face(short(session).into_owned(), rune::Color::TRANSPARENT).button_width(),
        short: None,
        icon: None,
    }
}

fn settles(desktop: &Desktop, facts: &attn_client::Facts) -> Vec<(String, Settle)> {
    desktop
        .panes
        .iter()
        .filter_map(|p| {
            let session = facts.sessions.iter().find(|s| s.id == p.session_id)?;
            Some((p.pane_id.clone(), Settle::of(session, &facts.outposts)?))
        })
        .collect()
}

/// The keys a tile names beside the buttons that do the same.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
struct HeaderKeys {
    keep: Option<String>,
    pop_out: Option<String>,
    focus: Option<String>,
    send_annotations: Option<String>,
    send_marks: Option<String>,
    find_note: Option<String>,
}

fn header_keys(state: &State) -> HeaderKeys {
    let key_of = |action: &crate::action::Action| {
        state.keymap.binding(action).map(|b| state.keymap.label(b))
    };
    HeaderKeys {
        keep: key_of(&crate::action::Action::SessionCancelCountdown),
        pop_out: key_of(&crate::action::Action::TilePopOut),
        send_marks: key_of(&crate::action::Action::MarkdownSendAnnotations),
        focus: key_of(&crate::action::Action::TerminalToggleMaximize),
        send_annotations: key_of(&crate::action::Action::TerminalSendAnnotations),
        find_note: key_of(&crate::action::Action::FileOpen),
    }
}

fn with_key(label: &str, key: Option<&String>) -> String {
    match key {
        Some(key) => format!("{label} ({key})"),
        None => label.to_owned(),
    }
}

/// `leaf` alone, filling a window of its own; its header starts `chrome_leading` points in,
/// clear of the window's own controls.
pub(super) fn lone(
    state: &State,
    desktop: &Desktop,
    leaf: &Leaf,
    t: &Tokens,
    scale: f32,
    chrome_leading: f32,
) -> El {
    let settles = settles(desktop, &state.facts);
    let keys = header_keys(state);
    let unsent = state.window.desk.peeks.unsent();
    let sublines = sublines(
        desktop,
        &state.facts,
        &state.window.desk,
        keys.keep.as_deref(),
    );
    let seeds = seed_reads(state);
    let terminal_colors = crate::color_scheme(&state.facts.settings)
        .colors(t.dark)
        .terminal;
    let cx = TileCx {
        desktop,
        seeds: &seeds,
        sessions: &state.facts.sessions,
        sublines: &sublines,
        unsent: &unsent,
        settles: &settles,
        keys: &keys,
        focused: None,
        drains: &state.window.desk.settling,
        dividers: false,
        dragging: None,
        rings: &[],
        refusals: &state.window.desk.refusals,
        mark_anchor: state.window.desk.header_pop.mark_anchor(),
        hints: state.window.hints.shown(),
        screen: super::tile_look::Screen {
            marking: None,
            zoomed: false,
            focus: false,
            lone: true,
        },
        holder: state.window.keyboard.as_ref(),
        terminals: &state.terminals,
        terminal_colors: &terminal_colors,
        contents: &state.facts.tile_contents,
        outposts: &state.facts.outposts,
        pictures: &state.pictures,
        notebook: &state.notebook,
        acting: &leaf.id,
        facts: &state.facts,
        marks: (&state.drafts, &state.readings),
        browsers: &state.browser,
        stale: &stale_builds(&state.facts.sessions, &state.window.desk),
        shared: &super::shared_conversation::notices(
            &state.facts.sessions,
            &state.facts.desktops,
            &desktop.id,
        ),
        t,
        scale,
        now_ms: state.window.now_ms,
        chrome_leading,
    };
    tile(leaf, &cx).w_full().h_full()
}

/// What every tile header on a screen reads: how each turn settles, the keys the header
/// names, and each agent's second line.
type HeaderReads<'a> = (
    &'a [(String, Settle)],
    &'a HeaderKeys,
    &'a [(String, Subline)],
);

fn screen(
    desktop: Option<&Desktop>,
    desk: &super::Desk,
    facts: &attn_client::Facts,
    (settles, keys, sublines): HeaderReads,
    holder: Option<&Holder>,
    (terminals, pictures, notebook, seeds, marks, browsers, hints): (
        &Terminals,
        &crate::markdown::Pictures,
        &crate::notebook::Notebook,
        &crate::garden::TileReads,
        (&crate::markdown::Drafts, &crate::markdown::Readings),
        &crate::browser::Browsers,
        Option<&crate::hints::Shown>,
    ),
    (t, scale, now_ms, focus_style): (&Tokens, f32, i64, super::FocusStyle),
) -> El {
    match desktop {
        None => empty(t, "No desktop"),
        Some(_) if desk.area().is_none() => col(),
        Some(desktop) => match desk.plan(facts, desktop) {
            None => empty(t, "Empty desktop"),
            Some((tree, plan)) => {
                let view = desk.row(&desktop.id).view;
                let rendered = plan.rendered(&tree, &view, &desktop.active_pane_id);
                let dragging = desk.drag.as_ref().and_then(|d| d.dragging());
                let unsent = desk.peeks.unsent();
                let stale = stale_builds(&facts.sessions, desk);
                let shared = super::shared_conversation::notices(
                    &facts.sessions,
                    &facts.desktops,
                    &desktop.id,
                );
                let terminal_colors = crate::color_scheme(&facts.settings).colors(t.dark).terminal;
                let acting = desk.acting_leaf(desktop);
                let cx = TileCx {
                    desktop,
                    seeds,
                    sessions: &facts.sessions,
                    sublines,
                    unsent: &unsent,
                    settles,
                    keys,
                    focused: match &view {
                        super::layout::View::Focused(leaf) => Some(leaf.as_str()),
                        _ => None,
                    },
                    drains: &desk.settling,
                    dividers: view == super::layout::View::Normal,
                    dragging,
                    rings: &desk.rings,
                    refusals: &desk.refusals,
                    mark_anchor: desk.header_pop.mark_anchor(),
                    hints,
                    screen: super::tile_look::Screen {
                        marking: (rendered.leaves().len() > 1).then_some(focus_style),
                        zoomed: view == super::layout::View::Zoomed,
                        focus: matches!(view, super::layout::View::Focused(_)),
                        lone: false,
                    },
                    holder,
                    terminals,
                    terminal_colors: &terminal_colors,
                    contents: &facts.tile_contents,
                    outposts: &facts.outposts,
                    pictures,
                    notebook,
                    acting: &acting,
                    facts,
                    marks,
                    browsers,
                    stale: &stale,
                    shared: &shared,
                    t,
                    scale,
                    now_ms,
                    chrome_leading: 0.0,
                };
                let placed = desk.placement(&rendered);
                let held = desk.held_divider(&placed);
                let shown = super::row_slide::ShownRow::of(
                    &plan,
                    &placed,
                    desk.area().map_or(0.0, |a| a.w),
                );
                laid_out(
                    &rendered,
                    &placed,
                    held,
                    (desk.shown_row.as_ref(), shown),
                    &cx,
                )
            }
        },
    }
}

/// The sessions whose terminal runs an older build than the daemon, unless the person
/// dismissed the notice.
fn stale_builds(sessions: &[Session], desk: &super::Desk) -> Vec<String> {
    sessions
        .iter()
        .filter(|s| s.terminal_build_stale == Some(true))
        .filter(|s| !desk.stale_build_dismissed.contains(&s.id))
        .map(|s| s.id.clone())
        .collect()
}

fn ratio_bits(
    ratios: &std::collections::HashMap<String, super::divider::RatioOverride>,
) -> Vec<(String, u32)> {
    let mut bits: Vec<(String, u32)> = ratios
        .iter()
        .map(|(k, o)| (k.clone(), o.ratio.to_bits()))
        .collect();
    bits.sort();
    bits
}

fn version<T>(arc: &std::sync::Arc<T>) -> usize {
    std::sync::Arc::as_ptr(arc) as usize
}

fn empty(t: &Tokens, message: &str) -> El {
    col()
        .w_full()
        .h_full()
        .center()
        .child(attn_ui::system::words(
            message.to_owned(),
            attn_ui::system::Type::Body,
            t.text_tertiary,
        ))
}

struct TileCx<'a> {
    desktop: &'a Desktop,
    seeds: &'a crate::garden::TileReads<'a>,
    sessions: &'a [Session],
    /// Each agent pane's second header line, by pane.
    sublines: &'a [(String, Subline)],
    /// Replies a peek could not send, by terminal.
    unsent: &'a [(String, String)],
    /// How each agent's turn settles, by leaf.
    settles: &'a [(String, Settle)],
    keys: &'a HeaderKeys,
    /// The leaf this desktop shows alone.
    focused: Option<&'a str>,
    drains: &'a std::collections::HashMap<String, crate::sidebar::Countdown>,
    /// Dividers drag only in the normal view.
    dividers: bool,
    dragging: Option<&'a str>,
    /// Tiles that ring because they arrived on screen.
    rings: &'a [super::arrival::Ring],
    /// Input the daemon refused, by terminal.
    refusals: &'a super::refusal::Refusals,
    /// Where the open card of an id in a terminal hangs.
    mark_anchor: Option<super::header_pop::Anchor>,
    hints: Option<&'a crate::hints::Shown>,
    screen: super::tile_look::Screen,
    holder: Option<&'a Holder>,
    terminals: &'a Terminals,
    terminal_colors: &'a TerminalColors,
    contents: &'a attn_client::TileContents,
    outposts: &'a [attn_protocol::OutpostInfo],
    pictures: &'a crate::markdown::Pictures,
    notebook: &'a crate::notebook::Notebook,
    /// The leaf this window acts on.
    acting: &'a str,
    facts: &'a attn_client::Facts,
    marks: (&'a crate::markdown::Drafts, &'a crate::markdown::Readings),
    browsers: &'a crate::browser::Browsers,
    /// Sessions whose terminal runs an older build, with the notice still up.
    stale: &'a [String],
    /// Sessions whose conversation another terminal also has open, with where it is.
    shared: &'a [(String, String)],
    now_ms: i64,
    t: &'a Tokens,
    scale: f32,
    /// Where the tile header starts, clear of the window's own controls.
    chrome_leading: f32,
}

/// The screen's tiles and dividers as siblings pinned where `placed` lays them, so a tile that
/// moves to another split stays the same element and springs to its new place. When the screen
/// moves along its row from the one `was` shows, tiles leaving it and joining it slide with it.
fn laid_out(
    rendered: &Node,
    placed: &super::layout::Placement,
    held: Option<(String, Direction, [f32; 4])>,
    (was, shown): (
        Option<&super::row_slide::ShownRow>,
        super::row_slide::ShownRow,
    ),
    cx: &TileCx,
) -> El {
    let slide = was.map_or(0.0, |was| was.slide_to(&shown));
    let leaves = rendered.leaves();
    let tiles = placed.leaves.iter().filter_map(|(id, rect)| {
        let leaf = leaves.iter().find(|l| l.id == *id)?;
        let tile = tile(leaf, cx);
        let joins = slide != 0.0 && was.is_some_and(|was| !was.shows(id)) && slides(&leaf.kind);
        let tile = if joins {
            tile.transition(super::row_slide::joining(
                slide,
                Shelf::row_spring(),
                tile_transition(&leaf.kind),
            ))
        } else {
            tile
        };
        Some(pinned(tile, *rect))
    });
    let dividers = placed
        .splits
        .iter()
        .map(|s| (s.id.clone(), s.direction, s.divider, cx.dividers))
        .chain(held.map(|(id, direction, rect)| (id, direction, rect, true)))
        .map(|(id, direction, rect, live)| pinned(divider(id, direction, live), rect));
    let row = el()
        .w_full()
        .h_full()
        .fill(cx.t.bg_tile)
        .children(tiles)
        .children(dividers)
        .child(shown.marker());
    if slide == 0.0 {
        return row;
    }
    row.child_exit(super::row_slide::leaving(slide, Shelf::row_spring()))
}

fn pinned(child: El, [x, y, w, h]: [f32; 4]) -> El {
    child.pin(Some(x), Some(y), None, None).w(w).h(h)
}

/// The strip over the line between a split's two sides, which drags the split's ratio, or
/// between screen columns their widths; a `live` one takes drags.
fn divider(split: String, direction: Direction, live: bool) -> El {
    let key = format!("divider.{split}");
    let bar = rune::el();
    if !live {
        return bar;
    }
    bar.key(key)
        .paint_order(1)
        .role("separator")
        .on_drag({
            let split = split.clone();
            move |state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
                super::divider::gesture(state, d, tx, &split, direction)
            }
        })
        .cursor(match direction {
            Direction::Vertical => rune::Cursor::ResizeColumn,
            Direction::Horizontal => rune::Cursor::ResizeRow,
        })
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
            let dividers = &mut state.window.desk.dividers;
            if dividers.get(&split) == Some(&(rect, direction)) {
                tx.live_only();
                return;
            }
            dividers.insert(split.clone(), (rect, direction));
        })
}

/// The name a leaf goes by in its header, on the shelf and to assistive tech: an agent by its
/// session's name, a tile by what it holds.
pub(crate) fn leaf_title(
    desktop: &Desktop,
    sessions: &[Session],
    harnesses: &[attn_protocol::Harness],
    contents: &attn_client::TileContents,
    seeds: &[std::sync::Arc<attn_protocol::Seed>],
    leaf: &Leaf,
) -> String {
    match &leaf.kind {
        LeafKind::Tile { kind, params, .. } => {
            let key = attn_client::TileKey {
                desktop: desktop.id.clone(),
                tile: leaf.id.clone(),
            };
            crate::sidebar::tile_title(
                kind,
                params.as_deref().unwrap_or(""),
                contents.get(&key).map(|c| &*c.message),
                seeds,
            )
        }
        LeafKind::Pane => {
            let pane = pane_of(desktop, &leaf.id);
            pane.and_then(|p| sessions.iter().find(|s| s.id == p.session_id))
                .map(|s| crate::sidebar::display_name(s, harnesses))
                .or_else(|| pane.map(|p| p.title.clone()).filter(|t| !t.is_empty()))
                .unwrap_or_else(|| "Terminal".into())
        }
    }
}

/// What a screen reader calls a terminal: its title and its agent's state, "Shell · idle".
fn terminal_name(title: &str, session: Option<&Session>) -> String {
    match session {
        Some(session) => format!(
            "{title} · {}",
            crate::sidebar::AgentState::parse(&session.state).spoken()
        ),
        None => title.to_owned(),
    }
}

pub(crate) fn tile_kind_label(kind: &str) -> &str {
    match kind {
        "markdown" => "Markdown",
        "browser" => "Browser",
        "seed" => "Seed",
        "notebook" => "Editor",
        other => other,
    }
}

pub(crate) fn pane_of<'a>(desktop: &'a Desktop, id: &str) -> Option<&'a DesktopPane> {
    desktop.panes.iter().find(|p| p.pane_id == id)
}

/// The session behind leaf `id`: its pane's agent, or the agent its tile was opened for.
pub(crate) fn session_of_leaf(desktop: &Desktop, id: &str) -> Option<String> {
    if let Some(pane) = pane_of(desktop, id).filter(|p| !p.session_id.is_empty()) {
        return Some(pane.session_id.clone());
    }
    match desktop.tree()?.find_leaf(id)? {
        Node::Leaf(Leaf {
            kind: LeafKind::Tile { session, .. },
            ..
        }) => session.clone(),
        _ => None,
    }
}

/// Tile `leaf` with its header.
fn tile(leaf: &Leaf, cx: &TileCx) -> El {
    let t = cx.t;
    let desktop = cx.desktop;
    let active = leaf.id == desktop.active_pane_id;
    let held = cx.holder.is_some_and(|h| h.pane == leaf.id);
    let title = leaf_title(
        desktop,
        cx.sessions,
        cx.seeds.harnesses,
        cx.contents,
        cx.seeds.seeds,
        leaf,
    );
    let holder = Holder {
        desktop: desktop.id.clone(),
        pane: leaf.id.clone(),
    };
    let body = match (&leaf.kind, pane_of(desktop, &leaf.id)) {
        (LeafKind::Pane, Some(pane)) if !pane.runtime_id.is_empty() => {
            let caret = held
                .then(|| ime_caret(cx.terminals, &pane.runtime_id, cx.terminal_colors))
                .flatten();
            let pointing_link = cx.terminals.pointing_link() == Some(pane.runtime_id.as_str())
                || cx.terminals.pointing_mark(&pane.runtime_id);
            let session = cx.sessions.iter().find(|s| s.id == pane.session_id);
            let slot = terminal_slot(
                (&pane.runtime_id, &pane.session_id),
                (&terminal_name(&title, session), held),
                holder.clone(),
                caret,
                pointing_link,
                (cx.terminal_colors, cx.scale),
            );
            let slot = match cx.terminals.finding(&pane.runtime_id) {
                Some(finding) => slot.child(super::find_bar::bar(&pane.runtime_id, finding, t)),
                None => slot,
            };
            let slot = match cx.terminals.filtering(&pane.runtime_id) {
                Some(filtering) => slot.child(super::filter_panel::panel(
                    &pane.runtime_id,
                    filtering,
                    cx.terminals.filter_results_height(&pane.runtime_id),
                    t,
                )),
                None => slot,
            };
            let shared = cx
                .shared
                .iter()
                .find(|(s, _)| *s == pane.session_id)
                .map(|(s, lead)| super::shared_conversation::notice(s, lead, t));
            let stale = cx
                .stale
                .contains(&pane.session_id)
                .then(|| super::stale_build::notice(&pane.session_id, t));
            let notices: Vec<_> = shared.into_iter().chain(stale).collect();
            let slot = if notices.is_empty() {
                slot
            } else {
                let (_, slot_h) = cx.terminals.slot_size(&pane.runtime_id).unwrap_or_default();
                let rows = ROWS_UNDER_NOTICES * cx.terminals.row_height().unwrap_or_default();
                slot.child(attn_ui::blocks::terminal_notices(
                    format!("terminal.{}.notices", pane.runtime_id),
                    notices,
                    (slot_h - rows).max(0.0),
                    t,
                ))
            };
            let slot = match cx.terminals.marking(&pane.runtime_id) {
                Some(marking) => slot.children(super::annotating::layers(
                    &pane.runtime_id,
                    marking,
                    (
                        cx.terminals.slot_size(&pane.runtime_id).unwrap_or_default(),
                        super::annotating::Room::of(cx.terminals, &pane.runtime_id),
                    ),
                    (
                        held.then_some(cx.keys.send_annotations.as_deref())
                            .flatten(),
                        unreachable_host(cx, pane).as_deref(),
                    ),
                    t,
                )),
                None => slot,
            };
            let slot = slot.children(cx.mark_anchor.as_ref().and_then(|(place, at)| {
                let [x, y, w, h] = at.map(f32::from_bits);
                let anchor = || el().key(super::header_pop::mark_anchor(place));
                let here = place.runtime() == Some(&pane.runtime_id);
                here.then(|| anchor().absolute(x, y).w(w).h(h))
            }));
            let slot = slot.children(cx.refusals.flash(&pane.runtime_id, t));
            let slot = slot.children(cx.hints.and_then(|shown| {
                let size = cx.terminals.slot_size(&pane.runtime_id)?;
                crate::hints::layer(shown, &pane.runtime_id, size, cx.terminal_colors, t)
            }));
            let unsent = cx
                .unsent
                .iter()
                .find(|(r, _)| *r == pane.runtime_id)
                .map(|(runtime, reply)| super::peek::unsent_strip(runtime, reply, t));
            let (notice, screen_on) = reach_strip(cx, pane);
            let refused = cx.refusals.strip(&pane.runtime_id, notice, screen_on, t);
            if unsent.is_none() && refused.is_none() {
                slot
            } else {
                col()
                    .w_full()
                    .grow(1.0)
                    .min_h(0.0)
                    .child(slot)
                    .children(refused)
                    .children(unsent)
            }
        }
        (LeafKind::Pane, _) => empty(t, "Starting…"),
        (LeafKind::Tile { kind, session, .. }, _) if kind == "markdown" => {
            let key = attn_client::TileKey {
                desktop: desktop.id.clone(),
                tile: leaf.id.clone(),
            };
            crate::markdown::body(
                (&leaf.id, held),
                &title,
                session.as_deref(),
                cx.contents.get(&key),
                (
                    cx.outposts,
                    crate::review::setup::Servers {
                        sessions: &cx.facts.sessions,
                        settings: &cx.facts.settings,
                        harnesses: &cx.facts.harnesses,
                        now_ms: cx.now_ms,
                    },
                ),
                (cx.pictures, cx.marks.0, cx.marks.1),
                t,
            )
        }
        (LeafKind::Tile { kind, params, .. }, _) if kind == "seed" => crate::garden::tile_body(
            &desktop.id,
            &leaf.id,
            params.as_deref().unwrap_or(""),
            cx.seeds,
            Some(cx.marks.0),
            t,
        ),
        (LeafKind::Tile { kind, .. }, _) if kind == "notebook" => {
            crate::notebook::tile_body(cx.notebook, &leaf.id, cx.keys.find_note.clone(), t)
        }
        (LeafKind::Tile { kind, .. }, _) if kind == crate::browser::KIND => {
            crate::browser::page_area(&browser_page(desktop, leaf), t)
        }
        (LeafKind::Tile { kind, .. }, _) => empty(t, tile_kind_label(kind)),
    };
    let look = super::tile_look::Look::of(
        matches!(leaf.kind, LeafKind::Pane),
        active,
        cx.dragging == Some(leaf.id.as_str()),
        cx.screen,
    );
    let recorded = format!("tile.{}", leaf.id);
    let frame = col()
        .key(recorded.clone())
        .min_w(0.0)
        .min_h(0.0)
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
            let rects = &mut state.window.desk.rects;
            if rects.get(&recorded) == Some(&rect) {
                tx.live_only();
                return;
            }
            rects.insert(recorded.clone(), rect);
        })
        .fill(t.bg_tile)
        .role("group")
        .label(title.clone())
        .on_press_within(move |state: &mut State, tx: &mut Tx| press_tile(state, &holder, tx))
        .transition(tile_transition(&leaf.kind));
    let key = format!("tile.{}", leaf.id);
    let below = TileHeader::height(pane_of(desktop, &leaf.id).is_some());
    let content = col()
        .key(format!("{key}.content"))
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(header(leaf, &title, &look, cx))
        .child(body)
        .children(look.veil(&key, below, t))
        .children(look.marks(&key, t))
        .children(super::arrival::overlay(cx.rings, &leaf.id, t));
    look.frame(frame, t)
        .child(attn_ui::blocks::tile_content(content))
}

fn browser_page(desktop: &Desktop, leaf: &Leaf) -> attn_client::TileKey {
    attn_client::TileKey {
        desktop: desktop.id.clone(),
        tile: leaf.id.clone(),
    }
}

pub(super) fn seed_reads(state: &State) -> crate::garden::TileReads<'_> {
    crate::garden::TileReads {
        garden: &state.window.garden,
        seeds: crate::garden::seeds(state),
        facts: &state.facts,
        v2: crate::garden::v2::enabled(state),
        readings: &state.readings,
        pictures: &state.pictures,
        now_ms: state.window.now_ms,
        harnesses: &state.facts.harnesses,
        chief: crate::garden::model::has_chief(
            &state.facts.sessions,
            &state.facts.profiles.selected,
        ),
        zoom: crate::garden::zoom(state),
        crew_back: crate::crew::back_tile(state),
    }
}

fn holds_a_seed(desktop: &Desktop) -> bool {
    desktop
        .leaves()
        .iter()
        .any(|l| matches!(l.tile(), Some(("seed", _))))
}

/// A document tile's controls for its marks, once its document reads: a markdown file's, or a
/// seed's.
fn document_controls(leaf: &Leaf, cx: &TileCx) -> Option<El> {
    let LeafKind::Tile {
        kind,
        session,
        params,
        ..
    } = &leaf.kind
    else {
        return None;
    };
    let (uri, to) = match kind.as_str() {
        "markdown" => {
            let content = cx.contents.get(&attn_client::TileKey {
                desktop: cx.desktop.id.clone(),
                tile: leaf.id.clone(),
            })?;
            if content.message.path.is_empty() {
                return None;
            }
            let uri = crate::markdown::drafts::Source::File {
                path: content.message.path.clone(),
                host: content.message.host.clone().filter(|h| !h.is_empty()),
            }
            .uri();
            (
                uri,
                crate::markdown::sending::Kind::File {
                    bound: session.clone(),
                    review: content.message.review.is_some(),
                },
            )
        }
        "seed" => {
            let id = params.as_deref().filter(|p| !p.is_empty())?;
            let seed = crate::garden::shown_seed(cx.seeds.garden, cx.seeds.seeds, id)?;
            let uri = crate::markdown::drafts::Source::Seed(id.to_owned()).uri();
            (uri, crate::markdown::sending::Kind::seed(seed))
        }
        _ => return None,
    };
    Some(crate::markdown::sending::controls(
        cx.marks,
        cx.sessions,
        cx.seeds.harnesses,
        cx.desktop,
        &leaf.id,
        &uri,
        &to,
        cx.keys.send_marks.as_deref(),
        cx.t,
    ))
}

/// `el` and what it holds move on the row's spring: launched hard and passing their rest a
/// little, by design.
pub(super) fn moves_with_the_row(el: El) -> El {
    el.motion(Shelf::row_spring())
        .motion_intent(rune::MotionIntent::OVERSHOOT | rune::MotionIntent::IMPULSE)
}

/// How a tile joins and leaves the row: it grows in and shrinks out, fading, and a browser tile
/// snaps.
fn tile_transition(kind: &LeafKind) -> rune::Transition {
    use rune::{Spring, SpringPair, Transition};
    if !slides(kind) {
        return Transition::none();
    }
    Transition::asymmetric(Transition::scale(0.9), Transition::scale(0.92)).springs(SpringPair {
        enter: Shelf::row_spring(),
        exit: Spring::new(0.11, 0.0),
    })
}

/// Whether a tile moves with the row: a browser tile's page snaps instead.
fn slides(kind: &LeafKind) -> bool {
    !matches!(kind, LeafKind::Tile { kind, .. } if kind == crate::browser::KIND)
}

/// The machine of the pane's session while the home cannot reach it.
fn unreachable_host(cx: &TileCx, pane: &DesktopPane) -> Option<String> {
    let session = cx.sessions.iter().find(|s| s.id == pane.session_id)?;
    crate::outpost::offline(session, cx.outposts)
        .then(|| super::host::name(session, cx.outposts))
        .flatten()
}

/// What a remote terminal's strip says of its machine, and the machine whose screen the tile
/// has never shown.
fn reach_strip(cx: &TileCx, pane: &DesktopPane) -> (Option<String>, Option<String>) {
    let Some(session) = cx.sessions.iter().find(|s| s.id == pane.session_id) else {
        return (None, None);
    };
    let notice = crate::outpost::notice(session, cx.outposts);
    let unseen = crate::outpost::offline(session, cx.outposts)
        && cx.terminals.away(&pane.runtime_id) == Some(false);
    let screen_on = unseen
        .then(|| super::host::name(session, cx.outposts))
        .flatten();
    (notice, screen_on)
}

/// What the tiles' strips read of their machines' reach.
fn reaches(
    state: &State,
    desktop: Option<&Desktop>,
) -> Vec<(String, Option<String>, Option<bool>)> {
    let facts = &state.facts;
    desktop
        .into_iter()
        .flat_map(|d| &d.panes)
        .filter_map(|p| {
            let s = facts.sessions.iter().find(|s| s.id == p.session_id)?;
            s.host.as_ref()?;
            Some((
                p.runtime_id.clone(),
                crate::outpost::notice(s, &facts.outposts),
                state.terminals.away(&p.runtime_id),
            ))
        })
        .collect()
}

/// Each outpost's id and name, which a view that names outposts reads.
fn outpost_names(outposts: &[attn_protocol::OutpostInfo]) -> Vec<(String, String)> {
    outposts
        .iter()
        .map(|o| (o.id.clone(), o.name.clone()))
        .collect()
}

/// What dragging the header of the current desktop's tile `leaf` picks up. An agent alone on
/// its desktop has nowhere to go.
pub(crate) fn header_source(state: &State, leaf: &str) -> Option<super::drag::Source> {
    let facts = &state.facts;
    let desktop = facts.current_desktop()?;
    let (tree, _) = state.window.desk.plan(facts, desktop)?;
    let kind = &tree.leaves().into_iter().find(|l| l.id == leaf)?.kind;
    Some(super::drag::Source::Header {
        desktop: desktop.id.clone(),
        leaf: leaf.to_owned(),
        label: super::shelf::title(desktop, facts, &tree, leaf),
        movable: matches!(kind, LeafKind::Tile { .. }) || tree.leaves().len() > 1,
    })
}

/// A tile's header, which the tile area drags the tile by. An agent's header carries a second
/// line under its name.
fn header(leaf: &Leaf, title: &str, look: &super::tile_look::Look, cx: &TileCx) -> El {
    let t = cx.t;
    let key = format!("tile.{}", leaf.id);
    let settle = cx
        .settles
        .iter()
        .find(|(l, _)| *l == leaf.id)
        .map(|(_, s)| s);
    let pane = pane_of(cx.desktop, &leaf.id);
    let session = pane.and_then(|p| cx.sessions.iter().find(|s| s.id == p.session_id));
    let focused = cx.focused == Some(leaf.id.as_str());
    let popped = leaf.window.is_some();
    let sub = cx
        .sublines
        .iter()
        .find(|(pane, _)| *pane == leaf.id)
        .map(|(_, sub)| sub);
    let header = match pane {
        Some(_) => TileHeader::agent(&key),
        None => TileHeader::tile(&key),
    }
    .leading(cx.chrome_leading);
    let header = match session {
        Some(s) => header.mark(crate::sidebar::state_mark(
            &key,
            &crate::sidebar::AgentRow::of(s, &cx.facts.harnesses, cx.outposts, false),
            attn_ui::system::DotSize::Small,
            t,
        )),
        None => header,
    };
    let header = match &leaf.kind {
        LeafKind::Tile { kind, params, .. } if kind == crate::browser::KIND => header.title_field(
            crate::browser::address_input(
                cx.browsers,
                &browser_page(cx.desktop, leaf),
                params.as_deref().unwrap_or(""),
            ),
            t,
        ),
        _ => header.title(title.to_owned(), look.title(t)),
    };
    let header = match pane {
        Some(_) => header.hover(
            [
                session.map(|s| rename_button(&key, s, title, t)),
                (!focused && !popped)
                    .then(|| focus_button(&key, &cx.desktop.id, &leaf.id, title, cx)),
                Some(pop_out_button(
                    &cx.desktop.id,
                    &leaf.id,
                    popped,
                    Place::Agent,
                    cx,
                )),
            ]
            .into_iter()
            .flatten()
            .collect(),
            look.header_under(t),
        ),
        None => header,
    };
    let header = match (settle, pane) {
        (Some(settle), Some(pane)) => header.push(settling_chip(
            &leaf.id,
            settle,
            &pane.session_id,
            cx.keys.keep.as_deref(),
            sub.is_some_and(|sub| sub.headline_level(Piece::Settling) == Level::Icon),
            t,
        )),
        _ => header,
    };
    let header = header.push_all(sub.zip(pane).and_then(|(sub, pane)| {
        let chip = sub.ask.as_ref()?;
        let level = match sub.headline_level(Piece::Ask) {
            Level::Icon | Level::Hidden => crate::asks::ChipLevel::Icon,
            Level::Short => crate::asks::ChipLevel::Short,
            Level::Full => crate::asks::ChipLevel::Full,
        };
        Some(crate::asks::chip(
            &leaf.id,
            &pane.session_id,
            chip,
            level,
            t,
        ))
    }));
    let is_tile = matches!(leaf.kind, LeafKind::Tile { .. });
    let is_document =
        matches!(&leaf.kind, LeafKind::Tile { kind, .. } if kind == "markdown" || kind == "seed");
    let header = header
        .push_all(match &leaf.kind {
            LeafKind::Tile { kind, params, .. } if kind == "notebook" => {
                Some(crate::notebook::root_picker(
                    cx.facts,
                    cx.notebook,
                    (cx.desktop, cx.acting),
                    &leaf.id,
                    params.as_deref().unwrap_or(""),
                    t,
                ))
            }
            _ => None,
        })
        .push_all(document_controls(leaf, cx));
    let header = if is_tile || focused {
        header.actions(
            [
                focused.then(|| return_to_row(&key, &cx.desktop.id, cx)),
                (is_document && !focused && !popped)
                    .then(|| focus_document_button(&key, &cx.desktop.id, &leaf.id, cx)),
                matches!(&leaf.kind, LeafKind::Tile { kind, .. } if kind == crate::browser::KIND)
                    .then(|| reload_button(&key, browser_page(cx.desktop, leaf), cx.t)),
                is_tile.then(|| pop_out_button(&cx.desktop.id, &leaf.id, popped, Place::Tile, cx)),
                is_tile.then(|| close_tile_button(&key, &cx.desktop.id, &leaf.id, cx.t)),
            ]
            .into_iter()
            .flatten()
            .collect(),
        )
    } else {
        header
    };
    let header = match pane {
        Some(pane) => header.subline(
            format!("{key}.subline"),
            subline(&key, &leaf.id, &pane.session_id, sub, t),
            look.subline_opacity(),
        ),
        None => header,
    };
    let header = match (settle, pane) {
        (Some(settle), Some(pane)) => {
            match settling_track(&leaf.id, settle, cx.drains.get(&pane.session_id), cx.t) {
                Some(track) => header.track(track),
                None => header,
            }
        }
        _ => header,
    };
    look.header(header.build(), t)
}

/// The agent header's second line: its seed, its delegation role, the machine it runs on, its usage, where
/// it came from, and its short id, which copies the whole id.
fn subline(key: &str, leaf: &str, session: &str, sub: Option<&Subline>, t: &Tokens) -> Vec<El> {
    let short = short(session).into_owned();
    let id = session.to_owned();
    let at = |piece| sub.and_then(|sub| sub.shows(piece));
    let seed = sub.and_then(|sub| {
        let level = at(Piece::Seed)?;
        Some(super::seed_chip::piece(
            leaf,
            session,
            sub.seed.as_ref()?,
            level,
            t,
        ))
    });
    let role = sub.and_then(|sub| {
        let level = at(Piece::Role)?;
        Some(super::chain::piece(
            leaf,
            session,
            sub.role.as_ref()?,
            level,
            t,
        ))
    });
    let host = sub.and_then(|sub| {
        let level = at(Piece::Host)?;
        Some(super::host::piece(leaf, sub.host.as_ref()?, level, t))
    });
    let usage = sub.and_then(|sub| {
        at(Piece::Usage)?;
        Some(super::usage::piece(
            leaf,
            session,
            sub.usage.as_ref()?,
            sub.usage_open,
            t,
        ))
    });
    let provenance = sub.and_then(|sub| {
        let level = at(Piece::Provenance)?;
        Some(super::provenance::piece(
            leaf,
            session,
            sub.provenance.as_ref()?,
            level,
            t,
        ))
    });
    let shows_id = sub.is_none_or(|sub| sub.shows(Piece::Id).is_some());
    [seed, role, host, usage, provenance]
        .into_iter()
        .flatten()
        .chain(shows_id.then(|| {
            id_face(short.clone(), t.text_tertiary)
                .button(format!("{key}.id"), format!("Copy session id {short}"), t)
                .on_press_tx(move |_: &mut State, tx: &mut Tx| tx.copy(id.clone()))
        }))
        .collect()
}

/// The header's pencil, which renames the agent's session.
fn rename_button(key: &str, session: &Session, title: &str, t: &Tokens) -> El {
    let (id, under) = (session.id.clone(), format!("{key}.rename"));
    header_button(
        under.clone(),
        format!("Rename session {title}"),
        &attn_ui::icon::PENCIL,
        Place::Agent,
        t,
        move |state, tx| {
            let opener = crate::sidebar::rename::Opener::Control(under.clone());
            crate::sidebar::rename::session(state, &id, opener, tx)
        },
    )
}

/// The header's button that shows this tile alone on its desktop.
fn focus_button(key: &str, desktop: &str, leaf: &str, title: &str, cx: &TileCx) -> El {
    let (desktop, leaf) = (desktop.to_owned(), leaf.to_owned());
    header_button(
        format!("{key}.focus"),
        with_key(&format!("Focus agent {title}"), cx.keys.focus.as_ref()),
        &attn_ui::icon::FOCUS_AGENT,
        Place::Agent,
        cx.t,
        move |state, tx| super::actions::focus_leaf(state, &desktop, &leaf, tx),
    )
}

/// The header's button that shows this document alone on its desktop: Focus in words, or its
/// corners alone where the header has less room.
fn focus_document_button(key: &str, desktop: &str, leaf: &str, cx: &TileCx) -> El {
    let room = Room::of(leaf);
    let (desktop, leaf) = (desktop.to_owned(), leaf.to_owned());
    let help = with_key("Focus document", cx.keys.focus.as_ref());
    let press = move |state: &mut State, tx: &mut Tx| {
        super::actions::focus_leaf(state, &desktop, &leaf, tx)
    };
    let button: El = match room {
        Room::Wide => SystemButton::new("Focus")
            .look(Look::Plain)
            .size(Size::Compact)
            .help(help)
            .on_press_tx(press)
            .build(cx.t)
            .into(),
        Room::Medium | Room::Narrow => icon_button(
            format!("{key}.focus"),
            "Focus document",
            &attn_ui::icon::FOCUS_DOCUMENT,
            IconSize::Regular,
        )
        .size(Size::Compact)
        .help(help)
        .on_press_tx(press)
        .build(cx.t)
        .into(),
    };
    button.key(format!("{key}.focus")).label("Focus document")
}

/// The header's button that takes the document or browser tile off its desktop.
fn close_tile_button(key: &str, desktop: &str, leaf: &str, t: &Tokens) -> El {
    let (desktop, leaf) = (desktop.to_owned(), leaf.to_owned());
    header_button(
        format!("{key}.close"),
        "Close tile".to_owned(),
        attn_ui::glyph::TIMES,
        Place::Tile,
        t,
        move |state, tx| super::actions::remove_tile(state, desktop.clone(), leaf.clone(), tx),
    )
}

/// The header's button that puts the tile shown alone back in its row.
fn return_to_row(key: &str, desktop: &str, cx: &TileCx) -> El {
    let desktop = desktop.to_owned();
    header_button(
        format!("{key}.return"),
        with_key("Return to row", cx.keys.focus.as_ref()),
        &attn_ui::icon::UNFOCUS,
        Place::Tile,
        cx.t,
        move |state, tx| super::actions::return_to_row(state, &desktop, tx),
    )
}

/// Which header a button stands in, which sets its size: an agent's beside its name, a
/// document's or a browser's at the header's end.
#[derive(Clone, Copy)]
enum Place {
    Agent,
    Tile,
}

fn header_button(
    key: String,
    label: String,
    glyph: impl Into<attn_ui::Glyph>,
    place: Place,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let (size, icon, ink) = match place {
        Place::Agent => (Size::Small, IconSize::Small, t.text_dimmed),
        Place::Tile => (Size::Compact, IconSize::Regular, t.text_muted),
    };
    icon_button(key, label, glyph, icon)
        .size(size)
        .ink(ink)
        .on_press_tx(press)
        .build(t)
        .into()
}

/// The header's button that loads a browser tile's page again.
fn reload_button(key: &str, page: attn_client::TileKey, t: &Tokens) -> El {
    header_button(
        format!("{key}.reload"),
        "Reload browser".into(),
        attn_ui::glyph::RELOAD,
        Place::Tile,
        t,
        move |_, _| crate::browser::reload(&page),
    )
}

/// The header's button that pops the tile out into its own window, or brings it back.
fn pop_out_button(desktop: &str, leaf: &str, popped: bool, place: Place, cx: &TileCx) -> El {
    let (label, glyph) = if popped {
        ("Bring back tile", &attn_ui::icon::BRING_BACK)
    } else {
        ("Pop out tile", &attn_ui::icon::POP_OUT)
    };
    let (desktop, leaf_id) = (desktop.to_owned(), leaf.to_owned());
    header_button(
        format!("tile.{leaf}.popout"),
        with_key(label, cx.keys.pop_out.as_ref()),
        glyph,
        place,
        cx.t,
        move |state, tx| super::windows::toggle_leaf(state, &desktop, &leaf_id, tx),
    )
}

/// The header's mark of a turn that settles on its own, or that the person kept; pressing it
/// keeps the turn, or undoes keeping it.
fn settling_said(settle: &Settle) -> (&'static str, &'static str, &'static str, Settling) {
    match settle {
        Settle::Counting(_) => ("Keep this turn", "SETTLING…", "keep", Settling::Counting),
        Settle::Held => (
            "Keep this turn",
            "SETTLING PAUSED",
            "keep",
            Settling::Paused,
        ),
        Settle::Kept => (
            "Turn kept; undo to let it auto-settle again",
            "TURN KEPT",
            "undo",
            Settling::Kept,
        ),
        Settle::Frozen => (
            "Settling paused while the host is unreachable",
            "SETTLING PAUSED",
            "",
            Settling::Paused,
        ),
    }
}

/// The widths of the settling chip in full and as its mark alone.
fn settling_widths(settle: &Settle, keep_key: Option<&str>) -> Widths {
    let (_, said, verb, _) = settling_said(settle);
    let keys = keep_key
        .filter(|_| *settle != Settle::Frozen)
        .map(|key| (key, verb));
    let (full, mark) = SettlingChip::widths(said, keys);
    Widths {
        full,
        short: None,
        icon: Some(mark),
    }
}

fn settling_chip(
    leaf: &str,
    settle: &Settle,
    session: &str,
    keep_key: Option<&str>,
    icon: bool,
    t: &Tokens,
) -> El {
    let (label, said, verb, stands) = settling_said(settle);
    let session = session.to_owned();
    let frozen = *settle == Settle::Frozen;
    SettlingChip::new(format!("tile.{leaf}.settling"), label, said, stands)
        .keys(
            keep_key
                .filter(|_| !frozen)
                .map(|key| (key.to_owned(), verb)),
        )
        .mark_only(icon)
        .disabled(frozen)
        .build(t, move |state: &mut State| {
            let _ = state.daemon.send(&attn_protocol::CancelCountdownMessage {
                session_id: session.clone(),
                ..attn_protocol::CancelCountdownMessage::new()
            });
        })
}

/// The hairline along the header's foot that drains to the countdown's deadline, or stands
/// dimmed while the countdown is held.
fn settling_track(
    leaf: &str,
    settle: &Settle,
    drain: Option<&crate::sidebar::Countdown>,
    t: &Tokens,
) -> Option<El> {
    if *settle == Settle::Kept {
        return None;
    }
    let line = rune::Shape::new().view_box(1.0, 1.0).line(
        [0.0, 0.5],
        [1.0, 0.5],
        1.0,
        rune::LineCap::Butt,
        t.settle,
    );
    let fill = rune::canvas(line)
        .key(format!("tile.{leaf}.settling.track"))
        .w_full()
        .h_full();
    let fill = match (settle, drain) {
        (Settle::Counting(_), Some(c)) => fill.live_trim(&c.drain),
        _ => fill.opacity(0.45),
    };
    Some(
        el().w_full()
            .h_full()
            .fill(t.settle.alpha(0.16))
            .child(fill),
    )
}

/// The input method's caret in the terminal holding the keyboard: the cell text goes to
/// next, where the candidate window opens, showing what the input method composes so far.
/// The caret moves with the terminal's cursor without painting the window again, until the
/// input method composes: then the caret is an element at the cursor holding what it composes.
fn ime_caret(terminals: &Terminals, runtime_id: &str, theme: &TerminalColors) -> Option<El> {
    let at = terminals.caret(runtime_id)?;
    let key = format!("terminal.{runtime_id}.caret");
    let (Some(preedit), Some((size, h))) = (
        terminals.preedit(runtime_id),
        terminals.cell_font(runtime_id),
    ) else {
        return Some(el().key(key).absolute(0.0, 0.0).ime_cursor_at(at));
    };
    let caret = el().key(key).absolute(0.0, 0.0).live_rect(at).ime_cursor();
    let color = attn_ui::colors::rgb;
    Some(
        caret.child(
            attn_ui::blocks::preedit(
                preedit.to_owned(),
                size,
                h,
                color(theme.fg),
                color(theme.bg),
            )
            .key(format!("terminal.{runtime_id}.preedit"))
            .absolute(0.0, 0.0),
        ),
    )
}

/// Where a terminal draws: the renderer finds it by its mark, and its laid-out size is the
/// size the terminal asks its PTY for. While it holds the keyboard it holds the input method's
/// `caret`.
fn terminal_slot(
    (runtime_id, session): (&str, &str),
    (title, held): (&str, bool),
    tile: Holder,
    caret: Option<El>,
    pointing_link: bool,
    (theme, scale): (&TerminalColors, f32),
) -> El {
    let id = runtime_id.to_owned();
    let (pointing, pressing, wheeling, leaving) = (id.clone(), id.clone(), id.clone(), id.clone());
    let (moved_over, left, reader) = (session.to_owned(), session.to_owned(), session.to_owned());
    let reading = id.clone();
    let slot = el()
        .key(crate::terminal::slot_key(runtime_id))
        .role("terminal")
        .label(title.to_owned())
        .holds_keys(held)
        .a11y_text_from(move |state: &State| state.terminals.reader_text(&reading))
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .fill(attn_ui::colors::rgb(theme.bg))
        .effect(crate::terminal::mark(runtime_id))
        .on_layout_tx(move |state: &mut State, [_, _, w, h], tx: &mut Tx| {
            state
                .terminals
                .laid_out(&id, (w, h), scale, tx, &state.daemon);
            tx.live_only();
        })
        .on_pointer_move_local(move |state: &mut State, p, tx: &mut Tx| {
            let mods = (report_mods(tx.mods()), accelerator(tx.mods()));
            let now = tx.clock();
            let held = tx.pointer_held();
            let pointing_changed = state.terminals.pointer_moved(
                &pointing,
                (p.x, p.y),
                held,
                mods,
                now,
                &state.daemon,
            );
            let over_mark = (tx.mods().alt && !held).then_some((p.x, p.y));
            let mark_changed = state.terminals.hover_mark(&pointing, over_mark);
            super::header_pop::pointer(state, &pointing, &moved_over, !held, tx);
            if !pointing_changed && !mark_changed {
                tx.live_only();
            }
        })
        .on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
            if !inside {
                super::header_pop::pointer(state, &leaving, &left, false, tx);
            }
            let mark_changed = !inside && state.terminals.hover_mark(&leaving, None);
            if (inside || !state.terminals.pointer_left(&leaving)) && !mark_changed {
                tx.live_only();
            }
        })
        .cursor(if pointing_link {
            rune::Cursor::Pointer
        } else {
            rune::Cursor::Text
        })
        .pointer_buttons([PointerButton::Middle, PointerButton::Secondary])
        .on_drag(move |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            let mods = report_mods(tx.mods());
            match d.phase {
                rune::Phase::Down => {
                    crate::annotations::draft::pressed_anywhere(state, tx);
                    let button = match tx.button() {
                        PointerButton::Middle => mouse::MIDDLE,
                        PointerButton::Secondary => mouse::SECONDARY,
                        _ => mouse::PRIMARY,
                    };
                    let opens = button == mouse::PRIMARY
                        && accelerator(tx.mods())
                        && state.terminals.presses_mark(&pressing, (d.x, d.y));
                    let reported = !opens
                        && state.terminals.pointer_pressed(
                            &pressing,
                            button,
                            (d.x, d.y),
                            mods,
                            &state.daemon,
                        );
                    if !reported
                        && button == mouse::PRIMARY
                        && let Some(text) = state.terminals.select_press(
                            &pressing,
                            (d.x, d.y),
                            d.clicks,
                            tx.mods().alt,
                        )
                    {
                        tx.copy(text);
                    }
                }
                rune::Phase::Move => {
                    state.terminals.select_drag(&pressing, (d.x, d.y));
                    tx.live_only();
                }
                rune::Phase::Up => {
                    let at = (!d.cancelled()).then_some((d.x, d.y));
                    let mods = if d.cancelled() { 0 } else { mods };
                    state
                        .terminals
                        .pointer_released(&pressing, at, mods, &state.daemon);
                    let at = (d.x, d.y);
                    match state.terminals.select_release(&pressing) {
                        Release::Copied(text) => tx.copy(text),
                        Release::Opened(mark) if !d.cancelled() => {
                            crate::annotations::draft::opened(state, &pressing, &mark, at, tx);
                        }
                        Release::Anchored(anchor) if !d.cancelled() => {
                            crate::annotations::draft::anchored(state, &pressing, &anchor, at, tx);
                        }
                        Release::Missed(miss, text) => {
                            if !d.cancelled() {
                                crate::annotations::draft::missed(state, &pressing, miss, at, tx);
                            }
                            if let Some(text) = text {
                                tx.copy(text);
                            }
                        }
                        Release::Click if !d.cancelled() && accelerator(tx.mods()) => {
                            if let Some(mark) = state.terminals.mark_at(&pressing, at) {
                                let place = super::header_pop::Place::Terminal {
                                    runtime: pressing.clone(),
                                    reader: reader.clone(),
                                };
                                super::header_pop::open_mark(state, place, mark, tx);
                                return;
                            }
                            match state.terminals.open_link_at(&pressing, (d.x, d.y), tx) {
                                Ok(Some(markdown)) => {
                                    let session = state
                                        .facts
                                        .desktops
                                        .iter()
                                        .find(|desktop| desktop.id == tile.desktop)
                                        .and_then(|desktop| session_of_leaf(desktop, &tile.pane));
                                    crate::opener::open_markdown(
                                        state, markdown, None, session, tx,
                                    );
                                }
                                Ok(None) => {}
                                Err(refusal) => state.window.notify(refusal, tx),
                            }
                        }
                        Release::Click if !d.cancelled() => {
                            state.terminals.click(&pressing, (d.x, d.y));
                        }
                        _ => {}
                    }
                    if !d.cancelled() && tx.button() == PointerButton::Secondary {
                        super::terminal_menu::open(
                            state,
                            &pressing,
                            (d.x, d.y),
                            d.window_point(),
                            tx,
                        );
                    }
                    tx.live_only();
                }
            }
        })
        .on_wheel(move |state: &mut State, e, tx: &mut Tx| {
            tx.live_only();
            state
                .terminals
                .wheel(&wheeling, e.dy, (e.x, e.y), &state.daemon)
        });
    slot.children(caret)
}

/// A press anywhere in `tile`, on its body or a control inside, closes the peek and makes it the
/// desktop's active tile, holding the window's keyboard. A press on the header's own surface may
/// start a drag, so only its click takes the tile.
fn press_tile(state: &mut State, tile: &Holder, tx: &mut Tx) {
    super::peek::dismiss(state);
    super::actions::activate_pressed(state, &tile.desktop, &tile.pane, tx);
}

fn report_mods(mods: Mods) -> u8 {
    mouse::modifiers(mods.shift, mods.alt, mods.ctrl)
}
