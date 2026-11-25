//! The overview as it draws: the row in miniature, the strips around it, a line per tile that
//! needs the user and the legend.

use attn_client::Desktop;
use attn_ui::Tokens;
use attn_ui::blocks::{MemberState, Miniature, Overview as Block, ShelfMember};
use rune::{El, Tx, el, memo};

use super::layout::{self, Column, Leaf, LeafKind, Node, Rows};
use super::overview::{self, Overview, Spot};
use crate::State;

/// Where the overview puts the shown row, in points from the layer's top left corner.
struct Arrangement {
    columns: Vec<Column>,
    on_screen: std::ops::Range<usize>,
    /// The points a column is drawn from.
    slot: (f32, f32),
    rows: Rows,
    /// The row's room.
    area: [f32; 4],
    above: f32,
    lines: f32,
    below: f32,
    legend: f32,
}

struct Needing {
    leaf: String,
    member: ShelfMember,
    asks: String,
}

fn strips(state: &State, shown: &str) -> (usize, usize) {
    let desktops = overview::ordered(state);
    let at = desktops.iter().position(|d| d.id == shown).unwrap_or(0);
    (at, desktops.len().saturating_sub(at + 1))
}

/// The gap a block of `height` takes with the room before the next one; none when empty.
fn with_gap(height: f32) -> f32 {
    if height > 0.0 {
        height + Block::GAP
    } else {
        0.0
    }
}

fn arrange(state: &State, desktop: &Desktop, lines: usize) -> Arrangement {
    let (columns, on_screen) = overview::row_of(state, desktop);
    let (w, h) = state.window.main_size;
    let (before, after) = strips(state, &desktop.id);
    let pad = Block::PADDING;
    let above = with_gap(before as f32 * Block::STRIP_STEP);
    let lines = with_gap(lines as f32 * Block::LINE);
    let below = with_gap((after + 1) as f32 * Block::STRIP_STEP);
    let legend = h - pad - Block::LINE;
    let area = [
        pad,
        pad + above,
        (w - crate::shell::dock_width(state) - 2.0 * pad).max(0.0),
        (legend - below - lines - Block::GAP - pad - above).max(0.0),
    ];
    let slot = (w / on_screen.len().max(1) as f32, h);
    let rows = if columns.is_empty() || w <= 0.0 || h <= 0.0 {
        Rows::default()
    } else {
        layout::rows(columns.len(), slot, (area[2], area[3]))
    };
    let after_row = area[1] + area[3] + Block::GAP;
    Arrangement {
        columns,
        on_screen,
        slot,
        rows,
        area,
        above: pad,
        lines: after_row,
        below: after_row + lines,
        legend,
    }
}

fn needing(state: &State, desktop: &Desktop) -> Vec<Needing> {
    let facts = &state.facts;
    let (columns, _) = overview::row_of(state, desktop);
    let Some(tree) = desktop.tree() else {
        return Vec::new();
    };
    let unseen = state.window.desk.row(&desktop.id).unseen;
    columns
        .iter()
        .flat_map(|c| &c.leaf_ids)
        .filter_map(|leaf| {
            let member = super::shelf::member(desktop, facts, tree, &unseen, leaf);
            let word = member.state.and_then(MemberState::needs)?;
            let asked = super::view::pane_of(desktop, leaf)
                .and_then(|pane| facts.sessions.iter().find(|s| s.id == pane.session_id))
                .and_then(|session| crate::asks::chip_of(facts, session));
            let asks = match asked {
                Some(ask) if ask.more > 0 => format!("{} and {} more", ask.subject, ask.more),
                Some(ask) => ask.subject,
                None => word.to_owned(),
            };
            Some(Needing {
                leaf: leaf.clone(),
                member,
                asks,
            })
        })
        .collect()
}

/// The terminals the open overview shows and the scale it draws them at.
pub(crate) fn miniatures(state: &State) -> Option<(Vec<String>, f32)> {
    let overview = overview::open(state)?;
    let desktop = overview::desktop_of(state, &overview.at().desktop)?;
    let scale = arrange(state, desktop, needing(state, desktop).len())
        .rows
        .scale;
    let terminals = desktop.panes.iter().map(|p| p.runtime_id.clone()).collect();
    (scale > 0.0).then_some((terminals, scale))
}

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let overview = overview::open(state)?;
    let layer = Block::layer(overview::LAYER, t)
        .role("listbox")
        .label("Desktop overview")
        .focusable(true)
        .no_focus_ring()
        .on_press_tx(overview::close);
    let Some(desktop) = overview::desktop_of(state, &overview.at().desktop) else {
        return Some(layer);
    };
    let needing = needing(state, desktop);
    let placed = arrange(state, desktop, needing.len());
    let layer = match &overview.at().leaf {
        Some(leaf) => layer.active_descendant(miniature_key(leaf)),
        None => layer,
    };
    let (row, bars) = row(state, overview, desktop, &placed, t);
    let desktops = overview::ordered(state);
    let (before, _) = strips(state, &desktop.id);
    let row_w = placed.area[2];
    let at = |y: f32, e: El| el().absolute(Block::PADDING, y).w(row_w).child(e.w(row_w));
    let strips_above = desktops[..before].iter().enumerate().map(|(i, d)| {
        at(
            placed.above + i as f32 * Block::STRIP_STEP,
            strip(state, d, t),
        )
    });
    let strips_below = desktops[before + 1..].iter().enumerate().map(|(i, d)| {
        at(
            placed.below + i as f32 * Block::STRIP_STEP,
            strip(state, d, t),
        )
    });
    let new_at = placed.below + (desktops.len() - before - 1) as f32 * Block::STRIP_STEP;
    let lines = needing.iter().enumerate().map(|(i, n)| {
        let spot = spot(desktop, &n.leaf);
        let line = Block::line(format!("overview.line.{}", n.leaf), &n.member, &n.asks, t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                overview::land(state, spot.clone(), tx)
            });
        at(placed.lines + i as f32 * Block::LINE, line)
    });
    let empty = placed
        .columns
        .is_empty()
        .then(|| Block::empty(placed.area, t));
    Some(
        layer
            .children(strips_above)
            .children(row)
            .children(bars)
            .children(empty)
            .children(lines)
            .children(strips_below)
            .child(at(
                new_at,
                Block::new_desktop("overview.new", t).on_press_tx(overview::create),
            ))
            .child(at(
                placed.legend,
                Block::legend(legend(state, overview, desktop), t),
            )),
    )
}

pub(crate) fn miniature_key(leaf: &str) -> String {
    format!("overview.miniature.{leaf}")
}

fn spot(desktop: &Desktop, leaf: &str) -> Spot {
    Spot {
        desktop: desktop.id.clone(),
        leaf: Some(leaf.to_owned()),
    }
}

fn row(
    state: &State,
    overview: &Overview,
    desktop: &Desktop,
    placed: &Arrangement,
    t: &Tokens,
) -> (Vec<El>, Vec<El>) {
    let Some(tree) = desktop.tree() else {
        return (Vec::new(), Vec::new());
    };
    let unseen = state.window.desk.row(&desktop.id).unseen;
    let scale = placed.rows.scale;
    let mut columns = Vec::new();
    let mut bars = Vec::new();
    for (i, (column, slot)) in placed.columns.iter().zip(&placed.rows.slots).enumerate() {
        let [x, y, w, h] = *slot;
        let rect = [placed.area[0] + x, placed.area[1] + y, w, h];
        let on_screen = placed.on_screen.contains(&i);
        let inside = layout::place(&column.node, placed.slot, 0.0, &|split| {
            let shown = state.window.desk.ratios.get(&split.id);
            shown.map_or(split.ratio, |o| o.ratio)
        });
        let tiles = inside.leaves.iter().filter_map(|(id, at)| {
            let Some(Node::Leaf(leaf)) = tree.find_leaf(id) else {
                return None;
            };
            let member = super::shelf::member(desktop, &state.facts, tree, &unseen, id);
            let highlighted = overview.at().leaf.as_deref() == Some(id.as_str());
            let full = (at[2], at[3] - Miniature::HEADER);
            let picture = picture(state, desktop, leaf, &member, full, scale, t);
            let here = spot(desktop, id);
            let said = [
                (highlighted, "highlighted"),
                (on_screen, "on screen"),
                (member.needs_user(), "needs you"),
            ];
            let said: Vec<&str> = said.iter().filter(|(is, _)| *is).map(|(_, w)| *w).collect();
            Some(
                Miniature::tile(
                    miniature_key(id),
                    &member,
                    highlighted,
                    at.map(|points| points * scale),
                    scale,
                    picture,
                    t,
                )
                .a11y_value(said.join(", "))
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    overview::land(state, here.clone(), tx)
                }),
            )
        });
        let key = format!("overview.column.{}", column.first());
        columns.push(Block::column(key.clone(), rect, t).children(tiles));
        if on_screen {
            bars.push(Block::on_screen(format!("{key}.on_screen"), rect, t));
        }
    }
    (columns, bars)
}

/// What a miniature shows under its label: a terminal's screen, the document or seed of the
/// current desktop, or a card.
fn picture(
    state: &State,
    desktop: &Desktop,
    leaf: &Leaf,
    member: &ShelfMember,
    full: (f32, f32),
    scale: f32,
    t: &Tokens,
) -> El {
    let key = miniature_key(&leaf.id);
    let current = state
        .facts
        .current_desktop()
        .is_some_and(|d| d.id == desktop.id);
    let drawn = match &leaf.kind {
        LeafKind::Pane => super::view::pane_of(desktop, &leaf.id)
            .filter(|pane| state.terminals.pictured(&pane.runtime_id))
            .map(|pane| {
                let scheme = crate::color_scheme(&state.facts.settings);
                el().fill(attn_ui::colors::rgb(scheme.colors(t.dark).terminal.bg))
                    .effect(crate::terminal::miniature_mark(&pane.runtime_id))
            }),
        LeafKind::Tile { kind, params, .. } if current => {
            let body = match kind.as_str() {
                "markdown" => document(state, desktop, &leaf.id, full.0, t),
                "seed" => Some(seed(state, desktop, &leaf.id, params.as_deref(), t)),
                _ => None,
            };
            body.map(|body| Miniature::body(body, full, scale, t))
        }
        LeafKind::Tile { .. } => None,
    };
    match drawn {
        Some(drawn) => drawn.key(format!("{key}.picture")),
        None => Miniature::card(member.icon.clone(), &member.name, t).key(format!("{key}.card")),
    }
}

fn document(state: &State, desktop: &Desktop, leaf: &str, width: f32, t: &Tokens) -> Option<El> {
    let key = attn_client::TileKey {
        desktop: desktop.id.clone(),
        tile: leaf.to_owned(),
    };
    let view = format!("{}.document", miniature_key(leaf));
    let content = state.facts.tile_contents.get(&key)?;
    crate::markdown::still(view, width, content, &state.pictures, t)
}

fn seed(state: &State, desktop: &Desktop, leaf: &str, params: Option<&str>, t: &Tokens) -> El {
    let reads = super::view::seed_reads(state);
    let version = (
        (
            std::sync::Arc::as_ptr(&state.facts.garden) as usize,
            state.window.garden.revision,
            std::sync::Arc::as_ptr(&state.facts.sessions) as usize,
        ),
        (state.pictures.revision, state.readings.revision),
        (state.window.now_ms / 60_000, reads.zoom.to_bits(), *t),
    );
    let tile = miniature_key(leaf);
    memo(format!("{tile}.seed"), version, || {
        let id = params.unwrap_or("");
        crate::garden::tile_body(&desktop.id, &tile, id, &reads, None, t)
    })
}

fn strip(state: &State, desktop: &Desktop, t: &Tokens) -> El {
    let facts = &state.facts;
    let (columns, _) = overview::row_of(state, desktop);
    let unseen = state.window.desk.row(&desktop.id).unseen;
    let members: Vec<Vec<ShelfMember>> = desktop.tree().map_or_else(Vec::new, |tree| {
        let member = |leaf: &String| super::shelf::member(desktop, facts, tree, &unseen, leaf);
        columns
            .iter()
            .map(|c| c.leaf_ids.iter().map(member).collect())
            .collect()
    });
    let waiting = members.iter().flatten().filter(|m| m.needs_user()).count();
    let id = desktop.id.clone();
    Block::strip(
        format!("overview.strip.{id}"),
        desktop.shortcut_slot.map(|slot| slot.to_string()),
        &crate::sidebar::model::desktop_title(desktop, &facts.desktops),
        &members,
        &counted(members.len(), waiting),
        t,
    )
    .on_press_tx(move |state: &mut State, tx: &mut Tx| overview::preview(state, &id, tx))
}

fn counted(columns: usize, waiting: usize) -> String {
    let columns = match columns {
        1 => "1 column".to_owned(),
        n => format!("{n} columns"),
    };
    match waiting {
        0 => columns,
        1 => format!("{columns}, 1 waits"),
        n => format!("{columns}, {n} wait"),
    }
}

fn legend(state: &State, overview: &Overview, desktop: &Desktop) -> Vec<(Vec<String>, String)> {
    let named = |spot: &Spot| {
        let on = overview::desktop_of(state, &spot.desktop)?;
        let leaf = spot.leaf.as_deref()?;
        Some(super::shelf::title(on, &state.facts, on.tree()?, leaf))
    };
    let shown = crate::sidebar::model::desktop_title(desktop, &state.facts.desktops);
    let lands = match named(overview.at()) {
        Some(_) if overview.at() == overview.from() => "zoom back in".to_owned(),
        Some(name) => format!("land on {name}"),
        None => format!("go to {shown}"),
    };
    let sends = overview::sendable(overview)
        .and_then(|_| named(overview.from()))
        .map(|name| (vec!["⇧", "↵"], format!("send {name} to {shown}")));
    [
        (vec!["←", "→"], "row".to_owned()),
        (
            vec!["↑", "↓"],
            "stack, then the desktop above or below".to_owned(),
        ),
        (vec!["↵"], lands),
    ]
    .into_iter()
    .chain(sends)
    .chain([
        (vec!["1–9"], "desktop".to_owned()),
        (vec!["esc"], "close".to_owned()),
    ])
    .map(|(keys, what)| (keys.into_iter().map(str::to_owned).collect(), what))
    .collect()
}
