//! The panel beside the question in the full-screen Asks: the seeds and markdown files an ask
//! names, each in a tab, read without leaving the ask.

use std::collections::HashMap;

use attn_protocol::{TileDocumentReadMessage, TileDocumentReadResultMessage};
use attn_ui::blocks::{Inline, Prose, Sheet};
use attn_ui::system::{Button, IconSize, Look, Size, icon_button};
use attn_ui::{Tokens, glyph};
use rune::{El, ScrollSpec, Tx, col, memo};
use rune_kit::ScrollBar;

use crate::State;
use crate::markdown::links;

/// What a panel tab shows.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Reference {
    Seed(String),
    /// A markdown file on the machine `host` names, absent for the home's.
    Markdown {
        host: Option<String>,
        path: String,
    },
}

/// One ask's panel: its tabs in the order they opened, and the one it shows.
#[derive(Default)]
pub(crate) struct Panel {
    pub(crate) tabs: Vec<Reference>,
    pub(crate) shown: usize,
}

/// A markdown file's text as its machine last answered.
pub(crate) enum Read {
    Reading,
    Text(String),
    Failed(String),
}

/// Every ask's panel, and the markdown their tabs read.
#[derive(Default)]
pub(crate) struct Panels {
    by_ask: HashMap<String, Panel>,
    reads: HashMap<(Option<String>, String), Read>,
}

impl Panels {
    pub(crate) fn of(&self, ask: &str) -> Option<&Panel> {
        self.by_ask.get(ask).filter(|p| !p.tabs.is_empty())
    }

    /// Forgets the panels of asks no longer listed, and what no tab reads any more.
    pub(crate) fn keep(&mut self, listed: &[String]) {
        self.by_ask.retain(|ask, _| listed.contains(ask));
        self.forget_unread();
    }

    fn forget_unread(&mut self) {
        let read: Vec<(Option<String>, String)> = (self.by_ask.values())
            .flat_map(|p| p.tabs.iter())
            .filter_map(|tab| match tab {
                Reference::Markdown { host, path } => Some((host.clone(), path.clone())),
                Reference::Seed(_) => None,
            })
            .collect();
        self.reads.retain(|file, _| read.contains(file));
    }
}

impl Panel {
    pub(crate) fn shown(&self) -> Option<&Reference> {
        self.tabs.get(self.shown)
    }
}

fn shown_ask(state: &State) -> Option<String> {
    super::is_open(state)
        .then(|| state.shown_ask().map(|(id, _)| id))
        .flatten()
}

/// The seed the panel of the ask on screen shows, which the garden then reads.
pub(crate) fn shown_seed(state: &State) -> Option<String> {
    let panels = &state.window.asks.panels;
    match panels.of(&shown_ask(state)?)?.shown()? {
        Reference::Seed(id) => Some(id.clone()),
        Reference::Markdown { .. } => None,
    }
}

/// The markdown the panel of the ask on screen shows: its machine, path and text.
pub(crate) fn shown_markdown(state: &State) -> Option<(Option<String>, String, String)> {
    let panels = &state.window.asks.panels;
    let Reference::Markdown { host, path } = panels.of(&shown_ask(state)?)?.shown()? else {
        return None;
    };
    match panels.reads.get(&(host.clone(), path.clone()))? {
        Read::Text(text) => Some((host.clone(), path.clone(), text.clone())),
        Read::Reading | Read::Failed(_) => None,
    }
}

/// Shows `reference` in the panel of the ask on screen, in its tab when one already holds it,
/// reading a markdown file again from its machine.
pub(crate) fn open(state: &mut State, reference: Reference, tx: &mut Tx) {
    let Some(ask) = shown_ask(state) else {
        return;
    };
    let panel = state.window.asks.panels.by_ask.entry(ask).or_default();
    panel.shown = match panel.tabs.iter().position(|tab| *tab == reference) {
        Some(at) => at,
        None => {
            panel.tabs.push(reference.clone());
            panel.tabs.len() - 1
        }
    };
    match reference {
        Reference::Markdown { host, path } => read(state, host, path, tx),
        Reference::Seed(_) => crate::garden::follow(state, tx),
    }
}

/// Opens seed `id` in the panel, as a link to it in the panel's own pages does.
pub(crate) fn open_seed(state: &mut State, id: &str, tx: &mut Tx) {
    open(state, Reference::Seed(id.to_owned()), tx);
}

fn read(state: &mut State, host: Option<String>, path: String, tx: &mut Tx) {
    let file = (host.clone(), path.clone());
    let reads = &mut state.window.asks.panels.reads;
    if !matches!(reads.get(&file), Some(Read::Text(_))) {
        reads.insert(file.clone(), Read::Reading);
    }
    state.ask(
        |id| TileDocumentReadMessage {
            host,
            path,
            request_id: Some(id),
            ..TileDocumentReadMessage::new()
        },
        move |state, answer, _| {
            let read = match answer
                .outcome()
                .map(|reply| reply.parse::<TileDocumentReadResultMessage>())
            {
                Ok(Some(result)) if result.success => {
                    Read::Text(result.content.unwrap_or_default())
                }
                Ok(Some(result)) => Read::Failed(
                    result
                        .error
                        .unwrap_or_else(|| "the file could not be read".to_owned()),
                ),
                Ok(None) => Read::Failed("the daemon did not answer".to_owned()),
                Err(why) => Read::Failed(why),
            };
            let reads = &mut state.window.asks.panels.reads;
            if reads.contains_key(&file) {
                reads.insert(file, read);
            }
        },
        tx,
    );
}

/// Moves the panel of the ask on screen `by` tabs, around its ends; false when it has none.
pub(crate) fn step(state: &mut State, by: isize, tx: &mut Tx) -> bool {
    let Some(ask) = shown_ask(state) else {
        return false;
    };
    let Some(panel) = state.window.asks.panels.by_ask.get_mut(&ask) else {
        return false;
    };
    let count = panel.tabs.len() as isize;
    if count == 0 {
        return false;
    }
    panel.shown = (panel.shown as isize + by).rem_euclid(count) as usize;
    if let Some(Reference::Markdown { host, path }) = panel.shown().cloned() {
        read(state, host, path, tx);
    } else {
        crate::garden::follow(state, tx);
    }
    true
}

/// Closes the tab the panel of the ask on screen shows, showing the one after it; false when
/// it has none.
pub(crate) fn close_tab(state: &mut State, tx: &mut Tx) -> bool {
    let Some(ask) = shown_ask(state) else {
        return false;
    };
    let panels = &mut state.window.asks.panels;
    let Some(panel) = panels.by_ask.get_mut(&ask).filter(|p| !p.tabs.is_empty()) else {
        return false;
    };
    panel.tabs.remove(panel.shown);
    panel.shown = panel.shown.min(panel.tabs.len().saturating_sub(1));
    panels.forget_unread();
    crate::garden::follow(state, tx);
    true
}

pub(crate) const PANEL: &str = "asks.panel";

fn tab_key(at: usize) -> String {
    format!("asks.panel.tab.{at}")
}

/// The panel of `ask`, or none while it has no tab open.
pub(crate) fn view(state: &State, ask: &str, t: &Tokens) -> Option<El> {
    let panel = state.window.asks.panels.of(ask)?;
    let shown = panel.shown()?;
    let machines = crate::machine::Machines::of(&state.facts);
    let tabs = Inline::tight()
        .key("asks.panel.tabs")
        .role("navigation")
        .label("Open references")
        .min_w(0.0)
        .shrink()
        .children(panel.tabs.iter().enumerate().map(|(at, tab)| {
            let said = tab_name(state, &machines, tab);
            let on = at == panel.shown;
            El::from(
                Button::new(said)
                    .size(Size::Compact)
                    .look(if on { Look::Plain } else { Look::Quiet })
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| show_tab(state, at, tx))
                    .build(t)
                    .key(tab_key(at)),
            )
            .a11y_value(if on { "pressed" } else { "" })
        }));
    let close = icon_button(
        "asks.panel.close-tab",
        "Close this tab",
        glyph::TIMES,
        IconSize::Large,
    )
    .on_press_tx(|state: &mut State, tx: &mut Tx| {
        close_tab(state, tx);
    })
    .build(t);
    let sheet = Sheet::new(PANEL, "References").head(tabs).close(close);
    let sheet = match shown {
        Reference::Seed(id) => sheet.fill(crate::garden::panel_body(
            id,
            &crate::garden::TileReads::of(state),
            t,
        )),
        Reference::Markdown { host, path } => {
            match state
                .window
                .asks
                .panels
                .reads
                .get(&(host.clone(), path.clone()))
            {
                Some(Read::Text(text)) => {
                    sheet.fill(markdown(state, &machines, host.as_deref(), path, text, t))
                }
                Some(Read::Failed(why)) => sheet.push(
                    Prose::empty("asks.panel.note", format!("Could not read {path}: {why}"))
                        .build(t),
                ),
                None | Some(Read::Reading) => {
                    sheet.push(Prose::empty("asks.panel.note", format!("Reading {path}…")).build(t))
                }
            }
        }
    };
    Some(
        sheet
            .build(t)
            .role("region")
            .label("References")
            .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle),
    )
}

fn show_tab(state: &mut State, at: usize, tx: &mut Tx) {
    let Some(ask) = shown_ask(state) else {
        return;
    };
    let Some(shown) = state.window.asks.panels.of(&ask).map(|p| p.shown) else {
        return;
    };
    step(state, at as isize - shown as isize, tx);
}

fn tab_name(state: &State, machines: &crate::machine::Machines, tab: &Reference) -> String {
    match tab {
        Reference::Seed(id) => crate::garden::seeds(state)
            .iter()
            .find(|s| s.id == *id)
            .map(|s| s.title.clone())
            .or_else(|| {
                crate::garden::reading(&state.window.garden, id)
                    .0
                    .map(|d| d.seed.title.clone())
            })
            .filter(|title| !title.is_empty())
            .unwrap_or_else(|| id.clone()),
        Reference::Markdown { host, path } => {
            let name = path.rsplit('/').next().unwrap_or(path).to_owned();
            match machines.away(host.as_deref()) {
                Some(machine) => format!("{name} · {machine}"),
                None => name,
            }
        }
    }
}

const VIEW: &str = "asks.panel.document";

fn markdown(
    state: &State,
    machines: &crate::machine::Machines,
    host: Option<&str>,
    path: &str,
    text: &str,
    t: &Tokens,
) -> El {
    let kept = state.readings.kept(VIEW);
    let document = links::Document {
        path: path.to_owned(),
        session: None,
        away: links::Away::of(machines, host),
        view: VIEW.into(),
        opens_seed: Some(open_seed),
        seed: None,
        part: None,
        ids: kept.ids.clone(),
        placed: Vec::new(),
    };
    let (padding, text_width) = crate::markdown::card_fit(kept.width, f32::INFINITY);
    let look = crate::markdown::look(t).prose_measure(crate::markdown::PROSE_MEASURE);
    let style = match text_width {
        Some(width) => look.text_width(width),
        None => look,
    };
    let pictures = state.pictures.of(host);
    let version = (
        text.to_owned(),
        document.path.clone(),
        document.away.clone(),
        kept.clone(),
        state.pictures.revision,
        *t,
    );
    let reading = memo("asks.panel.reading", version, || {
        crate::markdown::reader(text, document, pictures, kept, None, style, t)
    });
    col()
        .key("asks.panel.gutter")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            state.readings.measured(VIEW, w, tx)
        })
        .child(
            col()
                .key(VIEW)
                .role("document")
                .label(path.to_owned())
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .scroll(ScrollSpec {
                    stick_bottom: false,
                })
                .items_center()
                .child(crate::markdown::card(padding, reading, t))
                .scroll_bar_styled(attn_ui::document_scroll_bar(t)),
        )
}
