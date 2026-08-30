mod grid;
mod keys;
mod layout;
mod reader;
mod theme;

use std::borrow::Cow;
use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use anyhow::{Result, bail};
use futures::StreamExt;
use futures::channel::mpsc;
use gpui::{
    Animation, AnimationExt, AnyElement, AnyView, App, Application, Bounds, BoxShadow, Context,
    Div, ElementId, FocusHandle, FontWeight, Hsla, KeyDownEvent, Keystroke, ModifiersChangedEvent,
    MouseButton,
    MouseDownEvent, Pixels, Render, ScrollDelta, ScrollHandle, ScrollWheelEvent, SharedString,
    Stateful, Task, TitlebarOptions, Window, WindowBackgroundAppearance, WindowBounds,
    WindowOptions, canvas, div, ease_in_out, linear_color_stop, linear_gradient, point,
    prelude::*, px, relative, size,
};
use libghostty_vt::terminal::ScrollViewport;
use zero::editor::{Editor, EditorEvent, Engine, Open};
use zero::markdown::{self, Block};
use zero::model::{Agent, AgentId, AgentKind, Model};
use zero::palette::{self, Expect, PaletteCommand, Verb};
use zero::shell::{Shell, ShellOutput};
use zero::simulator::Simulator;
use zero::source::{Command, Event as SourceEvent, Scenario, ScenarioAction, Source};
use zero::switch_log::{SwitchLog, SwitchPath};
use zero::switcher::rows;

use crate::grid::{Frame, Grid, Metrics, Prepared};
use crate::layout::{Direction, neighbor, tiles};
use crate::reader::{Diagram, Reader};

const SHELL_ID: AgentId = u64::MAX;
const BAR_HEIGHT: f32 = 46.;
const GAP: f32 = 12.;
const USAGE: &str = "usage: zero-gpui [--scenario calm|busy-morning|all-busy] [--agents N] [--shell DESKTOP] [--doc [DESKTOP:]file.md]... [--edit [DESKTOP:]file.md]...
  --agents N             how many simulated agents to seed, 0 to 12; they spread over desktops 1-4
  --shell DESKTOP        where the real shell lives (default 1)
  --doc [DESKTOP:]file   a markdown file in a reading tile (default desktop 1)
  --edit [DESKTOP:]file  the same, opened in the editor";
/// Document tiles count down from here; the shell holds u64::MAX.
const DOC_ID_BASE: AgentId = u64::MAX - 1;

fn main() {
    let launch = match parse_args() {
        Ok(parsed) => parsed,
        Err(error) => {
            eprintln!("{error}\n{USAGE}");
            std::process::exit(2);
        }
    };
    Application::new().run(move |cx: &mut App| {
        cx.text_system()
            .add_fonts(vec![
                Cow::Borrowed(&include_bytes!("../fonts/JetBrainsMono-Regular.ttf")[..]),
                Cow::Borrowed(&include_bytes!("../fonts/JetBrainsMono-Bold.ttf")[..]),
                Cow::Borrowed(&include_bytes!("../fonts/JetBrainsMono-Italic.ttf")[..]),
                Cow::Borrowed(&include_bytes!("../fonts/JetBrainsMono-BoldItalic.ttf")[..]),
            ])
            .expect("embedded JetBrains Mono loads");
        let bounds = Bounds::centered(None, size(px(1480.), px(940.)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                titlebar: Some(TitlebarOptions {
                    title: Some("attn zero".into()),
                    appears_transparent: true,
                    traffic_light_position: Some(point(px(14.), px(15.))),
                }),
                window_background: WindowBackgroundAppearance::Opaque,
                ..Default::default()
            },
            move |window, cx| cx.new(|cx| Zero::new(launch, window, cx).expect("zero starts")),
        )
        .expect("the window opens");
        cx.on_window_closed(|cx| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();
        cx.activate(true);
    });
}

#[derive(Debug)]
struct Launch {
    scenario: Scenario,
    agents: usize,
    shell: u8,
    docs: Vec<DocArg>,
}

#[derive(Debug)]
struct DocArg {
    desktop: u8,
    path: PathBuf,
    edit: bool,
}

fn parse_args() -> Result<Launch> {
    parse_launch(std::env::args().skip(1))
}

fn parse_launch(args: impl IntoIterator<Item = String>) -> Result<Launch> {
    let mut launch = Launch {
        scenario: Scenario::Calm,
        agents: zero::simulator::MAX_AGENTS,
        shell: 1,
        docs: Vec::new(),
    };
    let mut args = args.into_iter();
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--scenario" => {
                let name = args.next().unwrap_or_default();
                launch.scenario = Scenario::parse(&name)
                    .ok_or_else(|| anyhow::anyhow!("unknown scenario {name:?}"))?;
            }
            "--agents" => {
                let value = args.next().unwrap_or_default();
                let count: usize = value
                    .parse()
                    .map_err(|_| anyhow::anyhow!("--agents wants a number, got {value:?}"))?;
                if count > zero::simulator::MAX_AGENTS {
                    bail!("--agents {count}: the crowd has {} names", zero::simulator::MAX_AGENTS);
                }
                launch.agents = count;
            }
            "--shell" => launch.shell = parse_desktop(&args.next().unwrap_or_default())?,
            "--doc" | "--edit" => {
                let (desktop, path) = split_desktop(&args.next().unwrap_or_default())?;
                launch.docs.push(DocArg { desktop, path, edit: arg == "--edit" });
            }
            other => bail!("unknown argument {other:?}"),
        }
    }
    Ok(launch)
}

fn parse_desktop(value: &str) -> Result<u8> {
    match value.parse::<u8>() {
        Ok(desktop @ 1..=9) => Ok(desktop),
        _ => bail!("a desktop is a digit 1-9, got {value:?}"),
    }
}

/// `3:notes.md` puts the file on desktop 3; a bare path lands on desktop 1.
fn split_desktop(value: &str) -> Result<(u8, PathBuf)> {
    let (desktop, path) = match value.split_once(':') {
        Some((digit, rest)) if digit.len() == 1 && digit.as_bytes()[0].is_ascii_digit() => {
            (parse_desktop(digit)?, rest)
        }
        _ => (1, value),
    };
    let path = PathBuf::from(path);
    Ok((desktop, std::fs::canonicalize(&path).unwrap_or(path)))
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum Popup {
    None,
    Palette { query: String, selected: usize },
    Scenario { selected: usize },
    Help,
}

/// The queue pill's hover unfold: t inches toward target a frame at a time, so reversals stay smooth.
#[derive(Default)]
struct Morph {
    t: f32,
    target: f32,
    last: Option<Instant>,
    on_pill: bool,
    on_panel: bool,
}

impl Morph {
    const UNFOLD_MS: f32 = 180.;

    fn retarget(&mut self) {
        self.target = if self.on_pill || self.on_panel { 1. } else { 0. };
    }

    /// One animation step; true while another frame should follow.
    fn step(&mut self) -> bool {
        if self.t == self.target {
            self.last = None;
            return false;
        }
        let now = Instant::now();
        let dt = self.last.map(|last| now.duration_since(last).as_secs_f32()).unwrap_or(0.);
        self.last = Some(now);
        let step = dt * 1000. / Self::UNFOLD_MS;
        self.t = if self.target > self.t {
            (self.t + step).min(self.target)
        } else {
            (self.t - step).max(self.target)
        };
        true
    }
}

enum ItemAction {
    Source(fn(&Zero) -> ScenarioAction),
    Motion,
}

struct ScenarioItem {
    label: &'static str,
    detail: &'static str,
    action: ItemAction,
}

const MOTION_STEPS: [u64; 4] = [400, 600, 800, 1100];

const SCENARIO_ITEMS: [ScenarioItem; 8] = [
    ScenarioItem { label: "calm", detail: "12 agents, one or two waiting at a time", action: ItemAction::Source(|_| ScenarioAction::Set(Scenario::Calm)) },
    ScenarioItem { label: "busy morning", detail: "five or more waiting, a turn every ~20s", action: ItemAction::Source(|_| ScenarioAction::Set(Scenario::BusyMorning)) },
    ScenarioItem { label: "all busy", detail: "everyone working, nobody waiting", action: ItemAction::Source(|_| ScenarioAction::Set(Scenario::AllBusy)) },
    ScenarioItem { label: "toggle speed", detail: "x1 or x5", action: ItemAction::Source(|_| ScenarioAction::ToggleSpeed) },
    ScenarioItem { label: "add an agent", detail: "on this desktop", action: ItemAction::Source(|zero| ScenarioAction::AddAgent { desktop: zero.model.current_desktop, name: None }) },
    ScenarioItem { label: "finish one now", detail: "a working agent flips to waiting", action: ItemAction::Source(|_| ScenarioAction::FinishOneNow) },
    ScenarioItem { label: "make three wait now", detail: "three turns open at once", action: ItemAction::Source(|_| ScenarioAction::MakeThreeWaitNow) },
    ScenarioItem { label: "motion", detail: "cycle the desktop slide duration", action: ItemAction::Motion },
];

struct Binding {
    keys: &'static str,
    terse: &'static str,
    help: &'static str,
}

struct Group {
    title: &'static str,
    /// Whether the hold-⌘ overlay shows the group; the help popup shows everything.
    overlay: bool,
    bindings: &'static [Binding],
}

const fn bind(keys: &'static str, terse: &'static str, help: &'static str) -> Binding {
    Binding { keys, terse, help }
}

const GROUPS: [Group; 5] = [
    Group { title: "queue", overlay: true, bindings: &[
        bind("⌘⇧E", "settle & next", "settle the focused turn and go to the next one in the queue"),
        bind("⌘J", "next in queue", "peek at the next agent in the queue without settling"),
        bind("⌘P", "previous", "back to the previous agent"),
        bind("⌘K", "palette", "agents and commands: type to jump, or new · term · doc · close"),
    ]},
    Group { title: "desktops", overlay: true, bindings: &[
        bind("⌘1…9", "go", "go to that desktop"),
        bind("⌘⌥1…9", "send pane", "move the focused pane to that desktop"),
    ]},
    Group { title: "tiles", overlay: true, bindings: &[
        bind("⌘←↑↓→", "focus", "focus the neighboring pane"),
        bind("⌘⌥←↑↓→", "swap", "swap the focused pane with its neighbor"),
        bind("⌘⏎", "promote", "promote the focused pane to the main slot"),
        bind("⌘⌥- ⌘⌥=", "main column", "shrink or grow the main column"),
        bind("⌘W", "close tile", "close the focused shell or document tile"),
    ]},
    Group { title: "zero", overlay: true, bindings: &[
        bind("⌘S", "scenario", "the scenario menu"),
        bind("⌘/", "keys", "this list; holding ⌘ shows the short version"),
        bind("⌘Q", "quit", "quit"),
    ]},
    Group { title: "mouse & typing", overlay: false, bindings: &[
        bind("hover", "", "the waiting pill unfolds into the agent list; click a row to jump"),
        bind("click", "", "every pill, badge, and pane is clickable; hovering shows its key"),
        bind("wheel", "", "scroll a pane's scrollback"),
        bind("type + ⏎", "", "keys go to the focused terminal; ⏎ prompts a waiting or idle agent"),
        bind("y / n", "", "answer an approval in its terminal"),
        bind("e", "", "edit the focused document in place (neovim); :wq saves and returns to reading"),
        bind("j k ␣ g G", "", "scroll a document by a line, a page, to the top or the bottom"),
    ]},
];

/// How long ⌘ is held before the overlay appears; a feel knob, not a measured limit.
const WHICH_KEY_MS: u64 = 400;

struct Zero {
    model: Model,
    source: Simulator,
    shells: HashMap<AgentId, Shell>,
    next_tile_id: AgentId,
    log: SwitchLog,
    started: Instant,
    focus_handle: FocusHandle,
    metrics: Metrics,
    grids: HashMap<AgentId, Grid>,
    dirty: HashSet<AgentId>,
    docs: HashMap<AgentId, DocTile>,
    popup: Popup,
    cmd_held: bool,
    which_key: bool,
    which_key_gen: u64,
    queue_morph: Morph,
    ratios: HashMap<u8, f32>,
    previous_focus: Option<AgentId>,
    content: Bounds<Pixels>,
    slide: (i8, u64),
    motion_ms: u64,
    ticker: Option<Task<()>>,
    _shell_pumps: Vec<Task<()>>,
}

/// A markdown file in a tile: read through the reader, edited in place through the editor adapter.
struct DocTile {
    path: PathBuf,
    blocks: Vec<Block>,
    error: Option<String>,
    scroll: ScrollHandle,
    face: Face,
    diagrams: HashMap<u64, Diagram>,
}

enum Face {
    Read,
    Edit(EditSession),
}

struct EditSession {
    editor: Box<dyn Editor>,
    frame: Frame,
    mode: String,
    size: (u16, u16),
    _pump: Task<()>,
}

impl DocTile {
    fn open(path: PathBuf) -> Self {
        let mut tile = Self {
            path,
            blocks: Vec::new(),
            error: None,
            scroll: ScrollHandle::new(),
            face: Face::Read,
            diagrams: HashMap::new(),
        };
        tile.reload();
        tile
    }

    fn reload(&mut self) {
        match std::fs::read_to_string(&self.path) {
            Ok(source) => {
                self.blocks = markdown::parse(&source);
                self.error = None;
            }
            Err(error) => {
                self.blocks.clear();
                self.error = Some(format!("{}: {error}", self.path.display()));
            }
        }
    }
}

impl Zero {
    fn new(launch: Launch, window: &mut Window, cx: &mut Context<Self>) -> Result<Self> {
        let Launch { scenario, agents, shell: shell_desktop, docs } = launch;
        let metrics = Metrics::measure(window)?;
        let mut source = Simulator::with_agents(scenario, agents);
        let mut model = Model::new();
        model.apply(source.advance(Duration::ZERO))?;
        let (shell, shell_output) = Shell::spawn(80, 24, None)?;
        model.add_shell(SHELL_ID, "shell".to_string(), shell_desktop, b"")?;
        let launch_docs = docs.len() as u64;
        let mut doc_tiles = HashMap::new();
        let mut edits = Vec::new();
        for (index, doc) in docs.into_iter().enumerate() {
            let id = DOC_ID_BASE - index as u64;
            let name = doc
                .path
                .file_name()
                .map(|name| name.to_string_lossy().into_owned())
                .unwrap_or_else(|| doc.path.display().to_string());
            model.add_document(id, name, doc.desktop)?;
            doc_tiles.insert(id, DocTile::open(doc.path));
            if doc.edit {
                edits.push(id);
            }
        }
        if let Some(first) = model.queue().first().copied() {
            model.current_desktop = model.agent(first).desktop;
            model.focus = Some(first);
        } else {
            model.focus = model.first_on_current_desktop();
        }
        let focus_handle = cx.focus_handle();
        window.focus(&focus_handle);
        let mut zero = Self {
            model,
            source,
            shells: HashMap::new(),
            next_tile_id: DOC_ID_BASE - launch_docs,
            log: SwitchLog::open_default()?,
            started: Instant::now(),
            focus_handle,
            metrics,
            grids: HashMap::new(),
            dirty: HashSet::new(),
            docs: doc_tiles,
            popup: Popup::None,
            cmd_held: false,
            which_key: false,
            which_key_gen: 0,
            queue_morph: Morph::default(),
            ratios: HashMap::new(),
            previous_focus: None,
            content: Bounds::default(),
            slide: (0, 0),
            motion_ms: 600,
            ticker: None,
            _shell_pumps: Vec::new(),
        };
        zero.arm_ticker(cx);
        zero.adopt_shell(SHELL_ID, shell, shell_output, cx);
        for id in edits {
            zero.start_edit(id, cx);
        }
        Ok(zero)
    }

    fn now(&self) -> Duration {
        self.started.elapsed()
    }

    fn arm_ticker(&mut self, cx: &mut Context<Self>) {
        let now = self.now();
        let deadline = match (self.source.next_deadline(), self.model.next_age_deadline(now)) {
            (Some(a), Some(b)) => Some(a.min(b)),
            (a, b) => a.or(b),
        };
        let Some(deadline) = deadline else {
            self.ticker = None;
            return;
        };
        let delay = deadline.saturating_sub(now);
        self.ticker = Some(cx.spawn(async move |this, cx| {
            cx.background_executor().timer(delay).await;
            this.update(cx, |zero, cx| zero.tick(cx)).ok();
        }));
    }

    fn tick(&mut self, cx: &mut Context<Self>) {
        let events = self.source.advance(self.now());
        self.apply(events);
        self.arm_ticker(cx);
        cx.notify();
    }

    fn command(&mut self, command: Command, cx: &mut Context<Self>) {
        let events = self.source.command(self.now(), command);
        self.apply(events);
        self.arm_ticker(cx);
        cx.notify();
    }

    fn apply(&mut self, events: Vec<SourceEvent>) {
        for event in &events {
            match event {
                SourceEvent::AgentAdded { id, .. }
                | SourceEvent::Output { id, .. }
                | SourceEvent::Resized { id, .. } => {
                    self.dirty.insert(*id);
                }
                SourceEvent::StateChanged { .. } | SourceEvent::TurnSettled { .. } => {}
            }
        }
        self.model.apply(events).expect("the model accepts simulator events");
    }

    fn adopt_shell(
        &mut self,
        id: AgentId,
        shell: Shell,
        output: impl IntoIterator<Item = ShellOutput> + Send + 'static,
        cx: &mut Context<Self>,
    ) {
        self.shells.insert(id, shell);
        let (sender, mut receiver) = mpsc::unbounded::<ShellOutput>();
        thread::spawn(move || {
            for item in output {
                if sender.unbounded_send(item).is_err() {
                    break;
                }
            }
        });
        self._shell_pumps.push(cx.spawn(async move |this, cx| {
            while let Some(item) = receiver.next().await {
                let mut batch = vec![item];
                while let Ok(more) = receiver.try_recv() {
                    batch.push(more);
                }
                if this
                    .update(cx, |zero, cx| zero.on_shell_output(id, batch, cx))
                    .is_err()
                {
                    break;
                }
            }
        }));
    }

    fn on_shell_output(&mut self, id: AgentId, batch: Vec<ShellOutput>, cx: &mut Context<Self>) {
        // A closed tile's pump can still deliver a tail; there is nowhere to paint it.
        if !self.model.agents.iter().any(|agent| agent.id == id) {
            return;
        }
        for output in batch {
            match output {
                ShellOutput::Bytes(bytes) => {
                    let agent = self.model.agent_mut(id);
                    agent.terminal.vt_write(&bytes);
                    for response in agent.take_pty_responses() {
                        if let Some(shell) = self.shells.get_mut(&id) {
                            shell.write(&response).ok();
                        }
                    }
                }
                ShellOutput::Closed => {
                    self.model
                        .agent_mut(id)
                        .terminal
                        .vt_write(b"\r\n\x1b[2m[shell exited]\x1b[0m");
                }
            }
        }
        self.dirty.insert(id);
        cx.notify();
    }

    fn resize(&mut self, id: AgentId, cols: u16, rows: u16) {
        if let Some(shell) = self.shells.get(&id) {
            shell.resize(cols, rows).ok();
            self.model.resize_terminal(id, cols, rows).ok();
            self.dirty.insert(id);
        } else {
            let events = self
                .source
                .command(self.now(), Command::Resize { id, cols, rows });
            self.apply(events);
        }
    }

    fn scenario_name(&self) -> &'static str {
        self.source.scenario().name()
    }

    fn focus(&mut self, target: Option<AgentId>, path: SwitchPath, cx: &mut Context<Self>) {
        let before = self.model.focus;
        let before_desktop = self.model.current_desktop;
        let record = self
            .model
            .switch_focus(target, path, self.scenario_name(), timestamp_ms())
            .cloned();
        if let Some(record) = record {
            self.log.append(&record).ok();
        }
        if self.model.focus != before {
            self.previous_focus = before;
        }
        self.note_desktop_change(before_desktop);
        cx.notify();
    }

    fn note_desktop_change(&mut self, before: u8) {
        let after = self.model.current_desktop;
        if after != before {
            self.slide = (if after > before { 1 } else { -1 }, self.slide.1 + 1);
        }
    }

    fn go_desktop(&mut self, desktop: u8, cx: &mut Context<Self>) {
        let before = self.model.current_desktop;
        let record = self
            .model
            .go_desktop(desktop, self.scenario_name(), timestamp_ms())
            .cloned();
        if let Some(record) = record {
            self.log.append(&record).ok();
        }
        self.note_desktop_change(before);
        cx.notify();
    }

    fn next_in_queue(&mut self, cx: &mut Context<Self>) {
        if let Some(next) = self.model.queue().first().copied() {
            self.focus(Some(next), SwitchPath::Queue, cx);
        }
    }

    fn settle(&mut self, id: AgentId, cx: &mut Context<Self>) {
        if self.model.agent(id).turn_opened_at.is_some() {
            self.command(Command::Settle { id }, cx);
        }
    }

    fn settle_and_next(&mut self, cx: &mut Context<Self>) {
        if let Some(id) = self.model.focus {
            self.settle(id, cx);
        }
        if let Some(next) = self.model.queue().first().copied() {
            self.focus(Some(next), SwitchPath::Settle, cx);
        }
    }

    fn back(&mut self, cx: &mut Context<Self>) {
        if let Some(previous) = self.previous_focus
            && self.model.agents.iter().any(|agent| agent.id == previous)
        {
            self.focus(Some(previous), SwitchPath::Back, cx);
        }
    }

    fn swap_neighbor(&mut self, direction: Direction, cx: &mut Context<Self>) {
        let ids = self.model.desktop_agents();
        let Some(current) = self.model.focus.and_then(|focus| ids.iter().position(|&id| id == focus)) else {
            return;
        };
        let rects = tiles(self.content, ids.len(), px(GAP), self.ratio());
        if let Some(other) = neighbor(&rects, current, direction) {
            self.model.swap(ids[current], ids[other]);
            cx.notify();
        }
    }

    fn promote_focused(&mut self, cx: &mut Context<Self>) {
        if let Some(id) = self.model.focus {
            self.model.promote(id);
            cx.notify();
        }
    }

    /// The current desktop's main-column share in the main+stack layout.
    fn ratio(&self) -> f32 {
        self.ratios.get(&self.model.current_desktop).copied().unwrap_or(0.5)
    }

    fn nudge_ratio(&mut self, delta: f32, cx: &mut Context<Self>) {
        let ratio = (self.ratio() + delta).clamp(0.3, 0.7);
        self.ratios.insert(self.model.current_desktop, ratio);
        cx.notify();
    }

    fn focus_neighbor(&mut self, direction: Direction, cx: &mut Context<Self>) {
        let ids = self.model.desktop_agents();
        let Some(current) = self
            .model
            .focus
            .and_then(|focus| ids.iter().position(|id| *id == focus))
        else {
            return;
        };
        let rects = tiles(self.content, ids.len(), px(GAP), self.ratio());
        if let Some(index) = neighbor(&rects, current, direction) {
            self.focus(Some(ids[index]), SwitchPath::PaneMove, cx);
        }
    }

    fn scroll(&mut self, id: AgentId, event: &ScrollWheelEvent, cx: &mut Context<Self>) {
        let lines = match event.delta {
            ScrollDelta::Lines(delta) => delta.y,
            ScrollDelta::Pixels(delta) => f32::from(delta.y) / f32::from(self.metrics.line_height),
        };
        let delta = (-lines).round() as isize;
        if delta != 0 {
            self.model
                .agent_mut(id)
                .terminal
                .scroll_viewport(ScrollViewport::Delta(delta));
            self.dirty.insert(id);
            cx.notify();
        }
    }

    fn run_scenario_item(&mut self, index: usize, cx: &mut Context<Self>) {
        match SCENARIO_ITEMS[index].action {
            ItemAction::Source(action) => {
                let action = action(self);
                self.popup = Popup::None;
                self.command(Command::Scenario(action), cx);
            }
            ItemAction::Motion => {
                let current = MOTION_STEPS.iter().position(|&ms| ms == self.motion_ms);
                self.motion_ms = MOTION_STEPS[current.map_or(0, |i| (i + 1) % MOTION_STEPS.len())];
                cx.notify();
            }
        }
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, _window: &mut Window, cx: &mut Context<Self>) {
        let keystroke = &event.keystroke;
        if self.popup != Popup::None {
            self.popup_key(keystroke, cx);
            return;
        }
        let modifiers = keystroke.modifiers;
        if modifiers.platform {
            if let Some(desktop) = digit(keystroke) {
                if modifiers.alt {
                    self.model.move_focused(desktop);
                    cx.notify();
                } else {
                    self.go_desktop(desktop, cx);
                }
                return;
            }
            match keystroke.key.as_str() {
                "e" if modifiers.shift => self.settle_and_next(cx),
                "j" => self.next_in_queue(cx),
                "p" => self.back(cx),
                "k" => self.open(Popup::Palette { query: String::new(), selected: 0 }, cx),
                "s" => self.open(Popup::Scenario { selected: 0 }, cx),
                "/" => self.open(Popup::Help, cx),
                "q" => cx.quit(),
                "w" => self.close_focused(cx),
                "left" if modifiers.alt => self.swap_neighbor(Direction::Left, cx),
                "right" if modifiers.alt => self.swap_neighbor(Direction::Right, cx),
                "up" if modifiers.alt => self.swap_neighbor(Direction::Up, cx),
                "down" if modifiers.alt => self.swap_neighbor(Direction::Down, cx),
                "left" => self.focus_neighbor(Direction::Left, cx),
                "right" => self.focus_neighbor(Direction::Right, cx),
                "up" => self.focus_neighbor(Direction::Up, cx),
                "down" => self.focus_neighbor(Direction::Down, cx),
                "enter" => self.promote_focused(cx),
                "-" if modifiers.alt => self.nudge_ratio(-0.05, cx),
                "=" if modifiers.alt => self.nudge_ratio(0.05, cx),
                _ => {}
            }
            return;
        }
        let Some(id) = self.model.focus else {
            return;
        };
        if self.model.agent(id).kind == AgentKind::Document {
            self.doc_key(id, keystroke, cx);
            return;
        }
        let Some(bytes) = keys::keystroke_bytes(keystroke) else {
            return;
        };
        match self.model.agent(id).kind {
            AgentKind::Shell => {
                if let Some(shell) = self.shells.get_mut(&id) {
                    shell.write(&bytes).ok();
                }
            }
            AgentKind::Simulated => self.command(Command::Input { id, bytes }, cx),
            AgentKind::Document => {}
        }
    }

    fn open(&mut self, popup: Popup, cx: &mut Context<Self>) {
        self.queue_morph = Morph::default();
        self.which_key = false;
        self.which_key_gen += 1;
        self.popup = popup;
        cx.notify();
    }

    fn popup_key(&mut self, keystroke: &Keystroke, cx: &mut Context<Self>) {
        let key = keystroke.key.as_str();
        if key == "escape" || (keystroke.modifiers.platform && matches!(key, "k" | "s" | "/")) {
            self.popup = Popup::None;
            cx.notify();
            return;
        }
        if let Popup::Palette { query, selected } = &self.popup {
            let (query, selected) = (query.clone(), *selected);
            self.palette_key(keystroke, query, selected, cx);
            return;
        }
        let mut run: Option<usize> = None;
        match &mut self.popup {
            Popup::Scenario { selected } => match key {
                "up" => *selected = selected.saturating_sub(1),
                "down" => *selected = (*selected + 1).min(SCENARIO_ITEMS.len() - 1),
                "enter" => run = Some(*selected),
                _ => {
                    if let Some(index) = key.parse::<usize>().ok().filter(|n| (1..=SCENARIO_ITEMS.len()).contains(n)) {
                        run = Some(index - 1);
                    }
                }
            },
            Popup::Help => self.popup = Popup::None,
            Popup::Palette { .. } | Popup::None => {}
        }
        if let Some(index) = run {
            self.run_scenario_item(index, cx);
        }
        cx.notify();
    }

    fn palette_key(&mut self, keystroke: &Keystroke, query: String, selected: usize, cx: &mut Context<Self>) {
        let view = self.palette_view(&query);
        let list = palette_rows(&view);
        let selected = selected.min(list.len().saturating_sub(1));
        match keystroke.key.as_str() {
            "up" => self.popup = Popup::Palette { query, selected: selected.saturating_sub(1) },
            "down" => self.popup = Popup::Palette { query, selected: (selected + 1).min(list.len().saturating_sub(1)) },
            "backspace" => {
                let mut query = query;
                query.pop();
                self.popup = Popup::Palette { query, selected: 0 };
            }
            "tab" => {
                // On the run row, tab still means "complete": fall through to the first candidate.
                let fill = match list.get(selected) {
                    Some(PaletteRow::Candidate(index)) => Some(*index),
                    Some(PaletteRow::Run) => view.completions.iter().enumerate().map(|(index, _)| index).next(),
                    _ => None,
                };
                if let Some(index) = fill {
                    let candidate = &view.completions[index];
                    self.popup = Popup::Palette { query: palette_fill(&query, view.replace_from, &candidate.insert, candidate.space), selected: 0 };
                } else if let Some(PaletteRow::Verb(verb)) = list.get(selected) {
                    self.popup = Popup::Palette { query: format!("{} ", verb.word()), selected: 0 };
                } else {
                    self.popup = Popup::Palette { query, selected };
                }
            }
            "enter" => match list.get(selected) {
                Some(PaletteRow::Run) => {
                    let command = view.run.clone().expect("a run row carries its command");
                    self.popup = Popup::None;
                    self.run_palette_command(command, cx);
                }
                Some(PaletteRow::Agent(id)) => {
                    let id = *id;
                    self.popup = Popup::None;
                    self.focus(Some(id), SwitchPath::Switcher, cx);
                }
                Some(PaletteRow::Verb(verb)) => {
                    self.popup = Popup::Palette { query: format!("{} ", verb.word()), selected: 0 };
                }
                Some(PaletteRow::Candidate(index)) => {
                    let candidate = &view.completions[*index];
                    self.popup = Popup::Palette { query: palette_fill(&query, view.replace_from, &candidate.insert, candidate.space), selected: 0 };
                }
                None => self.popup = Popup::Palette { query, selected },
            },
            _ => {
                if let Some(text) = keys::typed_text(keystroke) {
                    let mut query = query;
                    query.push_str(&text);
                    self.popup = Popup::Palette { query, selected: 0 };
                } else {
                    self.popup = Popup::Palette { query, selected };
                }
            }
        }
        cx.notify();
    }

    fn palette_view(&self, query: &str) -> PaletteView {
        let analysis = palette::analyze(query);
        match analysis.verb {
            None => {
                let trimmed = query.trim();
                PaletteView {
                    agents: rows(&self.model.agents, query),
                    verbs: Verb::ALL.into_iter().filter(|verb| trimmed.is_empty() || verb.word().starts_with(trimmed)).collect(),
                    run: None,
                    completions: Vec::new(),
                    grammar: None,
                    replace_from: 0,
                }
            }
            Some(verb) => {
                let mut completions = Vec::new();
                for expect in &analysis.expects {
                    completions.extend(candidates(*expect, &analysis.prefix));
                }
                PaletteView {
                    agents: Vec::new(),
                    verbs: Vec::new(),
                    run: analysis.ready,
                    completions,
                    grammar: Some(verb.grammar()),
                    replace_from: analysis.replace_from,
                }
            }
        }
    }

    fn describe(&self, command: &PaletteCommand) -> String {
        let here = self.model.current_desktop;
        match command {
            PaletteCommand::New { kind, cwd, desktop, name } => {
                let name = name.clone()
                    .or_else(|| cwd.as_deref().map(|cwd| format!("{}/{kind}", basename(cwd))))
                    .unwrap_or_else(|| format!("sandbox/{kind}"));
                format!("start {kind} as {name} on desktop {}", desktop.unwrap_or(here))
            }
            PaletteCommand::Term { cwd, desktop } => format!(
                "open a terminal{} on desktop {}",
                cwd.as_deref().map(|cwd| format!(" in {cwd}")).unwrap_or_default(),
                desktop.unwrap_or(here),
            ),
            PaletteCommand::Doc { path, edit, desktop } => format!(
                "open {path}{} on desktop {}",
                if *edit { " in the editor" } else { "" },
                desktop.unwrap_or(here),
            ),
            PaletteCommand::Close => match self.model.focus.map(|id| self.model.agent(id)) {
                Some(agent) if agent.kind != AgentKind::Simulated => format!("close {}", agent.name),
                _ => "only shells and documents close in the prototype".to_string(),
            },
        }
    }

    fn run_palette_command(&mut self, command: PaletteCommand, cx: &mut Context<Self>) {
        match command {
            PaletteCommand::New { kind, cwd, desktop, name } => {
                let desktop = desktop.unwrap_or(self.model.current_desktop);
                let name = name
                    .or_else(|| cwd.as_deref().map(|cwd| format!("{}/{kind}", basename(cwd))))
                    .unwrap_or_else(|| format!("sandbox/{kind}"));
                self.command(Command::Scenario(ScenarioAction::AddAgent { desktop, name: Some(name) }), cx);
                let id = self.model.agents.iter()
                    .filter(|agent| agent.kind == AgentKind::Simulated)
                    .map(|agent| agent.id)
                    .max();
                if let Some(id) = id {
                    self.focus(Some(id), SwitchPath::Switcher, cx);
                }
            }
            PaletteCommand::Term { cwd, desktop } => {
                let desktop = desktop.unwrap_or(self.model.current_desktop);
                let id = self.mint_tile_id();
                let cwd = cwd.map(|cwd| expand_tilde(&cwd));
                let name = cwd.as_deref()
                    .map(|cwd| format!("{}/term", basename(&cwd.to_string_lossy())))
                    .unwrap_or_else(|| "term".to_string());
                match Shell::spawn(80, 24, cwd) {
                    Ok((shell, output)) => {
                        self.model.add_shell(id, name, desktop, b"").expect("a fresh tile id");
                        self.adopt_shell(id, shell, output, cx);
                    }
                    Err(error) => {
                        let message = format!("\x1b[31m{error}\x1b[0m\r\n");
                        self.model.add_shell(id, name, desktop, message.as_bytes()).expect("a fresh tile id");
                    }
                }
                self.dirty.insert(id);
                self.focus(Some(id), SwitchPath::Switcher, cx);
            }
            PaletteCommand::Doc { path, edit, desktop } => {
                let desktop = desktop.unwrap_or(self.model.current_desktop);
                let id = self.mint_tile_id();
                let path = expand_tilde(&path);
                let path = std::fs::canonicalize(&path).unwrap_or(path);
                let name = path.file_name()
                    .map(|name| name.to_string_lossy().into_owned())
                    .unwrap_or_else(|| path.display().to_string());
                self.model.add_document(id, name, desktop).expect("a fresh tile id");
                self.docs.insert(id, DocTile::open(path));
                if edit {
                    self.start_edit(id, cx);
                }
                self.focus(Some(id), SwitchPath::Switcher, cx);
            }
            PaletteCommand::Close => self.close_focused(cx),
        }
    }

    fn close_focused(&mut self, cx: &mut Context<Self>) {
        let Some(id) = self.model.focus else {
            return;
        };
        if self.model.agent(id).kind == AgentKind::Simulated {
            return;
        }
        self.shells.remove(&id);
        self.docs.remove(&id);
        self.grids.remove(&id);
        self.dirty.remove(&id);
        self.model.remove(id);
        cx.notify();
    }

    fn mint_tile_id(&mut self) -> AgentId {
        let id = self.next_tile_id;
        self.next_tile_id -= 1;
        id
    }

    fn on_modifiers(&mut self, event: &ModifiersChangedEvent, _window: &mut Window, cx: &mut Context<Self>) {
        let held = event.modifiers.platform;
        if held == self.cmd_held {
            return;
        }
        self.cmd_held = held;
        self.which_key_gen += 1;
        if held {
            let generation = self.which_key_gen;
            cx.spawn(async move |this, cx| {
                cx.background_executor().timer(Duration::from_millis(WHICH_KEY_MS)).await;
                this.update(cx, |zero, cx| {
                    if zero.which_key_gen == generation && zero.cmd_held && zero.popup == Popup::None {
                        zero.which_key = true;
                        cx.notify();
                    }
                })
                .ok();
            })
            .detach();
        } else if self.which_key {
            self.which_key = false;
            cx.notify();
        }
    }

    /// The plain keys the focused pane answers to, for the overlay's contextual column.
    fn focused_hints(&self) -> Option<(&'static str, Vec<(&'static str, &'static str)>)> {
        let id = self.model.focus?;
        Some(match self.model.agent(id).kind {
            AgentKind::Document => match self.docs.get(&id).map(|doc| matches!(doc.face, Face::Edit(_))) {
                Some(true) => ("this document", vec![("vim", "keys go to neovim"), (":wq", "save & read"), (":q!", "discard")]),
                _ => ("this document", vec![("e", "edit"), ("j k ␣", "scroll"), ("g G", "top · bottom")]),
            },
            AgentKind::Shell => ("this shell", vec![("type ⏎", "runs in the shell")]),
            AgentKind::Simulated => ("this agent", vec![("type ⏎", "prompt"), ("y n", "approve · decline")]),
        })
    }

    fn render_which_key(&self) -> Option<AnyElement> {
        if !self.which_key || self.popup != Popup::None {
            return None;
        }
        let mut strip = div().flex().items_start().gap(px(28.));
        let mut columns: Vec<(&'static str, Vec<(String, &'static str)>)> = GROUPS
            .iter()
            .filter(|group| group.overlay)
            .map(|group| (group.title, group.bindings.iter().map(|binding| (binding.keys.to_string(), binding.terse)).collect()))
            .collect();
        if let Some((title, hints)) = self.focused_hints() {
            columns.push((title, hints.into_iter().map(|(keys, terse)| (keys.to_string(), terse)).collect()));
        }
        for (title, hints) in columns {
            let mut column = div().flex().flex_col().gap(px(6.)).child(
                div()
                    .text_size(px(10.))
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::comment())
                    .child(title.to_uppercase()),
            );
            for (keys, terse) in hints {
                column = column.child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(8.))
                        .child(key_cap(&keys))
                        .child(div().text_size(px(11.5)).text_color(theme::fg_dark()).child(terse)),
                );
            }
            strip = strip.child(column);
        }
        Some(
            div()
                .absolute()
                .left_0()
                .bottom_0()
                .w_full()
                .flex()
                .justify_center()
                .pb(px(GAP + 2.))
                .child(
                    div()
                        .rounded(px(12.))
                        .bg(theme::bg_dark().alpha(0.96))
                        .border_1()
                        .border_color(theme::gutter())
                        .shadow(vec![BoxShadow {
                            color: gpui::black().alpha(0.5),
                            offset: point(px(0.), px(10.)),
                            blur_radius: px(34.),
                            spread_radius: px(0.),
                        }])
                        .px(px(18.))
                        .py(px(12.))
                        .child(strip)
                        .with_animation(
                            ("which-key", self.which_key_gen),
                            Animation::new(Duration::from_millis(120)).with_easing(ease_in_out),
                            |panel, delta| panel.opacity(delta),
                        ),
                )
                .into_any_element(),
        )
    }

    fn prepare_grid(
        &mut self,
        id: AgentId,
        bounds: Bounds<Pixels>,
        focused: bool,
        window: &mut Window,
    ) -> Option<Prepared> {
        if !self.model.agents.iter().any(|agent| agent.id == id) {
            return None;
        }
        let origin = point(bounds.origin.x + px(10.), bounds.origin.y + px(6.));
        let avail = size(bounds.size.width - px(20.), bounds.size.height - px(10.));
        let cols = (f32::from(avail.width) / f32::from(self.metrics.cell_width)).floor().max(1.) as u16;
        let rows = (f32::from(avail.height) / f32::from(self.metrics.line_height)).floor().max(1.) as u16;
        let agent = self.model.agent(id);
        if agent.cols != cols || agent.rows != rows {
            self.resize(id, cols, rows);
        }
        let grid = self
            .grids
            .entry(id)
            .or_insert_with(|| Grid::new().expect("a render state"));
        if self.dirty.remove(&id) || grid.size() != (cols, rows) {
            grid.refresh(&self.model.agent(id).terminal, cols, rows).ok()?;
        }
        let color = if self.model.agent(id).kind == AgentKind::Shell {
            theme::teal()
        } else {
            theme::blue()
        };
        Some(grid.prepare(&self.metrics, origin, focused, color, window))
    }

    fn doc_key(&mut self, id: AgentId, keystroke: &Keystroke, cx: &mut Context<Self>) {
        let reading = matches!(self.docs.get(&id).map(|tile| &tile.face), Some(Face::Read));
        if reading && keystroke.key == "e" && !keystroke.modifiers.shift {
            self.start_edit(id, cx);
            return;
        }
        let Some(tile) = self.docs.get_mut(&id) else {
            return;
        };
        if let Face::Edit(session) = &mut tile.face {
            if let Some(key) = keys::editor_key(keystroke) {
                session.editor.input(&key);
            }
            return;
        }
        let page = f32::from(tile.scroll.bounds().size.height) - 40.;
        let shift = keystroke.modifiers.shift;
        let by = match keystroke.key.as_str() {
            "j" | "down" => 40.,
            "k" | "up" => -40.,
            "space" if shift => -page,
            "space" | "pagedown" => page,
            "pageup" => -page,
            "g" if shift => f32::INFINITY,
            "g" | "home" => f32::NEG_INFINITY,
            "end" => f32::INFINITY,
            _ => return,
        };
        let max = f32::from(tile.scroll.max_offset().height);
        let next = (f32::from(tile.scroll.offset().y) - by).clamp(-max, 0.);
        tile.scroll.set_offset(point(px(0.), px(next)));
        cx.notify();
    }

    /// The cell grid a tile's body holds right now, for an editor spawned into it.
    fn grid_size_for(&self, id: AgentId) -> (u16, u16) {
        // Before the first layout nothing has a size; the first paint resizes the engine.
        if self.content.size.width <= px(0.) {
            return (80, 24);
        }
        let ids = self.model.desktop_agents();
        let rect = ids
            .iter()
            .position(|&other| other == id)
            .and_then(|index| tiles(self.content, ids.len(), px(GAP), self.ratio()).into_iter().nth(index));
        let Some(rect) = rect else {
            return (80, 24);
        };
        let cols = ((f32::from(rect.size.width) - 20.) / f32::from(self.metrics.cell_width)).floor().max(1.);
        let rows = ((f32::from(rect.size.height) - 40.) / f32::from(self.metrics.line_height)).floor().max(1.);
        (cols as u16, rows as u16)
    }

    fn start_edit(&mut self, id: AgentId, cx: &mut Context<Self>) {
        let (cols, rows) = self.grid_size_for(id);
        let Some(tile) = self.docs.get_mut(&id) else {
            return;
        };
        if matches!(tile.face, Face::Edit(_)) {
            return;
        }
        let open = Open { path: tile.path.clone(), line: 1, cols, rows, clean: false };
        match zero::editor::spawn(Engine::Neovim, open) {
            Ok((editor, events)) => {
                let (sender, mut receiver) = mpsc::unbounded::<EditorEvent>();
                thread::spawn(move || {
                    for event in events {
                        if sender.unbounded_send(event).is_err() {
                            break;
                        }
                    }
                });
                let pump = cx.spawn(async move |this, cx| {
                    while let Some(event) = receiver.next().await {
                        let mut batch = vec![event];
                        while let Ok(more) = receiver.try_recv() {
                            batch.push(more);
                        }
                        if this.update(cx, |zero, cx| zero.on_editor_event(id, batch, cx)).is_err() {
                            break;
                        }
                    }
                });
                tile.face = Face::Edit(EditSession {
                    editor,
                    frame: Frame::default(),
                    mode: String::new(),
                    size: (cols, rows),
                    _pump: pump,
                });
            }
            Err(error) => tile.error = Some(format!("{error:#}")),
        }
        cx.notify();
    }

    fn on_editor_event(&mut self, id: AgentId, batch: Vec<EditorEvent>, cx: &mut Context<Self>) {
        let Some(tile) = self.docs.get_mut(&id) else {
            return;
        };
        if batch.contains(&EditorEvent::Exited) {
            tile.face = Face::Read;
            tile.reload();
        } else if let Face::Edit(session) = &mut tile.face {
            let frame = session.editor.frame();
            session.mode = frame.mode.clone();
            session.frame = Frame::from_editor(&frame);
        }
        cx.notify();
    }

    fn close_editor(&mut self, id: AgentId, save: bool, cx: &mut Context<Self>) {
        if let Some(DocTile { face: Face::Edit(session), .. }) = self.docs.get_mut(&id) {
            if save {
                session.editor.save_and_quit();
            } else {
                session.editor.quit();
            }
        }
        cx.notify();
    }

    fn prepare_editor(
        &mut self,
        id: AgentId,
        bounds: Bounds<Pixels>,
        focused: bool,
        window: &mut Window,
    ) -> Option<Prepared> {
        let origin = point(bounds.origin.x + px(10.), bounds.origin.y + px(6.));
        let avail = size(bounds.size.width - px(20.), bounds.size.height - px(10.));
        let cols = (f32::from(avail.width) / f32::from(self.metrics.cell_width)).floor().max(1.) as u16;
        let rows = (f32::from(avail.height) / f32::from(self.metrics.line_height)).floor().max(1.) as u16;
        let Some(DocTile { face: Face::Edit(session), .. }) = self.docs.get_mut(&id) else {
            return None;
        };
        if session.size != (cols, rows) {
            session.size = (cols, rows);
            session.editor.resize(cols, rows);
        }
        let (_, color) = theme::mode(&session.mode);
        Some(session.frame.prepare(&self.metrics, origin, focused, color, window))
    }

    fn render_reader(&mut self, id: AgentId, tile_width: Pixels, cx: &mut Context<Self>) -> AnyElement {
        let _ = cx;
        let Some(tile) = self.docs.get_mut(&id) else {
            return div().into_any_element();
        };
        let width = (tile_width - px(56.)).min(px(720.));
        let mut content: Vec<AnyElement> = Vec::new();
        if let Some(error) = &tile.error {
            content.push(reader::error_card(error.clone()).into_any_element());
        }
        let dir = tile.path.parent().map(Path::to_path_buf).unwrap_or_default();
        let mut reader = Reader::new(dir, &mut tile.diagrams, width);
        content.extend(reader.render(&tile.blocks));
        div()
            .id(("reader", id))
            .flex_1()
            .w_full()
            .min_h(px(0.))
            .overflow_y_scroll()
            .track_scroll(&tile.scroll)
            .px(px(28.))
            .py(px(18.))
            .font_family(".SystemUIFont")
            .text_size(px(14.5))
            .line_height(relative(1.55))
            .text_color(theme::fg())
            .child(div().w_full().max_w(width).flex().flex_col().gap(px(12.)).children(content))
            .into_any_element()
    }

    fn render_pane(&mut self, id: AgentId, rect: Bounds<Pixels>, now: Duration, cx: &mut Context<Self>) -> AnyElement {
        let agent = self.model.agent(id);
        let focused = self.model.focus == Some(id);
        let owes = agent.turn_opened_at.is_some();
        let is_doc = agent.kind == AgentKind::Document;
        let editing = self.docs.get(&id).and_then(|tile| match &tile.face {
            Face::Edit(session) => Some(session.mode.clone()),
            Face::Read => None,
        });
        let (label, color) = match &editing {
            Some(mode) => theme::mode(mode),
            None if is_doc => ("read", theme::doc()),
            None => (agent.state.label(), theme::state_color(agent.kind, agent.state)),
        };
        let age = agent.turn_opened_at.map(|opened| now.saturating_sub(opened));
        let warmth = age.map_or(0., |age| (age.as_secs_f32() / 120.).min(1.));
        let ring = if focused {
            theme::blue()
        } else if owes {
            color.alpha(0.4)
        } else {
            theme::gutter().alpha(0.6)
        };
        let glow = if focused {
            theme::blue().alpha(0.55)
        } else if owes {
            color.alpha(0.08 + 0.22 * warmth)
        } else {
            gpui::transparent_black()
        };
        let name = agent.name.clone();
        let entity = cx.entity();
        let mut header = div()
            .flex_none()
            .h(px(30.))
            .px(px(12.))
            .flex()
            .items_center()
            .gap(px(8.))
            .bg(if focused { theme::blue().alpha(0.14) } else { theme::card_header() })
            .border_b_1()
            .border_color(theme::gutter().alpha(0.5))
            .child(
                div()
                    .flex_none()
                    .size(px(8.))
                    .rounded_full()
                    .bg(color)
                    .shadow(vec![BoxShadow {
                        color: color.alpha(0.7),
                        offset: point(px(0.), px(0.)),
                        blur_radius: px(6.),
                        spread_radius: px(0.),
                    }]),
            )
            .child(
                div()
                    .text_size(px(12.5))
                    .font_weight(if focused { FontWeight::SEMIBOLD } else { FontWeight::MEDIUM })
                    .text_color(if focused { theme::fg() } else { theme::fg_dark() })
                    .whitespace_nowrap()
                    .overflow_hidden()
                    .text_ellipsis()
                    .child(name),
            )
            .child(chip(label, color))
            .when_some(age, |header, age| {
                header.child(
                    div()
                        .text_size(px(11.5))
                        .font_family("JetBrains Mono")
                        .text_color(if warmth > 0.5 { theme::orange() } else { theme::comment() })
                        .child(format_age(age)),
                )
            })
            .child(div().flex_1());
        if owes {
            header = header.child(header_button(
                ("settle", id),
                "settle",
                "⌘⇧E",
                tip("close this turn and go to the next one", "⌘⇧E"),
                cx.listener(move |zero, _, _, cx| {
                    cx.stop_propagation();
                    if zero.model.focus == Some(id) {
                        zero.settle_and_next(cx);
                    } else {
                        zero.settle(id, cx);
                    }
                }),
            ));
        }
        if is_doc && editing.is_none() {
            header = header.child(header_button(
                ("edit", id),
                "edit",
                "e",
                tip("edit this file in place with neovim", "e"),
                cx.listener(move |zero, _, window, cx| {
                    cx.stop_propagation();
                    window.focus(&zero.focus_handle);
                    zero.focus(Some(id), SwitchPath::Click, cx);
                    zero.start_edit(id, cx);
                }),
            ));
        }
        if editing.is_some() {
            header = header
                .child(header_button(
                    ("save", id),
                    "save & close",
                    ":wq",
                    tip("write the file and go back to reading it", ":wq"),
                    cx.listener(move |zero, _, _, cx| {
                        cx.stop_propagation();
                        zero.close_editor(id, true, cx);
                    }),
                ))
                .child(header_button(
                    ("discard", id),
                    "discard",
                    ":q!",
                    tip("drop the changes and go back to reading", ":q!"),
                    cx.listener(move |zero, _, _, cx| {
                        cx.stop_propagation();
                        zero.close_editor(id, false, cx);
                    }),
                ));
        }
        let body: AnyElement = if is_doc && editing.is_none() {
            self.render_reader(id, rect.size.width, cx)
        } else {
            let editing_now = editing.is_some();
            canvas(
                move |bounds, window, cx| {
                    entity.update(cx, |zero, _| {
                        if editing_now {
                            zero.prepare_editor(id, bounds, focused, window)
                        } else {
                            zero.prepare_grid(id, bounds, focused, window)
                        }
                    })
                },
                |_, prepared, window, cx| {
                    if let Some(prepared) = prepared {
                        prepared.paint(window, cx);
                    }
                },
            )
            .flex_1()
            .w_full()
            .into_any_element()
        };
        div()
            .absolute()
            .left(rect.origin.x)
            .top(rect.origin.y)
            .w(rect.size.width)
            .h(rect.size.height)
            .flex()
            .flex_col()
            .rounded(px(10.))
            .overflow_hidden()
            .bg(if focused { theme::card() } else { theme::bg_dark() })
            .when(focused, |card| card.border_2())
            .when(!focused, |card| card.border_1())
            .border_color(ring)
            .opacity(if focused { 1. } else if owes { 0.92 } else { 0.8 })
            .shadow(vec![
                BoxShadow {
                    color: gpui::black().alpha(0.55),
                    offset: point(px(0.), px(12.)),
                    blur_radius: px(32.),
                    spread_radius: px(-4.),
                },
                BoxShadow {
                    color: glow,
                    offset: point(px(0.), px(0.)),
                    blur_radius: px(if focused { 30. } else { 22. }),
                    spread_radius: px(if focused { 3. } else { 2. }),
                },
            ])
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |zero, _, window, cx| {
                    window.focus(&zero.focus_handle);
                    zero.focus(Some(id), SwitchPath::Click, cx);
                }),
            )
            .when(!is_doc, |card| {
                card.on_scroll_wheel(cx.listener(move |zero, event, _, cx| zero.scroll(id, event, cx)))
            })
            .child(header)
            .child(body)
            .into_any_element()
    }

    /// The queue pill's hover face: every agent as a row, unfolding under the pill.
    fn render_queue_panel(&self, open: f32, now: Duration, cx: &mut Context<Self>) -> Stateful<Div> {
        let mut ids = self.model.queue();
        let rest: Vec<AgentId> = self.model.agents.iter().map(|agent| agent.id).filter(|id| !ids.contains(id)).collect();
        ids.extend(rest);
        let height = (12. + ids.len() as f32 * 28.) * open;
        let mut panel = div()
            .id("queue-panel")
            .occlude()
            .absolute()
            .top(px(26.))
            .left_0()
            .w_full()
            .min_w(px(360.))
            .h(px(height.max(1.)))
            .overflow_hidden()
            .rounded_bl(px(12.))
            .rounded_br(px(12.))
            .rounded_tr(px(4.))
            .bg(theme::bg_dark())
            .border_1()
            .border_color(theme::gutter())
            .shadow(vec![BoxShadow {
                color: gpui::black().alpha(0.5),
                offset: point(px(0.), px(14.)),
                blur_radius: px(38.),
                spread_radius: px(0.),
            }])
            .opacity((open * 1.5).min(1.))
            .flex()
            .flex_col()
            .py(px(6.))
            .on_hover(cx.listener(|zero, hovered: &bool, _, cx| {
                zero.queue_morph.on_panel = *hovered;
                zero.queue_morph.retarget();
                cx.notify();
            }));
        for (index, id) in ids.into_iter().enumerate() {
            let agent = self.model.agent(id);
            let color = theme::state_color(agent.kind, agent.state);
            let age = agent.turn_opened_at.map(|opened| format_age(now.saturating_sub(opened))).unwrap_or_default();
            let owes = agent.turn_opened_at.is_some();
            let reveal = ((open - 0.1 - (index as f32).min(8.) * 0.05) * 3.).clamp(0., 1.);
            panel = panel.child(
                div()
                    .id(("queue-row", id))
                    .h(px(28.))
                    .px(px(12.))
                    .flex()
                    .items_center()
                    .gap(px(10.))
                    .cursor_pointer()
                    .opacity(reveal)
                    .hover(|row| row.bg(theme::bg_highlight().alpha(0.7)))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |zero, _, _, cx| {
                            cx.stop_propagation();
                            zero.queue_morph = Morph::default();
                            zero.focus(Some(id), SwitchPath::Click, cx);
                        }),
                    )
                    .child(dot(color))
                    .child(div().flex_1().text_color(if owes { theme::fg() } else { theme::fg_dark() }).child(agent.name.clone()))
                    .child(key_cap(&format!("⌘{}", agent.desktop)))
                    .child(chip(label(agent), color))
                    .child(div().w(px(40.)).font_family("JetBrains Mono").text_size(px(11.)).text_color(theme::comment()).child(age))
                    .when(index == 0 && owes, |row| row.child(key_cap("⌘J"))),
            );
        }
        panel
    }

    fn render_bar(&self, now: Duration, cx: &mut Context<Self>) -> Div {
        let tips = matches!(self.popup, Popup::None);
        let queue = self.model.queue();
        let waiting = queue.len();
        let open = ease_in_out(self.queue_morph.t);
        let mut left = div()
            .id("queue-pill")
            .relative()
            .flex()
            .items_center()
            .gap(px(8.))
            .h(px(26.))
            .px(px(11.))
            .rounded_tl(px(13.))
            .rounded_tr(px(13.))
            .rounded_bl(px(13. * (1. - open)))
            .rounded_br(px(13. * (1. - open)))
            .cursor_pointer()
            .when(tips && open == 0., |pill| pill.tooltip(tip("agents & commands", "⌘K")))
            .on_hover(cx.listener(|zero, hovered: &bool, _, cx| {
                zero.queue_morph.on_pill = *hovered;
                zero.queue_morph.retarget();
                cx.notify();
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|zero, _, _, cx| {
                    cx.stop_propagation();
                    zero.open(Popup::Palette { query: String::new(), selected: 0 }, cx);
                }),
            );
        if waiting > 0 {
            left = left
                .bg(theme::yellow().alpha(0.12))
                .hover(|pill| pill.bg(theme::yellow().alpha(0.2)))
                .child(dot(theme::yellow()))
                .child(
                    div()
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(theme::yellow())
                        .child(format!("{waiting} waiting")),
                );
            for (index, id) in queue.iter().copied().take(3).enumerate() {
                let agent = self.model.agent(id);
                left = left.child(div().text_color(theme::comment()).opacity(1. - 0.8 * open).child(if index == 0 { "·" } else { "›" }));
                left = left.child(
                    div()
                        .id(("chip", id))
                        .cursor_pointer()
                        .when(tips && index == 0 && open == 0., |chip| chip.tooltip(tip("next in the queue", "⌘J")))
                        .opacity(1. - 0.8 * open)
                        .text_color(theme::fg_dark())
                        .hover(|chip| chip.text_color(theme::fg()))
                        .child(agent.name.clone())
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |zero, _, _, cx| {
                                cx.stop_propagation();
                                zero.focus(Some(id), SwitchPath::Click, cx);
                            }),
                        ),
                );
            }
            if waiting > 3 {
                left = left.child(div().text_color(theme::comment()).opacity(1. - 0.8 * open).child(format!("+{}", waiting - 3)));
            }
        } else {
            left = left
                .hover(|pill| pill.bg(theme::bg_highlight()))
                .child(dot(theme::comment()))
                .child(div().text_color(theme::comment()).child("all quiet"));
        }
        left = left.child(div().text_color(theme::comment()).text_size(px(10.)).child("▾"));
        if waiting > 0 && open > 0. {
            left = left.child(self.render_queue_panel(open, now, cx));
        }

        let mut center = div().flex().items_center().gap(px(4.));
        for desktop in 1..=9u8 {
            center = center.child(self.render_badge(desktop, cx));
        }

        let mut right = div().flex().items_center().gap(px(10.));
        if let Some(agent) = self.model.focused() {
            let color = theme::state_color(agent.kind, agent.state);
            right = right
                .child(div().text_color(theme::fg()).font_weight(FontWeight::MEDIUM).child(agent.name.clone()))
                .child(chip(label(agent), color));
        }
        let speed = self.source.speed();
        right = right
            .child(
                div()
                    .id("scenario")
                    .cursor_pointer()
                    .px(px(9.))
                    .h(px(22.))
                    .flex()
                    .items_center()
                    .rounded(px(6.))
                    .bg(theme::purple().alpha(0.12))
                    .text_color(theme::purple())
                    .text_size(px(11.5))
                    .hover(|el| el.bg(theme::purple().alpha(0.22)))
                    .when(tips, |pill| pill.tooltip(tip("scenario and knobs", "⌘S")))
                    .child(format!("{} · x{speed}", self.scenario_name()))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|zero, _, _, cx| {
                            cx.stop_propagation();
                            zero.open(Popup::Scenario { selected: 0 }, cx);
                        }),
                    ),
            )
            .child(
                div()
                    .id("help")
                    .cursor_pointer()
                    .flex()
                    .items_center()
                    .gap(px(6.))
                    .text_color(theme::comment())
                    .text_size(px(11.5))
                    .hover(|el| el.text_color(theme::fg_dark()))
                    .when(tips, |pill| pill.tooltip(tip("every key", "⌘/")))
                    .child("keys")
                    .child(key_cap("⌘/"))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|zero, _, _, cx| {
                            cx.stop_propagation();
                            zero.open(Popup::Help, cx);
                        }),
                    ),
            );
        let _ = now;
        div()
            .absolute()
            .top_0()
            .left_0()
            .w_full()
            .h(px(BAR_HEIGHT))
            .flex()
            .items_center()
            .pl(px(86.))
            .pr(px(14.))
            .gap(px(12.))
            .on_mouse_down(MouseButton::Left, |_, window, _| window.start_window_move())
            .child(left)
            .child(div().flex_1())
            .child(center)
            .child(div().flex_1())
            .child(right)
    }

    fn render_badge(&self, desktop: u8, cx: &mut Context<Self>) -> AnyElement {
        let tips = matches!(self.popup, Popup::None);
        let current = desktop == self.model.current_desktop;
        let owed = self.model.desktop_waiting_count(desktop);
        let populated = self.model.agents.iter().any(|agent| agent.desktop == desktop);
        let mut badge = div()
            .id(("badge", desktop as u64))
            .relative()
            .w(px(24.))
            .h(px(22.))
            .rounded(px(6.))
            .flex()
            .items_center()
            .justify_center()
            .font_family("JetBrains Mono")
            .text_size(px(11.5))
            .cursor_pointer()
            .when(tips, |badge| {
                badge.tooltip(tip(format!("desktop {desktop} · ⌘⌥{desktop} brings the focused pane here"), format!("⌘{desktop}")))
            })
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |zero, _, _, cx| {
                    cx.stop_propagation();
                    zero.go_desktop(desktop, cx);
                }),
            )
            .child(desktop.to_string());
        badge = if current {
            badge.bg(theme::blue()).text_color(theme::bg_dark()).font_weight(FontWeight::BOLD)
        } else if owed > 0 {
            badge
                .bg(theme::yellow().alpha(0.14))
                .text_color(theme::yellow())
                .hover(|el| el.bg(theme::yellow().alpha(0.26)))
        } else if populated {
            badge.text_color(theme::fg_dark()).hover(|el| el.bg(theme::bg_highlight()))
        } else {
            badge.text_color(theme::comment().alpha(0.6)).hover(|el| el.bg(theme::bg_highlight()))
        };
        if owed > 0 {
            badge = badge.child(
                div()
                    .absolute()
                    .top(px(-4.))
                    .right(px(-5.))
                    .min_w(px(13.))
                    .h(px(13.))
                    .px(px(3.))
                    .rounded_full()
                    .bg(theme::yellow())
                    .text_color(theme::bg_dark())
                    .text_size(px(8.5))
                    .font_weight(FontWeight::BOLD)
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(owed.to_string()),
            );
        }
        badge.into_any_element()
    }

    fn render_popup(&self, now: Duration, cx: &mut Context<Self>) -> Option<AnyElement> {
        let anchored = matches!(self.popup, Popup::Palette { .. });
        let panel = match &self.popup {
            Popup::None => return None,
            Popup::Palette { query, selected } => {
                let view = self.palette_view(query);
                let list = palette_rows(&view);
                let selected = (*selected).min(list.len().saturating_sub(1));
                let first = selected.saturating_sub(11);
                let right = match view.grammar {
                    Some(grammar) => grammar.to_string(),
                    None => format!("{} of {}", view.agents.len(), self.model.agents.len()),
                };
                let mut panel = popup_panel(px(640.)).child(
                    div()
                        .h(px(46.))
                        .px(px(16.))
                        .flex()
                        .items_center()
                        .gap(px(10.))
                        .border_b_1()
                        .border_color(theme::gutter().alpha(0.6))
                        .child(key_cap("⌘K"))
                        .child(
                            div()
                                .flex_1()
                                .font_family("JetBrains Mono")
                                .text_size(px(13.))
                                .text_color(theme::fg())
                                .child(format!("{query}▍")),
                        )
                        .child(div().text_color(theme::comment()).text_size(px(11.)).child(right)),
                );
                if list.is_empty() {
                    panel = panel.child(div().p(px(16.)).text_color(theme::comment()).child(
                        if view.grammar.is_some() { "keep typing…" } else { "nothing matches" },
                    ));
                }
                let verbs_start = list.len() - view.verbs.len();
                for (index, row) in list.iter().enumerate().skip(first).take(12) {
                    let is_selected = index == selected;
                    if index == verbs_start && !view.verbs.is_empty() && !view.agents.is_empty() {
                        panel = panel.child(
                            div()
                                .h(px(24.))
                                .px(px(16.))
                                .flex()
                                .items_end()
                                .text_size(px(10.))
                                .font_weight(FontWeight::SEMIBOLD)
                                .text_color(theme::comment())
                                .child("COMMANDS"),
                        );
                    }
                    panel = panel.child(match row {
                        PaletteRow::Run => {
                            let command = view.run.clone().expect("a run row carries its command");
                            let description = self.describe(&command);
                            div()
                                .id("palette-run")
                                .h(px(38.))
                                .px(px(16.))
                                .flex()
                                .items_center()
                                .gap(px(10.))
                                .cursor_pointer()
                                .bg(theme::blue().alpha(if is_selected { 0.18 } else { 0.10 }))
                                .hover(|row| row.bg(theme::blue().alpha(0.22)))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |zero, _, _, cx| {
                                        cx.stop_propagation();
                                        zero.popup = Popup::None;
                                        zero.run_palette_command(command.clone(), cx);
                                    }),
                                )
                                .child(div().w(px(10.)).text_color(theme::blue()).child("›"))
                                .child(div().flex_1().text_color(theme::fg()).font_weight(FontWeight::MEDIUM).child(description))
                                .child(key_cap("⏎"))
                        }
                        PaletteRow::Candidate(candidate_index) => {
                            let candidate = &view.completions[*candidate_index];
                            let insert = candidate.insert.clone();
                            let space = candidate.space;
                            let from = view.replace_from;
                            let base = query.clone();
                            div()
                                .id(("palette-candidate", *candidate_index as u64))
                                .h(px(32.))
                                .px(px(16.))
                                .flex()
                                .items_center()
                                .gap(px(10.))
                                .cursor_pointer()
                                .when(is_selected, |row| row.bg(theme::bg_highlight()))
                                .hover(|row| row.bg(theme::bg_highlight().alpha(0.7)))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |zero, _, _, cx| {
                                        cx.stop_propagation();
                                        zero.popup = Popup::Palette { query: palette_fill(&base, from, &insert, space), selected: 0 };
                                        cx.notify();
                                    }),
                                )
                                .child(div().w(px(10.)).text_color(theme::blue()).child(if is_selected { "›" } else { "" }))
                                .child(div().font_family("JetBrains Mono").text_size(px(12.5)).text_color(theme::fg()).child(candidate.insert.clone()))
                                .child(div().flex_1())
                                .child(div().text_size(px(11.)).text_color(theme::comment()).child(candidate.note.clone()))
                                .when(is_selected, |row| row.child(key_cap("⇥")))
                        }
                        PaletteRow::Agent(id) => {
                            let id = *id;
                            let agent = self.model.agent(id);
                            let color = theme::state_color(agent.kind, agent.state);
                            let age = agent.turn_opened_at.map(|opened| format_age(now.saturating_sub(opened))).unwrap_or_default();
                            div()
                                .id(("row", id))
                                .h(px(34.))
                                .px(px(16.))
                                .flex()
                                .items_center()
                                .gap(px(10.))
                                .cursor_pointer()
                                .when(is_selected, |row| row.bg(theme::bg_highlight()))
                                .hover(|row| row.bg(theme::bg_highlight().alpha(0.7)))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |zero, _, _, cx| {
                                        cx.stop_propagation();
                                        zero.popup = Popup::None;
                                        zero.focus(Some(id), SwitchPath::Switcher, cx);
                                    }),
                                )
                                .child(div().w(px(10.)).text_color(theme::blue()).child(if is_selected { "›" } else { "" }))
                                .child(dot(color))
                                .child(div().flex_1().text_color(if is_selected { theme::fg() } else { theme::fg_dark() }).child(agent.name.clone()))
                                .child(key_cap(&format!("⌘{}", agent.desktop)))
                                .child(chip(label(agent), color))
                                .child(div().w(px(40.)).font_family("JetBrains Mono").text_size(px(11.)).text_color(theme::comment()).child(age))
                                .child(div().w(px(40.)).flex().justify_end().when(
                                    index == 0 && query.is_empty() && agent.turn_opened_at.is_some(),
                                    |cell| cell.child(key_cap("⌘J")),
                                ))
                        }
                        PaletteRow::Verb(verb) => {
                            let verb = *verb;
                            div()
                                .id(("palette-verb", index as u64))
                                .h(px(32.))
                                .px(px(16.))
                                .flex()
                                .items_center()
                                .gap(px(10.))
                                .cursor_pointer()
                                .when(is_selected, |row| row.bg(theme::bg_highlight()))
                                .hover(|row| row.bg(theme::bg_highlight().alpha(0.7)))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |zero, _, _, cx| {
                                        cx.stop_propagation();
                                        zero.popup = Popup::Palette { query: format!("{} ", verb.word()), selected: 0 };
                                        cx.notify();
                                    }),
                                )
                                .child(div().w(px(10.)).text_color(theme::blue()).child(if is_selected { "›" } else { "" }))
                                .child(div().font_family("JetBrains Mono").text_size(px(12.5)).text_color(theme::fg()).child(verb.word()))
                                .child(div().flex_1().text_size(px(11.5)).text_color(theme::comment()).child(verb.detail()))
                                .when(is_selected, |row| row.child(key_cap("⇥")))
                        }
                    });
                }
                let footer = div()
                    .h(px(30.))
                    .px(px(16.))
                    .flex()
                    .items_center()
                    .gap(px(14.))
                    .border_t_1()
                    .border_color(theme::gutter().alpha(0.6))
                    .text_size(px(11.))
                    .text_color(theme::comment());
                let footer = match view.grammar {
                    Some(grammar) => footer
                        .child(div().font_family("JetBrains Mono").child(grammar))
                        .child(div().flex_1())
                        .child("⇥ fill")
                        .child("⏎ run")
                        .child("esc closes"),
                    None => footer
                        .child("↑↓ move")
                        .child("⏎ jump")
                        .child("type a name to filter")
                        .child("or new · term · doc · close")
                        .child("esc closes"),
                };
                panel.child(footer)
            }
            Popup::Scenario { selected } => {
                let mut panel = popup_panel(px(520.)).child(popup_title("scenario", format!("{} · x{}", self.scenario_name(), self.source.speed())));
                for (index, item) in SCENARIO_ITEMS.iter().enumerate() {
                    let is_selected = index == *selected;
                    panel = panel.child(
                        div()
                            .id(("scenario-item", index as u64))
                            .h(px(36.))
                            .px(px(16.))
                            .flex()
                            .items_center()
                            .gap(px(12.))
                            .cursor_pointer()
                            .when(is_selected, |row| row.bg(theme::bg_highlight()))
                            .hover(|row| row.bg(theme::bg_highlight().alpha(0.7)))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |zero, _, _, cx| {
                                    cx.stop_propagation();
                                    zero.run_scenario_item(index, cx);
                                }),
                            )
                            .child(key_cap(&(index + 1).to_string()))
                            .child(div().text_color(theme::fg()).font_weight(FontWeight::MEDIUM).child(item.label))
                            .child(div().text_color(theme::comment()).child(match item.action {
                                ItemAction::Motion => format!("desktop slide {}ms, then {}", self.motion_ms, MOTION_STEPS[(MOTION_STEPS.iter().position(|&ms| ms == self.motion_ms).unwrap_or(0) + 1) % MOTION_STEPS.len()]),
                                ItemAction::Source(_) => item.detail.to_string(),
                            })),
                    );
                }
                panel
            }
            Popup::Help => {
                let mut panel = popup_panel(px(640.)).child(popup_title("keys", "hold ⌘ for the short version".to_string()));
                for group in GROUPS.iter() {
                    panel = panel.child(
                        div()
                            .h(px(26.))
                            .px(px(16.))
                            .flex()
                            .items_end()
                            .text_size(px(10.))
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::comment())
                            .child(group.title.to_uppercase()),
                    );
                    for binding in group.bindings {
                        panel = panel.child(
                            div()
                                .h(px(24.))
                                .px(px(16.))
                                .flex()
                                .items_center()
                                .gap(px(12.))
                                .child(div().w(px(96.)).flex().child(key_cap(binding.keys)))
                                .child(div().text_color(theme::fg_dark()).child(binding.help)),
                        );
                    }
                }
                panel.child(div().h(px(10.)))
            }
        };
        Some(
            div()
                .absolute()
                .left_0()
                .top_0()
                .size_full()
                .bg(gpui::black().alpha(0.4))
                .flex()
                .items_start()
                .when(anchored, |scrim| scrim.justify_start().pt(px(BAR_HEIGHT - 6.)).pl(px(80.)))
                .when(!anchored, |scrim| scrim.justify_center().pt(px(110.)))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|zero, _, _, cx| {
                        zero.popup = Popup::None;
                        cx.notify();
                    }),
                )
                .child(panel.on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation()))
                .into_any_element(),
        )
    }
}

impl Render for Zero {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let viewport = window.viewport_size();
        self.content = Bounds {
            origin: point(px(GAP), px(BAR_HEIGHT)),
            size: size(viewport.width - px(GAP * 2.), viewport.height - px(BAR_HEIGHT) - px(GAP)),
        };
        if self.model.queue().is_empty() {
            self.queue_morph = Morph::default();
        }
        if self.queue_morph.step() {
            window.request_animation_frame();
        }
        let now = self.now();
        let ids = self.model.desktop_agents();
        let rects = tiles(self.content, ids.len(), px(GAP), self.ratio());
        let panes: Vec<AnyElement> = ids
            .iter()
            .zip(rects)
            .map(|(&id, rect)| self.render_pane(id, rect, now, cx))
            .collect();
        let (direction, generation) = self.slide;
        let slide_from = px(64. * direction as f32);
        let desktop_layer = div()
            .absolute()
            .left_0()
            .top_0()
            .size_full()
            .children(panes)
            .with_animation(
                ("desktop", generation),
                Animation::new(Duration::from_millis(self.motion_ms)).with_easing(ease_in_out),
                move |layer, delta| layer.left(slide_from * (1. - delta)).opacity(0.35 + 0.65 * delta),
            );
        let mut root = div()
            .relative()
            .size_full()
            .bg(linear_gradient(
                180.,
                linear_color_stop(theme::desk(), 0.),
                linear_color_stop(theme::desk_far(), 1.),
            ))
            .track_focus(&self.focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .on_modifiers_changed(cx.listener(Self::on_modifiers))
            .font_family(".SystemUIFont")
            .text_size(px(12.5))
            .text_color(theme::fg())
            .child(desktop_layer);
        if ids.is_empty() {
            root = root.child(
                div()
                    .absolute()
                    .left_0()
                    .top_0()
                    .size_full()
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_color(theme::comment())
                    .child(format!("desktop {} is empty · ⌘1…9 to move around · ⌘⌥1…9 to bring a pane here", self.model.current_desktop)),
            );
        }
        root = root.child(self.render_bar(now, cx));
        if let Some(overlay) = self.render_which_key() {
            root = root.child(overlay);
        }
        if let Some(popup) = self.render_popup(now, cx) {
            root = root.child(popup);
        }
        root
    }
}

/// What the palette shows for a query: agents to jump to, verbs, completions, and a runnable command.
struct PaletteView {
    agents: Vec<AgentId>,
    verbs: Vec<Verb>,
    run: Option<PaletteCommand>,
    completions: Vec<Candidate>,
    grammar: Option<&'static str>,
    replace_from: usize,
}

struct Candidate {
    insert: String,
    note: String,
    /// Whether accepting moves to the next argument; directories keep the cursor for deeper paths.
    space: bool,
}

enum PaletteRow {
    Run,
    Candidate(usize),
    Agent(AgentId),
    Verb(Verb),
}

fn palette_rows(view: &PaletteView) -> Vec<PaletteRow> {
    let mut rows = Vec::new();
    if view.run.is_some() {
        rows.push(PaletteRow::Run);
    }
    rows.extend((0..view.completions.len()).map(PaletteRow::Candidate));
    rows.extend(view.agents.iter().copied().map(PaletteRow::Agent));
    rows.extend(view.verbs.iter().copied().map(PaletteRow::Verb));
    rows
}

fn palette_fill(query: &str, from: usize, insert: &str, space: bool) -> String {
    let mut next = query[..from].to_string();
    next.push_str(insert);
    if space {
        next.push(' ');
    }
    next
}

fn candidates(expect: Expect, prefix: &str) -> Vec<Candidate> {
    let keyword_note = |word: &str| match word {
        "on" => "on <desktop>",
        "as" => "as <name>",
        "edit" => "open in the editor",
        _ => "",
    };
    match expect {
        Expect::Kind => palette::KINDS
            .iter()
            .filter(|kind| kind.starts_with(prefix))
            .map(|kind| Candidate { insert: kind.to_string(), note: "agent kind".to_string(), space: true })
            .collect(),
        Expect::Desktop => (1..=9)
            .map(|n| n.to_string())
            .filter(|n| n.starts_with(prefix))
            .map(|n| Candidate { insert: n, note: "desktop".to_string(), space: true })
            .collect(),
        Expect::Keyword(word) => std::iter::once(word)
            .filter(|word| word.starts_with(prefix))
            .map(|word| Candidate { insert: word.to_string(), note: keyword_note(word).to_string(), space: true })
            .collect(),
        Expect::Name => Vec::new(),
        Expect::Dir => fs_candidates(prefix, true),
        Expect::Path => fs_candidates(prefix, false),
    }
}

/// Real paths under the typed prefix: directories always, .md files when files are wanted.
fn fs_candidates(prefix: &str, dirs_only: bool) -> Vec<Candidate> {
    let (dir_part, name_prefix) = match prefix.rfind('/') {
        Some(index) => (&prefix[..=index], &prefix[index + 1..]),
        None => ("", prefix),
    };
    let search = if dir_part.is_empty() { PathBuf::from(".") } else { expand_tilde(dir_part) };
    let Ok(entries) = std::fs::read_dir(&search) else {
        return Vec::new();
    };
    let mut dirs = Vec::new();
    let mut files = Vec::new();
    for entry in entries.flatten() {
        let name = entry.file_name().to_string_lossy().into_owned();
        if !name.starts_with(name_prefix) || (name.starts_with('.') && !name_prefix.starts_with('.')) {
            continue;
        }
        let is_dir = entry.file_type().map(|kind| kind.is_dir()).unwrap_or(false);
        if is_dir {
            dirs.push(Candidate { insert: format!("{dir_part}{name}/"), note: "dir".to_string(), space: false });
        } else if !dirs_only && name.ends_with(".md") {
            files.push(Candidate { insert: format!("{dir_part}{name}"), note: "file".to_string(), space: true });
        }
    }
    dirs.sort_by(|a, b| a.insert.cmp(&b.insert));
    files.sort_by(|a, b| a.insert.cmp(&b.insert));
    // 8 rows fit the panel beside the run row and footer.
    files.into_iter().chain(dirs).take(8).collect()
}

fn expand_tilde(path: &str) -> PathBuf {
    if path == "~" || path.starts_with("~/") {
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join(path[1..].trim_start_matches('/'));
        }
    }
    PathBuf::from(path)
}

fn basename(path: &str) -> String {
    let trimmed = path.trim_end_matches('/');
    trimmed
        .rsplit('/')
        .next()
        .filter(|part| !part.is_empty() && *part != "~")
        .unwrap_or("sandbox")
        .to_string()
}

fn popup_panel(width: Pixels) -> Div {
    div()
        .w(width)
        .flex()
        .flex_col()
        .rounded(px(12.))
        .overflow_hidden()
        .bg(theme::bg_dark())
        .border_1()
        .border_color(theme::gutter())
        .shadow(vec![BoxShadow {
            color: gpui::black().alpha(0.6),
            offset: point(px(0.), px(18.)),
            blur_radius: px(48.),
            spread_radius: px(0.),
        }])
        .text_size(px(12.5))
}

fn popup_title(title: &'static str, detail: String) -> Div {
    div()
        .h(px(44.))
        .px(px(16.))
        .flex()
        .items_center()
        .gap(px(10.))
        .border_b_1()
        .border_color(theme::gutter().alpha(0.6))
        .child(div().font_weight(FontWeight::SEMIBOLD).text_color(theme::fg()).child(title))
        .child(div().text_color(theme::comment()).child(detail))
}

fn key_cap(text: &str) -> Div {
    div()
        .px(px(6.))
        .h(px(20.))
        .flex()
        .items_center()
        .rounded(px(5.))
        .bg(theme::bg_highlight())
        .border_1()
        .border_color(theme::gutter())
        .font_family("JetBrains Mono")
        .text_size(px(11.))
        .text_color(theme::fg_dark())
        .child(text.to_string())
}

/// What the bar and the list call an agent: its state, or "doc" for a document tile.
fn label(agent: &Agent) -> &'static str {
    if agent.kind == AgentKind::Document { "doc" } else { agent.state.label() }
}

/// A small header action that shows the key it stands for.
fn header_button(
    id: impl Into<ElementId>,
    text: &'static str,
    key: &str,
    tooltip: impl Fn(&mut Window, &mut App) -> AnyView + 'static,
    on_click: impl Fn(&MouseDownEvent, &mut Window, &mut App) + 'static,
) -> Stateful<Div> {
    div()
        .id(id)
        .flex_none()
        .h(px(20.))
        .pl(px(7.))
        .pr(px(4.))
        .flex()
        .items_center()
        .gap(px(6.))
        .rounded(px(5.))
        .cursor_pointer()
        .text_size(px(11.))
        .text_color(theme::fg_dark())
        .hover(|el| el.bg(theme::bg_highlight()).text_color(theme::fg()))
        .tooltip(tooltip)
        .on_mouse_down(MouseButton::Left, on_click)
        .child(text)
        .child(key_cap(key))
}

fn chip(label: &'static str, color: Hsla) -> Div {
    div()
        .flex_none()
        .px(px(7.))
        .h(px(18.))
        .flex()
        .items_center()
        .rounded(px(5.))
        .bg(color.alpha(0.16))
        .text_color(color)
        .text_size(px(10.5))
        .font_weight(FontWeight::MEDIUM)
        .child(label)
}

fn dot(color: Hsla) -> Div {
    div().flex_none().size(px(7.)).rounded_full().bg(color)
}

struct Tip {
    text: SharedString,
    key: SharedString,
}

impl Render for Tip {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div()
            .px(px(10.))
            .py(px(6.))
            .flex()
            .items_center()
            .gap(px(8.))
            .rounded(px(6.))
            .bg(theme::bg_dark())
            .border_1()
            .border_color(theme::gutter())
            .shadow(vec![BoxShadow {
                color: gpui::black().alpha(0.5),
                offset: point(px(0.), px(6.)),
                blur_radius: px(16.),
                spread_radius: px(0.),
            }])
            .font_family(".SystemUIFont")
            .text_size(px(12.))
            .text_color(theme::fg_dark())
            .child(self.text.clone())
            .when(!self.key.is_empty(), |tip| tip.child(key_cap(&self.key)))
    }
}

/// A hover tooltip naming the action and its key: the mouse path teaches the keyboard path.
fn tip(text: impl Into<SharedString>, key: impl Into<SharedString>) -> impl Fn(&mut Window, &mut App) -> AnyView + 'static {
    let text = text.into();
    let key = key.into();
    move |_, cx| {
        let (text, key) = (text.clone(), key.clone());
        cx.new(|_| Tip { text, key }).into()
    }
}

fn digit(keystroke: &Keystroke) -> Option<u8> {
    let key = keystroke.key.as_str();
    if let Ok(n) = key.parse::<u8>()
        && (1..=9).contains(&n)
    {
        return Some(n);
    }
    "!@#$%^&*(".find(key).map(|index| index as u8 + 1)
}

fn format_age(age: Duration) -> String {
    let seconds = age.as_secs();
    format!("{}:{:02}", seconds / 60, seconds % 60)
}

fn timestamp_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

#[cfg(test)]
mod tests {
    use super::*;

    fn launch(args: &[&str]) -> Result<Launch> {
        parse_launch(args.iter().map(|arg| arg.to_string()))
    }

    #[test]
    fn docs_take_a_desktop_prefix() {
        let launch = launch(&["--agents", "3", "--shell", "4", "--doc", "notes.md", "--edit", "3:plan.md"]).unwrap();
        assert_eq!(launch.agents, 3);
        assert_eq!(launch.shell, 4);
        let docs: Vec<_> = launch.docs.iter().map(|doc| (doc.desktop, doc.edit)).collect();
        assert_eq!(docs, [(1, false), (3, true)]);
        assert!(launch.docs[1].path.ends_with("plan.md"));
    }

    #[test]
    fn limits_name_themselves() {
        let error = launch(&["--agents", "13"]).unwrap_err().to_string();
        assert!(error.contains("13") && error.contains("12"), "{error}");
        let error = launch(&["--doc", "0:notes.md"]).unwrap_err().to_string();
        assert!(error.contains("1-9"), "{error}");
    }
}
