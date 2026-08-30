use std::collections::{HashMap, HashSet};
use std::io;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result, bail};
use crossbeam_channel::{Select, never, unbounded};
use crossterm::event::{
    self, DisableBracketedPaste, EnableBracketedPaste, Event as TerminalEvent, KeyCode, KeyEvent,
    KeyEventKind, KeyModifiers,
};
use crossterm::execute;
use crossterm::terminal::{
    EnterAlternateScreen, LeaveAlternateScreen, disable_raw_mode, enable_raw_mode,
};
use ratatui::backend::CrosstermBackend;
use ratatui::layout::{Margin, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::Line;
use ratatui::widgets::{Block, Borders, Clear, Paragraph, Wrap};
use ratatui::{Frame, Terminal};

use zero::layout::{Direction, neighbor, tile_layout};
use zero::model::{AgentId, AgentKind, AgentState, Model};
use zero::shell::{Shell, ShellOutput};
use zero::simulator::Simulator;
use zero::source::{Command, Event as SourceEvent, Scenario, ScenarioAction, Source};
use zero::switch_log::{SwitchLog, SwitchPath, default_path, summary};
use zero::switcher::filter_agents;
use zero::vt::VtRenderer;

const SHELL_ID: AgentId = u64::MAX;
const LEADER_KEY: KeyCode = KeyCode::Null;
const USAGE: &str = "usage:
  zero [--scenario calm|busy-morning|all-busy]
  zero --headless --scenario NAME --seconds N
  zero --summary";

fn main() -> Result<()> {
    let options = Options::parse()?;
    if options.summary {
        return print_summary();
    }
    if options.headless {
        return run_headless(options.scenario, options.seconds);
    }
    run_tui(options.scenario)
}

struct Options {
    summary: bool,
    headless: bool,
    scenario: Scenario,
    seconds: u64,
}

impl Options {
    fn parse() -> Result<Self> {
        let mut options = Self {
            summary: false,
            headless: false,
            scenario: Scenario::Calm,
            seconds: 60,
        };
        let mut args = std::env::args().skip(1);
        while let Some(argument) = args.next() {
            match argument.as_str() {
                "--summary" => options.summary = true,
                "--headless" => options.headless = true,
                "--scenario" => {
                    let value = args.next().context("--scenario needs a value")?;
                    options.scenario = Scenario::parse(&value)
                        .with_context(|| format!("unknown scenario {value}"))?;
                }
                "--seconds" => {
                    options.seconds = args.next().context("--seconds needs a value")?.parse()?;
                }
                "-h" | "--help" => {
                    println!("{USAGE}");
                    std::process::exit(0);
                }
                other => bail!("unknown argument {other}\n{USAGE}"),
            }
        }
        Ok(options)
    }
}

fn print_summary() -> Result<()> {
    let path = default_path()?;
    let counts = summary(&path)?;
    println!("switch log: {}", path.display());
    if counts.is_empty() {
        println!("no switches recorded");
    } else {
        let total: usize = counts.values().sum();
        println!("{total} switches");
        for (path, count) in counts {
            println!("{path:>10}  {count}");
        }
    }
    Ok(())
}

fn run_headless(scenario: Scenario, seconds: u64) -> Result<()> {
    let mut source = Simulator::new(scenario);
    let mut model = Model::new();
    model.apply(source.advance(Duration::ZERO))?;
    let end = Duration::from_secs(seconds);
    while let Some(deadline) = source.next_deadline() {
        if deadline > end {
            break;
        }
        model.apply(source.advance(deadline))?;
    }
    model.apply(source.advance(end))?;
    let [working, waiting, approval, idle] = model.state_counts();
    println!(
        "scenario={} seconds={} speed=x{} agents={} working={} waiting_input={} pending_approval={} idle={} owed={}",
        source.scenario().name(),
        seconds,
        source.speed(),
        model.agents.len(),
        working,
        waiting,
        approval,
        idle,
        model.waiting_count(),
    );
    Ok(())
}

fn run_tui(scenario: Scenario) -> Result<()> {
    let started = Instant::now();
    let mut source = Simulator::new(scenario);
    let mut model = Model::new();
    model.apply(source.advance(Duration::ZERO))?;
    model.add_shell(SHELL_ID, 1, b"starting local shell...\r\n")?;
    model.focus = model.first_on_current_desktop();
    let (mut shell, mut shell_events) = Shell::spawn(80, 24)?;
    let (terminal_sender, terminal_events) = unbounded();
    thread::spawn(move || {
        while let Ok(event) = event::read() {
            if terminal_sender.send(event).is_err() {
                break;
            }
        }
    });

    let mut stdout = io::stdout();
    enable_raw_mode()?;
    execute!(stdout, EnterAlternateScreen, EnableBracketedPaste)?;
    let _guard = TerminalGuard;
    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let log = SwitchLog::open_default()?;
    let mut app = App::new(model, log);
    let mut pending_input: Option<Instant> = None;
    let mut latencies = Vec::new();

    loop {
        let now = started.elapsed();
        let events = source.advance(now);
        app.apply_events(events, &source)?;
        if app.dirty {
            let size: Rect = terminal.size()?.into();
            app.ensure_sizes(size, &mut source, &shell)?;
            let scenario = source.scenario();
            let speed = source.speed();
            terminal.draw(|frame| app.draw(frame, now, scenario, speed))?;
            app.dirty = false;
            if let Some(key_at) = pending_input.take() {
                latencies.push(key_at.elapsed());
            }
        }
        if app.quit {
            break;
        }

        let now = started.elapsed();
        let deadline = match (source.next_deadline(), app.model.next_age_deadline(now)) {
            (Some(source), Some(age)) => Some(source.min(age)),
            (Some(source), None) => Some(source),
            (None, Some(age)) => Some(age),
            (None, None) => None,
        };
        let timeout = deadline.map(|deadline| deadline.saturating_sub(now));
        let mut selection = Select::new();
        let terminal_index = selection.recv(&terminal_events);
        let shell_index = selection.recv(&shell_events);
        let operation = match timeout {
            Some(timeout) => match selection.select_timeout(timeout) {
                Ok(operation) => operation,
                Err(_) => {
                    app.dirty = true;
                    continue;
                }
            },
            None => selection.select(),
        };
        if operation.index() == terminal_index {
            match operation.recv(&terminal_events) {
                Ok(TerminalEvent::Key(key)) if key.kind != KeyEventKind::Release => {
                    let key_at = Instant::now();
                    app.handle_key(key, started.elapsed(), &mut source, &mut shell)?;
                    pending_input = app.dirty.then_some(key_at);
                }
                Ok(TerminalEvent::Paste(text)) => {
                    let key_at = Instant::now();
                    app.handle_paste(&text, started.elapsed(), &mut source, &mut shell)?;
                    pending_input = app.dirty.then_some(key_at);
                }
                Ok(TerminalEvent::Resize(_, _)) => app.dirty = true,
                Ok(_) => {}
                Err(_) => break,
            }
        } else if operation.index() == shell_index {
            match operation.recv(&shell_events) {
                Ok(ShellOutput::Bytes(bytes)) => {
                    let responses = {
                        let agent = app.model.agent_mut(SHELL_ID);
                        agent.terminal.vt_write(&bytes);
                        agent.take_pty_responses()
                    };
                    for response in responses {
                        shell.write(&response)?;
                    }
                    app.dirty_terminals.insert(SHELL_ID);
                    app.dirty = true;
                }
                Ok(ShellOutput::Closed) | Err(_) => {
                    app.model
                        .agent_mut(SHELL_ID)
                        .terminal
                        .vt_write(b"\r\n[shell exited]\r\n");
                    app.dirty_terminals.insert(SHELL_ID);
                    app.dirty = true;
                    shell_events = never();
                }
            }
        }
    }
    drop(terminal);
    drop(_guard);
    println!("switch log: {}", app.log.path().display());
    if !latencies.is_empty() {
        latencies.sort_unstable();
        let median = latencies[latencies.len() / 2];
        println!(
            "keystroke-to-paint median: {:.3} ms ({} painted keystrokes)",
            median.as_secs_f64() * 1_000.0,
            latencies.len()
        );
    }
    Ok(())
}

struct TerminalGuard;

impl Drop for TerminalGuard {
    fn drop(&mut self) {
        let _ = disable_raw_mode();
        let _ = execute!(io::stdout(), DisableBracketedPaste, LeaveAlternateScreen);
    }
}

#[derive(Clone, Copy, Eq, PartialEq)]
enum PendingKey {
    None,
    Leader,
    Move,
}

enum Popup {
    None,
    Switcher { query: String, selected: usize },
    Scenario { selected: usize },
    Help,
}

struct App {
    model: Model,
    log: SwitchLog,
    renderers: HashMap<AgentId, VtRenderer>,
    dirty_terminals: HashSet<AgentId>,
    popup: Popup,
    pending: PendingKey,
    dirty: bool,
    quit: bool,
}

impl App {
    fn new(model: Model, log: SwitchLog) -> Self {
        let dirty_terminals = model.agents.iter().map(|agent| agent.id).collect();
        Self {
            model,
            log,
            renderers: HashMap::new(),
            dirty_terminals,
            popup: Popup::None,
            pending: PendingKey::None,
            dirty: true,
            quit: false,
        }
    }

    fn apply_events(&mut self, events: Vec<SourceEvent>, source: &impl Source) -> Result<()> {
        for event in &events {
            match event {
                SourceEvent::AgentAdded { id, .. }
                | SourceEvent::Output { id, .. }
                | SourceEvent::Resized { id, .. } => {
                    self.dirty_terminals.insert(*id);
                }
                SourceEvent::StateChanged { .. } | SourceEvent::TurnSettled { .. } => {}
            }
        }
        let applied = self.model.apply(events)?;
        if applied.dirty {
            self.dirty = true;
        }
        if applied.focused_turn_settled {
            self.auto_next(source)?;
        }
        Ok(())
    }

    fn ensure_sizes(
        &mut self,
        screen: Rect,
        source: &mut impl Source,
        shell: &Shell,
    ) -> Result<()> {
        let main = Rect::new(
            screen.x,
            screen.y,
            screen.width,
            screen.height.saturating_sub(1),
        );
        let ids = self.model.desktop_agents();
        let panes = tile_layout(main, ids.len());
        for (id, pane) in ids.into_iter().zip(panes) {
            let inner = pane.inner(Margin::new(1, 1));
            let cols = inner.width.max(1);
            let rows = inner.height.max(1);
            let agent = self.model.agent(id);
            if agent.cols == cols && agent.rows == rows {
                continue;
            }
            if agent.kind == AgentKind::Shell {
                shell.resize(cols, rows)?;
                self.model.resize_terminal(id, cols, rows)?;
                self.dirty_terminals.insert(id);
            } else {
                let events = source.command(Duration::ZERO, Command::Resize { id, cols, rows });
                self.apply_events(events, &*source)?;
            }
        }
        Ok(())
    }

    fn draw(&mut self, frame: &mut Frame<'_>, now: Duration, scenario: Scenario, speed: u8) {
        let screen = frame.area();
        let main = Rect::new(
            screen.x,
            screen.y,
            screen.width,
            screen.height.saturating_sub(1),
        );
        let ids = self.model.desktop_agents();
        let panes = tile_layout(main, ids.len());
        for (id, area) in ids.iter().copied().zip(panes) {
            let refresh = self.dirty_terminals.remove(&id);
            let agent = self.model.agent(id);
            let focused = self.model.focus == Some(id);
            let age = agent
                .turn_opened_at
                .map(|opened| format!(" {}", format_age(now.saturating_sub(opened))))
                .unwrap_or_default();
            let border = if focused {
                Color::Cyan
            } else if agent.state == AgentState::PendingApproval {
                Color::Red
            } else if agent.state.owes_turn() {
                Color::Yellow
            } else {
                Color::DarkGray
            };
            let title = format!(" {} · {}{} ", agent.name, agent.state.label(), age);
            let block = Block::new()
                .borders(Borders::ALL)
                .title(title)
                .border_style(Style::new().fg(border));
            let inner = block.inner(area);
            frame.render_widget(block, area);
            let renderer = self
                .renderers
                .entry(id)
                .or_insert_with(|| VtRenderer::new().unwrap());
            if let Err(error) = renderer.render(frame, &agent.terminal, inner, focused, refresh) {
                frame.render_widget(Paragraph::new(error.to_string()), inner);
            }
        }
        if ids.is_empty() {
            frame.render_widget(
                Paragraph::new(format!("desktop {} is empty", self.model.current_desktop))
                    .style(Style::new().fg(Color::DarkGray)),
                main,
            );
        }
        if screen.height > 0 {
            self.draw_bar(
                frame,
                Rect::new(screen.x, screen.bottom() - 1, screen.width, 1),
                scenario,
                speed,
            );
        }
        match &self.popup {
            Popup::None => {}
            Popup::Switcher { query, selected } => self.draw_switcher(frame, now, query, *selected),
            Popup::Scenario { selected } => self.draw_scenario(frame, *selected, scenario, speed),
            Popup::Help => self.draw_help(frame),
        }
    }

    fn draw_bar(&self, frame: &mut Frame<'_>, area: Rect, scenario: Scenario, speed: u8) {
        let queue = self.model.queue();
        let mut shown = queue.len().min(3);
        let desktops = (1..=9)
            .map(|desktop| {
                let dots = "●".repeat(self.model.desktop_waiting_count(desktop));
                if desktop == self.model.current_desktop {
                    format!("[{desktop}{dots}]")
                } else {
                    format!("{desktop}{dots}")
                }
            })
            .collect::<Vec<_>>()
            .join(" ");
        let focus = self
            .model
            .focused()
            .map(|agent| format!("{} · {}", agent.name, agent.state.label()))
            .unwrap_or_else(|| "no pane".to_string());
        let leader = match self.pending {
            PendingKey::None => "^space",
            PendingKey::Leader => "^space …",
            PendingKey::Move => "^space m …",
        };
        let text = loop {
            let names = queue
                .iter()
                .take(shown)
                .map(|id| self.model.agent(*id).name.as_str())
                .collect::<Vec<_>>()
                .join(" ▸ ");
            let hidden = queue.len().saturating_sub(shown);
            let queue_part = match (names.is_empty(), hidden) {
                (true, 0) => "0 waiting".to_string(),
                (true, hidden) => format!("{} waiting ▸ +{hidden}", queue.len()),
                (false, 0) => format!("{} waiting ▸ {names}", queue.len()),
                (false, hidden) => format!("{} waiting ▸ {names} ▸ +{hidden}", queue.len()),
            };
            let core = format!(" {queue_part}   {desktops}   {focus}   {leader} ");
            let contextual = format!(
                " {queue_part}   {desktops}   {focus}   {} · x{speed}   {leader} ",
                scenario.name()
            );
            if contextual.chars().count() <= area.width as usize {
                break contextual;
            }
            if core.chars().count() <= area.width as usize || shown == 0 {
                break core;
            }
            shown -= 1;
        };
        frame.render_widget(
            Paragraph::new(text).style(Style::new().fg(Color::Black).bg(Color::Gray)),
            area,
        );
    }

    fn draw_switcher(&self, frame: &mut Frame<'_>, now: Duration, query: &str, selected: usize) {
        let matches = filter_agents(&self.model.agents, query);
        let area = centered(frame.area(), 72, (matches.len() as u16 + 3).min(18));
        let visible = area.height.saturating_sub(2) as usize;
        let start = selected
            .saturating_sub(visible.saturating_sub(1))
            .min(matches.len().saturating_sub(visible));
        let lines = matches
            .iter()
            .enumerate()
            .skip(start)
            .take(visible)
            .map(|(index, id)| {
                let agent = self.model.agent(*id);
                let age = agent
                    .turn_opened_at
                    .map(|opened| format_age(now.saturating_sub(opened)))
                    .unwrap_or_else(|| "-".to_string());
                Line::styled(
                    format!(
                        "{} {:<24} d{}  {:<10} {}",
                        if index == selected { "›" } else { " " },
                        agent.name,
                        agent.desktop,
                        agent.state.label(),
                        age
                    ),
                    if index == selected {
                        Style::new().fg(Color::Black).bg(Color::Cyan)
                    } else {
                        Style::new()
                    },
                )
            })
            .collect::<Vec<_>>();
        frame.render_widget(Clear, area);
        frame.render_widget(
            Paragraph::new(lines)
                .block(Block::bordered().title(format!(" switcher /{query} ")))
                .wrap(Wrap { trim: false }),
            area,
        );
    }

    fn draw_scenario(&self, frame: &mut Frame<'_>, selected: usize, scenario: Scenario, speed: u8) {
        let items = scenario_items(speed);
        let lines = items
            .iter()
            .enumerate()
            .map(|(index, label)| {
                Line::styled(
                    format!("{} {label}", if selected == index { "›" } else { " " }),
                    if selected == index {
                        Style::new().fg(Color::Black).bg(Color::Cyan)
                    } else {
                        Style::new()
                    },
                )
            })
            .collect::<Vec<_>>();
        let area = centered(frame.area(), 44, items.len() as u16 + 2);
        frame.render_widget(Clear, area);
        frame.render_widget(
            Paragraph::new(lines).block(
                Block::bordered().title(format!(" scenario · {} · x{speed} ", scenario.name())),
            ),
            area,
        );
    }

    fn draw_help(&self, frame: &mut Frame<'_>) {
        let help = vec![
            Line::from("Ctrl-Space, then…"),
            Line::from("n queue   1-9 desktop   m 1-9 move pane"),
            Line::from("space or / switcher   h j k l / arrows focus"),
            Line::from("s scenario   ? help   q quit"),
            Line::from(""),
            Line::from("In a waiting pane, type and press Enter."),
            Line::from("In an approval pane, press y or n."),
            Line::from("The shell pane receives ordinary keys directly."),
            Line::from("Alt chords mirror leader chords when delivered."),
            Line::from(""),
            Line::styled(
                "Esc closes this popup",
                Style::new().add_modifier(Modifier::DIM),
            ),
        ];
        let area = centered(frame.area(), 68, help.len() as u16 + 2);
        frame.render_widget(Clear, area);
        frame.render_widget(
            Paragraph::new(help).block(Block::bordered().title(" keys ")),
            area,
        );
    }

    fn handle_key(
        &mut self,
        key: KeyEvent,
        now: Duration,
        source: &mut impl Source,
        shell: &mut Shell,
    ) -> Result<()> {
        match &mut self.popup {
            Popup::Switcher { query, selected } => {
                match key.code {
                    KeyCode::Esc => self.popup = Popup::None,
                    KeyCode::Up => *selected = selected.saturating_sub(1),
                    KeyCode::Down => {
                        let count = filter_agents(&self.model.agents, query).len();
                        *selected = (*selected + 1).min(count.saturating_sub(1));
                    }
                    KeyCode::Backspace => {
                        query.pop();
                        *selected = 0;
                    }
                    KeyCode::Enter => {
                        let target = filter_agents(&self.model.agents, query)
                            .get(*selected)
                            .copied();
                        self.popup = Popup::None;
                        if let Some(target) = target {
                            self.focus(Some(target), SwitchPath::Switcher, source)?;
                        }
                    }
                    KeyCode::Char(character) if !key.modifiers.contains(KeyModifiers::CONTROL) => {
                        query.push(character);
                        *selected = 0;
                    }
                    _ => {}
                }
                self.dirty = true;
                return Ok(());
            }
            Popup::Scenario { selected } => {
                match key.code {
                    KeyCode::Esc => self.popup = Popup::None,
                    KeyCode::Up => *selected = selected.saturating_sub(1),
                    KeyCode::Down => {
                        *selected = (*selected + 1).min(scenario_items(source.speed()).len() - 1)
                    }
                    KeyCode::Enter => {
                        let action = scenario_action(*selected, self.model.current_desktop);
                        let events = source.command(now, Command::Scenario(action));
                        self.apply_events(events, &*source)?;
                        if matches!(action, ScenarioAction::AddAgent { .. })
                            && self.model.focus.is_none()
                        {
                            let target = self.model.first_on_current_desktop();
                            self.focus(target, SwitchPath::PaneMove, source)?;
                        }
                        if action != ScenarioAction::ToggleSpeed {
                            self.popup = Popup::None;
                        }
                    }
                    _ => {}
                }
                self.dirty = true;
                return Ok(());
            }
            Popup::Help => {
                if key.code == KeyCode::Esc || key.code == KeyCode::Char('?') {
                    self.popup = Popup::None;
                }
                self.dirty = true;
                return Ok(());
            }
            Popup::None => {}
        }

        if is_leader(key) {
            self.pending = PendingKey::Leader;
            self.dirty = true;
            return Ok(());
        }
        if self.pending == PendingKey::Move {
            self.pending = PendingKey::None;
            if let Some(desktop) = digit(key.code) {
                self.model.move_focused(desktop);
            }
            self.dirty = true;
            return Ok(());
        }
        if self.pending == PendingKey::Leader {
            self.pending = PendingKey::None;
            self.handle_leader(key.code, source)?;
            self.dirty = true;
            return Ok(());
        }
        let focused_shell = self
            .model
            .focused()
            .is_some_and(|agent| agent.kind == AgentKind::Shell);
        if key.modifiers.contains(KeyModifiers::ALT) && !focused_shell {
            self.handle_leader(key.code, source)?;
            self.dirty = true;
            return Ok(());
        }
        if key.code == KeyCode::Esc && !focused_shell {
            self.pending = PendingKey::None;
            self.dirty = true;
            return Ok(());
        }

        let Some(id) = self.model.focus else {
            return Ok(());
        };
        let Some(bytes) = shell_key_bytes(key) else {
            return Ok(());
        };
        if self.model.agent(id).kind == AgentKind::Shell {
            shell.write(&bytes)?;
        } else {
            let events = source.command(now, Command::Input { id, bytes });
            self.apply_events(events, &*source)?;
        }
        Ok(())
    }

    fn handle_paste(
        &mut self,
        text: &str,
        now: Duration,
        source: &mut impl Source,
        shell: &mut Shell,
    ) -> Result<()> {
        let Some(id) = self.model.focus else {
            return Ok(());
        };
        if self.model.agent(id).kind == AgentKind::Shell {
            shell.write(format!("\x1b[200~{text}\x1b[201~").as_bytes())?;
        } else {
            let bytes = text.as_bytes().to_vec();
            let events = source.command(now, Command::Input { id, bytes });
            self.apply_events(events, &*source)?;
        }
        Ok(())
    }

    fn handle_leader(&mut self, code: KeyCode, source: &impl Source) -> Result<()> {
        match code {
            KeyCode::Char('n') => {
                let target = self.model.queue().first().copied();
                if target.is_some() {
                    self.focus(target, SwitchPath::Queue, source)?;
                }
            }
            KeyCode::Char('m') => self.pending = PendingKey::Move,
            KeyCode::Char(' ' | '/') => {
                self.popup = Popup::Switcher {
                    query: String::new(),
                    selected: 0,
                }
            }
            KeyCode::Char('s') => self.popup = Popup::Scenario { selected: 0 },
            KeyCode::Char('?') => self.popup = Popup::Help,
            KeyCode::Char('q') => self.quit = true,
            KeyCode::Left | KeyCode::Char('h') => self.focus_neighbor(Direction::Left, source)?,
            KeyCode::Down | KeyCode::Char('j') => self.focus_neighbor(Direction::Down, source)?,
            KeyCode::Up | KeyCode::Char('k') => self.focus_neighbor(Direction::Up, source)?,
            KeyCode::Right | KeyCode::Char('l') => self.focus_neighbor(Direction::Right, source)?,
            code => {
                if let Some(desktop) = digit(code) {
                    let record = self
                        .model
                        .go_desktop(desktop, source.scenario().name(), timestamp_ms())
                        .cloned();
                    self.append_record(record)?;
                }
            }
        }
        Ok(())
    }

    fn focus_neighbor(&mut self, direction: Direction, source: &impl Source) -> Result<()> {
        let ids = self.model.desktop_agents();
        let Some(current) = self
            .model
            .focus
            .and_then(|focus| ids.iter().position(|id| *id == focus))
        else {
            return Ok(());
        };
        let rects = tile_layout(Rect::new(0, 0, 120, 40), ids.len());
        if let Some(index) = neighbor(&rects, current, direction) {
            self.focus(Some(ids[index]), SwitchPath::PaneMove, source)?;
        }
        Ok(())
    }

    fn auto_next(&mut self, source: &impl Source) -> Result<()> {
        if let Some(next) = self.model.queue().first().copied() {
            self.focus(Some(next), SwitchPath::AutoNext, source)?;
        }
        Ok(())
    }

    fn focus(
        &mut self,
        target: Option<AgentId>,
        path: SwitchPath,
        source: &impl Source,
    ) -> Result<()> {
        let record = self
            .model
            .switch_focus(target, path, source.scenario().name(), timestamp_ms())
            .cloned();
        self.append_record(record)
    }

    fn append_record(&mut self, record: Option<zero::switch_log::SwitchRecord>) -> Result<()> {
        if let Some(record) = record {
            self.log.append(&record)?;
        }
        Ok(())
    }
}

fn is_leader(key: KeyEvent) -> bool {
    key.modifiers.contains(KeyModifiers::CONTROL)
        && matches!(key.code, LEADER_KEY | KeyCode::Char(' '))
}

fn digit(code: KeyCode) -> Option<u8> {
    match code {
        KeyCode::Char(character @ '1'..='9') => Some(character as u8 - b'0'),
        _ => None,
    }
}

fn shell_key_bytes(key: KeyEvent) -> Option<Vec<u8>> {
    let mut bytes = Vec::new();
    if key.modifiers.contains(KeyModifiers::ALT) {
        bytes.push(0x1b);
    }
    match key.code {
        KeyCode::Char(character) if key.modifiers.contains(KeyModifiers::CONTROL) => {
            let lower = character.to_ascii_lowercase() as u8;
            if lower.is_ascii_lowercase() {
                bytes.push(lower - b'a' + 1);
            }
        }
        KeyCode::Char(character) => {
            let mut buffer = [0; 4];
            bytes.extend_from_slice(character.encode_utf8(&mut buffer).as_bytes());
        }
        KeyCode::Enter => bytes.push(b'\r'),
        KeyCode::Backspace => bytes.push(0x7f),
        KeyCode::Tab => bytes.push(b'\t'),
        KeyCode::BackTab => bytes.extend_from_slice(b"\x1b[Z"),
        KeyCode::Esc => bytes.push(0x1b),
        KeyCode::Up => navigation_sequence(&mut bytes, 'A', key.modifiers),
        KeyCode::Down => navigation_sequence(&mut bytes, 'B', key.modifiers),
        KeyCode::Right => navigation_sequence(&mut bytes, 'C', key.modifiers),
        KeyCode::Left => navigation_sequence(&mut bytes, 'D', key.modifiers),
        KeyCode::Home => navigation_sequence(&mut bytes, 'H', key.modifiers),
        KeyCode::End => navigation_sequence(&mut bytes, 'F', key.modifiers),
        KeyCode::Insert => bytes.extend_from_slice(b"\x1b[2~"),
        KeyCode::Delete => bytes.extend_from_slice(b"\x1b[3~"),
        KeyCode::PageUp => bytes.extend_from_slice(b"\x1b[5~"),
        KeyCode::PageDown => bytes.extend_from_slice(b"\x1b[6~"),
        KeyCode::F(number) => function_key_sequence(&mut bytes, number)?,
        _ => return None,
    }
    Some(bytes)
}

fn navigation_sequence(bytes: &mut Vec<u8>, code: char, modifiers: KeyModifiers) {
    bytes.clear();
    let parameter = 1
        + u8::from(modifiers.contains(KeyModifiers::SHIFT))
        + 2 * u8::from(modifiers.contains(KeyModifiers::ALT))
        + 4 * u8::from(modifiers.contains(KeyModifiers::CONTROL));
    if parameter == 1 {
        bytes.extend_from_slice(format!("\x1b[{code}").as_bytes());
    } else {
        bytes.extend_from_slice(format!("\x1b[1;{parameter}{code}").as_bytes());
    }
}

fn function_key_sequence(bytes: &mut Vec<u8>, number: u8) -> Option<()> {
    bytes.clear();
    match number {
        1..=4 => bytes.extend_from_slice(&[0x1b, b'O', b'P' + number - 1]),
        5..=12 => {
            let code = [15, 17, 18, 19, 20, 21, 23, 24][number as usize - 5];
            bytes.extend_from_slice(format!("\x1b[{code}~").as_bytes());
        }
        _ => return None,
    }
    Some(())
}

fn scenario_items(speed: u8) -> [String; 7] {
    [
        "calm".to_string(),
        "busy-morning".to_string(),
        "all-busy".to_string(),
        format!("speed x{speed} → x{}", if speed == 1 { 5 } else { 1 }),
        "add agent".to_string(),
        "finish one now".to_string(),
        "make three wait now".to_string(),
    ]
}

fn scenario_action(index: usize, desktop: u8) -> ScenarioAction {
    match index {
        0 => ScenarioAction::Set(Scenario::Calm),
        1 => ScenarioAction::Set(Scenario::BusyMorning),
        2 => ScenarioAction::Set(Scenario::AllBusy),
        3 => ScenarioAction::ToggleSpeed,
        4 => ScenarioAction::AddAgent { desktop },
        5 => ScenarioAction::FinishOneNow,
        _ => ScenarioAction::MakeThreeWaitNow,
    }
}

fn centered(area: Rect, width: u16, height: u16) -> Rect {
    let width = width.min(area.width.saturating_sub(2));
    let height = height.min(area.height.saturating_sub(2));
    Rect::new(
        area.x + area.width.saturating_sub(width) / 2,
        area.y + area.height.saturating_sub(height) / 2,
        width,
        height,
    )
}

fn format_age(duration: Duration) -> String {
    let seconds = duration.as_secs();
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
    use std::fs;

    #[test]
    fn auto_next_focuses_and_records_the_oldest_turn() {
        let mut model = Model::new();
        model
            .apply(vec![
                SourceEvent::AgentAdded {
                    id: 1,
                    name: "garden/claude".to_string(),
                    desktop: 1,
                    state: AgentState::Working,
                    output: Vec::new(),
                },
                SourceEvent::AgentAdded {
                    id: 2,
                    name: "hub/codex".to_string(),
                    desktop: 2,
                    state: AgentState::Working,
                    output: Vec::new(),
                },
                SourceEvent::StateChanged {
                    id: 2,
                    state: AgentState::WaitingInput,
                    at: Duration::from_secs(2),
                },
                SourceEvent::StateChanged {
                    id: 1,
                    state: AgentState::WaitingInput,
                    at: Duration::from_secs(4),
                },
            ])
            .unwrap();
        model.focus = Some(1);
        let path = std::env::temp_dir().join(format!(
            "attn-zero-auto-next-{}-{}.jsonl",
            std::process::id(),
            timestamp_ms()
        ));
        let log = SwitchLog::open(path.clone()).unwrap();
        let mut app = App::new(model, log);

        app.auto_next(&Simulator::new(Scenario::Calm)).unwrap();

        assert_eq!(app.model.focus, Some(2));
        assert_eq!(
            app.model.switches.last().unwrap().path,
            SwitchPath::AutoNext
        );
        drop(app);
        fs::remove_file(path).unwrap();
    }

    #[test]
    fn shell_receives_escape_alt_and_control_a() {
        assert_eq!(
            shell_key_bytes(KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE)),
            Some(vec![0x1b])
        );
        assert_eq!(
            shell_key_bytes(KeyEvent::new(KeyCode::Char('f'), KeyModifiers::ALT)),
            Some(b"\x1bf".to_vec())
        );
        let control_a = KeyEvent::new(KeyCode::Char('a'), KeyModifiers::CONTROL);
        assert_eq!(shell_key_bytes(control_a), Some(vec![0x01]));
        assert!(!is_leader(control_a));
        assert_eq!(
            shell_key_bytes(KeyEvent::new(
                KeyCode::Left,
                KeyModifiers::CONTROL | KeyModifiers::ALT
            )),
            Some(b"\x1b[1;7D".to_vec())
        );
        assert_eq!(
            shell_key_bytes(KeyEvent::new(KeyCode::F(5), KeyModifiers::NONE)),
            Some(b"\x1b[15~".to_vec())
        );
    }
}
