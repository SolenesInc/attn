//! The panel beside the question in the full-screen Asks: the seeds, markdown files and, on
//! macOS, web pages an ask names, read without leaving the ask, and the terminals its commands
//! open, each in a tab.

use std::collections::{HashMap, HashSet};

use attn_client::Facts;
use attn_protocol::{
    DesktopShowSessionMessage, SessionPlacement, SpawnSessionMessage, TerminalBinding,
    TileDocumentReadMessage, TileDocumentReadResultMessage, UnregisterMessage,
};
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
    /// An http or https page, shown in the panel's own web page.
    Web(String),
    /// A shell the ask holds, by its session.
    Terminal(String),
}

/// One ask's panel: its tabs in the order they opened, and the one it shows.
#[derive(Default)]
pub(crate) struct Panel {
    pub(crate) tabs: Vec<Reference>,
    pub(crate) shown: usize,
    /// The terminal it shows holds the keyboard, until the Asks chord hands it back.
    pub(crate) keys: bool,
    /// The shell each of the ask's commands opened, by the command's place among them.
    commands: HashMap<usize, String>,
}

/// A shell spawned into an ask that no binding shows yet.
enum Spawn {
    Starting,
    Refused(String),
}

/// A markdown file's text as its machine last answered.
pub(crate) enum Read {
    Reading,
    Text { text: String, revision: u64 },
    Failed(String),
}

/// Every ask's panel, and the markdown their tabs read.
#[derive(Default)]
pub(crate) struct Panels {
    by_ask: HashMap<String, Panel>,
    reads: HashMap<(Option<String>, String), Read>,
    answers: u64,
    spawns: HashMap<String, Spawn>,
    /// The shells closed or moved out of their ask's panel, until no ask holds them.
    let_go: HashSet<String>,
    drawing: Option<String>,
}

impl Panels {
    pub(crate) fn of(&self, ask: &str) -> Option<&Panel> {
        self.by_ask.get(ask).filter(|p| !p.tabs.is_empty())
    }

    /// Forgets the panels of asks no longer listed, and what no tab reads any more, and keeps a
    /// tab for each shell a listed ask holds, so a restarted app shows them again.
    pub(crate) fn keep(&mut self, listed: &[String], held: &[TerminalBinding]) {
        self.by_ask.retain(|ask, _| listed.contains(ask));
        self.follow_terminals(listed, held);
        self.forget_unread();
    }

    fn follow_terminals(&mut self, listed: &[String], held: &[TerminalBinding]) {
        let Panels {
            by_ask,
            spawns,
            let_go,
            ..
        } = self;
        let_go.retain(|session| held.iter().any(|b| b.session_id == *session));
        for binding in held {
            let Some(ask) = binding.ask_id.as_ref().filter(|ask| listed.contains(ask)) else {
                continue;
            };
            spawns.remove(&binding.session_id);
            if let_go.contains(&binding.session_id) {
                continue;
            }
            let panel = by_ask.entry(ask.clone()).or_default();
            let tab = Reference::Terminal(binding.session_id.clone());
            if !panel.tabs.contains(&tab) {
                panel.tabs.push(tab);
            }
        }
        for (ask, panel) in by_ask.iter_mut() {
            let holds = |session: &String| {
                held.iter()
                    .any(|b| b.session_id == *session && b.ask_id.as_ref() == Some(ask))
            };
            let shown = panel.shown().cloned();
            panel.tabs.retain(|tab| match tab {
                Reference::Terminal(session) => {
                    !let_go.contains(session) && (holds(session) || spawns.contains_key(session))
                }
                Reference::Seed(_) | Reference::Markdown { .. } | Reference::Web(_) => true,
            });
            panel.shown = shown
                .and_then(|shown| panel.tabs.iter().position(|tab| *tab == shown))
                .unwrap_or_else(|| panel.shown.min(panel.tabs.len().saturating_sub(1)));
            let tabs = &panel.tabs;
            (panel.commands)
                .retain(|_, session| tabs.contains(&Reference::Terminal(session.clone())));
        }
    }

    fn forget_unread(&mut self) {
        let read: Vec<(Option<String>, String)> = (self.by_ask.values())
            .flat_map(|p| p.tabs.iter())
            .filter_map(|tab| match tab {
                Reference::Markdown { host, path } => Some((host.clone(), path.clone())),
                Reference::Seed(_) | Reference::Web(_) | Reference::Terminal(_) => None,
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
        Reference::Markdown { .. } | Reference::Web(_) | Reference::Terminal(_) => None,
    }
}

/// Whether a web page opens in the panel: while Asks is open, where the app runs pages.
pub(crate) fn shows_web(state: &State) -> bool {
    shown_ask(state).is_some() && crate::browser::hosts_pages()
}

/// Whether the panel of the ask on screen has a web tab, which keeps the panel's page.
pub(crate) fn has_web(state: &State) -> bool {
    let Some(ask) = shown_ask(state) else {
        return false;
    };
    (state.window.asks.panels.of(&ask)).is_some_and(|panel| {
        panel
            .tabs
            .iter()
            .any(|tab| matches!(tab, Reference::Web(_)))
    })
}

/// The address of the web tab the panel of the ask on screen shows.
pub(crate) fn shown_web(state: &State) -> Option<String> {
    let panels = &state.window.asks.panels;
    match panels.of(&shown_ask(state)?)?.shown()? {
        Reference::Web(url) => Some(url.clone()),
        Reference::Seed(_) | Reference::Markdown { .. } | Reference::Terminal(_) => None,
    }
}

/// The markdown the panel of the ask on screen shows: its machine, path and text.
pub(crate) fn shown_markdown(state: &State) -> Option<(Option<String>, String, String)> {
    let panels = &state.window.asks.panels;
    let Reference::Markdown { host, path } = panels.of(&shown_ask(state)?)?.shown()? else {
        return None;
    };
    match panels.reads.get(&(host.clone(), path.clone()))? {
        Read::Text { text, .. } => Some((host.clone(), path.clone(), text.clone())),
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
        Reference::Web(_) | Reference::Terminal(_) => {}
    }
}

/// The terminal that runs shell `session`, by the binding its ask holds it with.
fn runtime_of<'f>(facts: &'f Facts, session: &str) -> Option<&'f str> {
    (facts.ask_terminals.iter())
        .find(|b| b.session_id == session)
        .map(|b| b.terminal_id.as_str())
}

/// Opens command `n` of ask `ask_id` in a terminal the ask holds, on the machine and in the
/// folder the command names, with its text typed and not run, and gives that terminal the
/// keyboard. A command it already opened shows its terminal again.
pub(crate) fn open_command(state: &mut State, ask_id: &str, n: usize, tx: &mut Tx) {
    let Some(ask) = state.facts.asks.iter().find(|a| a.id == ask_id) else {
        return;
    };
    let Some(command) = super::commands::of(ask).into_iter().nth(n) else {
        return;
    };
    let profile_id = state.facts.profiles.selected.clone();
    let panels = &mut state.window.asks.panels;
    let panel = panels.by_ask.entry(ask_id.to_owned()).or_default();
    let earlier = panel.commands.get(&n).cloned();
    let opened = (earlier.clone())
        .map(Reference::Terminal)
        .and_then(|tab| panel.tabs.iter().position(|t| *t == tab));
    panel.keys = true;
    tx.focus(TERMINAL, false);
    let refused = (earlier.as_ref())
        .and_then(|session| panels.spawns.get(session))
        .is_some_and(|spawn| matches!(spawn, Spawn::Refused(_)));
    match opened {
        Some(at) if !refused => {
            panel.shown = at;
            return;
        }
        Some(at) => {
            panel.tabs.remove(at);
            earlier.map(|session| panels.spawns.remove(&session));
        }
        None => {}
    }
    let session = uuid::Uuid::new_v4().to_string();
    panel.commands.insert(n, session.clone());
    panel.tabs.push(Reference::Terminal(session.clone()));
    panel.shown = panel.tabs.len() - 1;
    panels.spawns.insert(session.clone(), Spawn::Starting);
    let spawn = SpawnSessionMessage {
        id: session.clone(),
        agent: "shell".into(),
        host: command.host,
        cwd: command.cwd,
        typed: Some(command.text),
        placement: Some(SessionPlacement {
            ask_id: Some(ask_id.to_owned()),
            ..SessionPlacement::default()
        }),
        cols: 80,
        rows: 24,
        profile_id,
        ..SpawnSessionMessage::new()
    };
    state.ask_as(
        session.clone(),
        &spawn,
        move |state: &mut State, answer, _: &mut Tx| {
            if let Err(why) = answer.outcome() {
                (state.window.asks.panels.spawns).insert(session, Spawn::Refused(why));
            }
        },
        tx,
    );
}

/// The terminal the panel of the ask on screen shows while it holds the keyboard.
pub(crate) fn keys_terminal(state: &State) -> Option<String> {
    let panel = state.window.asks.panels.of(&shown_ask(state)?)?;
    let Reference::Terminal(session) = panel.shown().filter(|_| panel.keys)? else {
        return None;
    };
    runtime_of(&state.facts, session).map(str::to_owned)
}

/// Gives the keyboard to the terminal the panel shows, as a press in it does.
fn take_keys(state: &mut State, tx: &mut Tx) {
    let Some(ask) = shown_ask(state) else {
        return;
    };
    if let Some(panel) = state.window.asks.panels.by_ask.get_mut(&ask) {
        panel.keys = true;
        tx.focus(TERMINAL, false);
    }
}

/// The person moved the keyboard into the question, so the panel's terminal no longer holds it.
pub(crate) fn let_keys_go(state: &mut State) {
    let Some(ask) = shown_ask(state) else {
        return;
    };
    if let Some(panel) = state.window.asks.panels.by_ask.get_mut(&ask) {
        panel.keys = false;
    }
}

/// The panel's terminal gives the keyboard back to the question; false when it did not hold it.
pub(crate) fn hand_back(state: &mut State, tx: &mut Tx) -> bool {
    let Some(ask) = shown_ask(state) else {
        return false;
    };
    let Some(panel) = state.window.asks.panels.by_ask.get_mut(&ask) else {
        return false;
    };
    if !std::mem::take(&mut panel.keys) {
        return false;
    }
    state.window.asks.take_keyboard = true;
    super::follow(state, tx);
    true
}

/// Attaches the shells of the ask Asks now shows and lets go of the ones it showed before,
/// once the shown ask changes or Asks opens or closes.
pub(crate) fn follow_drawn(state: &mut State, tx: &mut Tx) {
    let shown = shown_ask(state);
    if state.window.asks.panels.drawing != shown {
        state.window.asks.panels.drawing = shown;
        crate::follow_terminals(state, tx);
    }
}

/// The terminals the ask on screen holds, which the window keeps attached while Asks shows it.
pub(crate) fn drawn_terminals<'f>(
    state: &State,
    facts: &'f Facts,
) -> Vec<crate::terminal::Drawn<'f>> {
    let Some(shown) = shown_ask(state) else {
        return Vec::new();
    };
    (facts.ask_terminals.iter())
        .filter(|b| b.ask_id.as_deref() == Some(shown.as_str()))
        .map(|b| crate::terminal::Drawn {
            runtime: &b.terminal_id,
            session: &b.session_id,
            kind: "shell",
        })
        .collect()
}

/// Whether a command still runs in a terminal `ask` holds, which sending the answer stops.
pub(crate) fn runs_command(state: &State, ask: &str) -> bool {
    (state.facts.ask_terminals.iter())
        .filter(|b| b.ask_id.as_deref() == Some(ask))
        .any(|b| state.terminals.runs_command(&b.terminal_id))
}

/// Lets go of the shell the panel of the ask on screen shows, so its tab goes at once: the
/// ask's binding to it ends once the daemon closes it or a tile takes it.
fn let_go_shown(state: &mut State) -> Option<String> {
    let ask = shown_ask(state)?;
    let panels = &mut state.window.asks.panels;
    let panel = panels.by_ask.get_mut(&ask)?;
    let Some(Reference::Terminal(session)) = panel.shown().cloned() else {
        return None;
    };
    panel.tabs.remove(panel.shown);
    panel.shown = panel.shown.min(panel.tabs.len().saturating_sub(1));
    panel.keys = false;
    panels.spawns.remove(&session);
    panels.let_go.insert(session.clone());
    state.window.asks.take_keyboard = true;
    Some(session)
}

/// Moves the shell the panel shows to a tile beside the asking agent's, or on the current
/// desktop, where it outlives the ask.
pub(crate) fn move_to_desktop(state: &mut State, tx: &mut Tx) {
    let Some(session) = let_go_shown(state) else {
        return;
    };
    super::follow(state, tx);
    let session_id = session.clone();
    state.ask(
        |request_id| DesktopShowSessionMessage {
            request_id,
            session_id,
            ..DesktopShowSessionMessage::new()
        },
        move |state: &mut State, answer, tx: &mut Tx| {
            if let Err(why) = answer.outcome() {
                state.window.asks.panels.let_go.remove(&session);
                state
                    .window
                    .notify(format!("Could not move that terminal: {why}"), tx);
            }
        },
        tx,
    );
}

/// Opens seed `id` in the panel, as a link to it in the panel's own pages does.
pub(crate) fn open_seed(state: &mut State, id: &str, tx: &mut Tx) {
    open(state, Reference::Seed(id.to_owned()), tx);
}

fn read(state: &mut State, host: Option<String>, path: String, tx: &mut Tx) {
    let file = (host.clone(), path.clone());
    let reads = &mut state.window.asks.panels.reads;
    if !matches!(reads.get(&file), Some(Read::Text { .. })) {
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
            let panels = &mut state.window.asks.panels;
            panels.answers += 1;
            let revision = panels.answers;
            let read = match answer
                .outcome()
                .map(|reply| reply.parse::<TileDocumentReadResultMessage>())
            {
                Ok(Some(result)) if result.success => Read::Text {
                    text: result.content.unwrap_or_default(),
                    revision,
                },
                Ok(Some(result)) => Read::Failed(
                    result
                        .error
                        .unwrap_or_else(|| "the file could not be read".to_owned()),
                ),
                Ok(None) => Read::Failed("the daemon did not answer".to_owned()),
                Err(why) => Read::Failed(why),
            };
            if panels.reads.contains_key(&file) {
                panels.reads.insert(file, read);
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
    match panel.shown().cloned() {
        Some(Reference::Markdown { host, path }) => read(state, host, path, tx),
        Some(Reference::Terminal(_)) => {}
        _ => crate::garden::follow(state, tx),
    }
    true
}

/// Closes the tab the panel of the ask on screen shows, showing the one after it, and ends the
/// shell a terminal tab shows; false when it has none.
pub(crate) fn close_tab(state: &mut State, tx: &mut Tx) -> bool {
    if let Some(session) = let_go_shown(state) {
        super::follow(state, tx);
        if state
            .daemon
            .send(&UnregisterMessage {
                id: session,
                ..UnregisterMessage::new()
            })
            .is_err()
        {
            state.window.notify(
                "Could not end that terminal: not connected to the daemon".into(),
                tx,
            );
        }
        return true;
    }
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
/// Where the panel's web page shows.
pub(crate) const PAGE: &str = "asks.panel.page";

fn tab_key(at: usize) -> String {
    format!("asks.panel.tab.{at}")
}

/// The panel of `ask`, or none while it has no tab open.
pub(crate) fn view(state: &State, ask: &str, scale: f32, t: &Tokens) -> Option<El> {
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
        Reference::Terminal(session) => match runtime_of(&state.facts, session) {
            Some(runtime) => sheet.fill(terminal(state, (runtime, session), panel.keys, scale, t)),
            None => {
                let said = match state.window.asks.panels.spawns.get(session) {
                    Some(Spawn::Refused(why)) => format!("Could not open the terminal: {why}"),
                    Some(Spawn::Starting) | None => "Starting a shell…".to_owned(),
                };
                sheet.fill(
                    col()
                        .w_full()
                        .grow(1.0)
                        .min_h(0.0)
                        .child(holding_keys(Prose::empty("asks.panel.note", said).el(t))),
                )
            }
        },
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
                Some(Read::Text { text, revision }) => sheet.fill(markdown(
                    state,
                    &machines,
                    host.as_deref(),
                    path,
                    (text, *revision),
                    t,
                )),
                Some(Read::Failed(why)) => sheet.push(
                    Prose::empty("asks.panel.note", format!("Could not read {path}: {why}"))
                        .build(t),
                ),
                None | Some(Read::Reading) => {
                    sheet.push(Prose::empty("asks.panel.note", format!("Reading {path}…")).build(t))
                }
            }
        }
        Reference::Web(_) => sheet.fill(
            col()
                .w_full()
                .min_h(0.0)
                .child(crate::browser::page_slot(PAGE.to_owned())),
        ),
    };
    Some(
        sheet
            .build(t)
            .role("region")
            .label("References")
            .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle),
    )
}

/// The shell `session` runs in `runtime`, drawn in the panel, its only view, with the way out to
/// a desktop under it.
fn terminal(
    state: &State,
    (runtime, session): (&str, &str),
    keys: bool,
    scale: f32,
    t: &Tokens,
) -> El {
    let place = (state.facts.sessions.iter())
        .find(|s| s.id == session)
        .map(|s| {
            let machines = crate::machine::Machines::of(&state.facts);
            format!(
                "{} · {}",
                machines.name(s.host.as_deref().filter(|h| !h.is_empty())),
                s.directory
            )
        })
        .unwrap_or_default();
    let bar = Inline::loose()
        .key("asks.panel.terminal.bar")
        .w_full()
        .child(
            attn_ui::system::words(place, attn_ui::system::Type::Mono, t.text_tertiary)
                .key("asks.panel.terminal.place")
                .min_w(0.0)
                .shrink()
                .truncate(),
        )
        .child(El::from(
            Button::new("Move to desktop")
                .size(Size::Compact)
                .look(Look::Quiet)
                .on_press_tx(move_to_desktop)
                .build(t)
                .key(MOVE),
        ));
    col()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(
            holding_keys(crate::desktop::ask_terminal(
                state,
                (runtime, session),
                keys,
                scale,
                t,
            ))
            .w_full()
            .grow(1.0)
            .min_h(0.0),
        )
        .child(bar)
}

/// The element that holds the keyboard for the panel's terminal. The Asks sheet keeps focus
/// inside itself, so the shell takes the keyboard by being focused, from its first frame on.
fn holding_keys(shown: El) -> El {
    col()
        .key(TERMINAL)
        .role("group")
        .label("Terminal")
        .focusable(true)
        .no_focus_ring()
        .on_key(|state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            crate::keys::on_key(state, ev, tx) || !matches!(ev.key, rune::Key::Modifier { .. })
        })
        .on_press_within(take_keys)
        .child(shown)
}

pub(crate) const TERMINAL: &str = "asks.panel.terminal";

pub(crate) const MOVE: &str = "asks.panel.move";

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
        Reference::Terminal(session) => (state.facts.sessions.iter())
            .find(|s| s.id == *session)
            .map(|s| {
                let folder = s.directory.trim_end_matches('/');
                let folder = folder
                    .rsplit('/')
                    .next()
                    .filter(|f| !f.is_empty())
                    .unwrap_or("/");
                match machines.away(s.host.as_deref().filter(|h| !h.is_empty())) {
                    Some(machine) => format!("{folder} · {machine}"),
                    None => folder.to_owned(),
                }
            })
            .unwrap_or_else(|| "Terminal".to_owned()),
        Reference::Markdown { host, path } => {
            let name = path.rsplit('/').next().unwrap_or(path).to_owned();
            match machines.away(host.as_deref()) {
                Some(machine) => format!("{name} · {machine}"),
                None => name,
            }
        }
        Reference::Web(url) => {
            let rest = url.split_once("://").map_or(url.as_str(), |(_, rest)| rest);
            rest.split(['/', '?', '#'])
                .next()
                .unwrap_or(rest)
                .to_owned()
        }
    }
}

const VIEW: &str = "asks.panel.document";

fn markdown(
    state: &State,
    machines: &crate::machine::Machines,
    host: Option<&str>,
    path: &str,
    (text, revision): (&str, u64),
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
    let version = (revision, kept.clone(), state.pictures.revision, *t);
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
