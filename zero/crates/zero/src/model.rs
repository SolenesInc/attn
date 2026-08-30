use std::cell::RefCell;
use std::collections::{HashMap, VecDeque};
use std::rc::Rc;
use std::time::Duration;

use anyhow::Result;
use libghostty_vt::style::RgbColor;
use libghostty_vt::terminal::{
    ConformanceLevel, DeviceAttributeFeature, DeviceAttributes, DeviceType,
    PrimaryDeviceAttributes, SecondaryDeviceAttributes, SizeReportSize, Terminal,
};

use crate::source::Event;
use crate::switch_log::{SwitchPath, SwitchRecord};

pub type AgentId = u64;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AgentState {
    Working,
    WaitingInput,
    PendingApproval,
    Idle,
}

impl AgentState {
    pub fn label(self) -> &'static str {
        match self {
            Self::Working => "working",
            Self::WaitingInput => "waiting",
            Self::PendingApproval => "approval",
            Self::Idle => "idle",
        }
    }

    pub fn owes_turn(self) -> bool {
        matches!(self, Self::WaitingInput | Self::PendingApproval)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AgentKind {
    Simulated,
    Shell,
}

pub struct Agent {
    pub id: AgentId,
    pub name: String,
    pub desktop: u8,
    pub kind: AgentKind,
    pub state: AgentState,
    pub turn_opened_at: Option<Duration>,
    pub terminal: Terminal<'static, 'static>,
    pub cols: u16,
    pub rows: u16,
    pty_responses: Rc<RefCell<VecDeque<Vec<u8>>>>,
}

impl Agent {
    fn new(
        id: AgentId,
        name: String,
        desktop: u8,
        kind: AgentKind,
        state: AgentState,
        now: Duration,
        output: &[u8],
    ) -> Result<Self> {
        let responses = Rc::new(RefCell::new(VecDeque::new()));
        let mut terminal = Terminal::new(80, 24)?;
        if kind == AgentKind::Shell {
            let callback_responses = Rc::clone(&responses);
            terminal
                .set_default_fg_color(Some(RgbColor {
                    r: 0xd8,
                    g: 0xde,
                    b: 0xe9,
                }))?
                .set_default_bg_color(Some(RgbColor {
                    r: 0x1b,
                    g: 0x1d,
                    b: 0x23,
                }))?
                .set_default_cursor_color(Some(RgbColor {
                    r: 0xd8,
                    g: 0xde,
                    b: 0xe9,
                }))?
                .on_pty_write(move |_terminal, bytes| {
                    callback_responses.borrow_mut().push_back(bytes.to_vec());
                })?
                .on_size(|terminal| {
                    Some(SizeReportSize {
                        rows: terminal.rows().ok()?,
                        columns: terminal.cols().ok()?,
                        cell_width: 0,
                        cell_height: 0,
                    })
                })?
                .on_device_attributes(|_terminal| {
                    Some(DeviceAttributes {
                        primary: PrimaryDeviceAttributes::new(
                            ConformanceLevel::VT220,
                            &[
                                DeviceAttributeFeature::COLUMNS_132,
                                DeviceAttributeFeature::SELECTIVE_ERASE,
                                DeviceAttributeFeature::ANSI_COLOR,
                            ],
                        ),
                        secondary: SecondaryDeviceAttributes {
                            device_type: DeviceType::VT220,
                            firmware_version: 1,
                            rom_cartridge: 0,
                        },
                        tertiary: Default::default(),
                    })
                })?
                .on_xtversion(|_terminal| Some("attn-zero"))?;
        }
        terminal.vt_write(output);
        Ok(Self {
            id,
            name,
            desktop,
            kind,
            state,
            turn_opened_at: state.owes_turn().then_some(now),
            terminal,
            cols: 80,
            rows: 24,
            pty_responses: responses,
        })
    }

    pub fn take_pty_responses(&self) -> Vec<Vec<u8>> {
        self.pty_responses.borrow_mut().drain(..).collect()
    }
}

pub struct Model {
    pub agents: Vec<Agent>,
    pub current_desktop: u8,
    pub focus: Option<AgentId>,
    pub switches: Vec<SwitchRecord>,
    indexes: HashMap<AgentId, usize>,
}

impl Model {
    pub fn new() -> Self {
        Self {
            agents: Vec::new(),
            current_desktop: 1,
            focus: None,
            switches: Vec::new(),
            indexes: HashMap::new(),
        }
    }

    pub fn add_shell(&mut self, id: AgentId, desktop: u8, output: &[u8]) -> Result<()> {
        let agent = Agent::new(
            id,
            "shell".to_string(),
            desktop,
            AgentKind::Shell,
            AgentState::Idle,
            Duration::ZERO,
            output,
        )?;
        self.insert(agent);
        Ok(())
    }

    fn insert(&mut self, agent: Agent) {
        let id = agent.id;
        self.indexes.insert(id, self.agents.len());
        self.agents.push(agent);
    }

    pub fn first_on_current_desktop(&self) -> Option<AgentId> {
        self.agents
            .iter()
            .find(|agent| agent.desktop == self.current_desktop)
            .map(|agent| agent.id)
    }

    pub fn apply(&mut self, events: Vec<Event>) -> Result<bool> {
        let mut dirty = false;
        for event in events {
            dirty = true;
            match event {
                Event::AgentAdded {
                    id,
                    name,
                    desktop,
                    state,
                    output,
                } => {
                    let agent = Agent::new(
                        id,
                        name,
                        desktop,
                        AgentKind::Simulated,
                        state,
                        Duration::ZERO,
                        &output,
                    )?;
                    self.insert(agent);
                }
                Event::StateChanged { id, state, at } => {
                    let agent = self.agent_mut(id);
                    if state.owes_turn() && !agent.state.owes_turn() {
                        agent.turn_opened_at = Some(at);
                    } else if !state.owes_turn() {
                        agent.turn_opened_at = None;
                    }
                    agent.state = state;
                }
                Event::TurnSettled { id } => self.agent_mut(id).turn_opened_at = None,
                Event::Output { id, bytes } => self.agent_mut(id).terminal.vt_write(&bytes),
                Event::Resized { id, cols, rows } => self.resize_terminal(id, cols, rows)?,
            }
        }
        Ok(dirty)
    }

    pub fn agent(&self, id: AgentId) -> &Agent {
        &self.agents[self.indexes[&id]]
    }

    pub fn agent_mut(&mut self, id: AgentId) -> &mut Agent {
        let index = self.indexes[&id];
        &mut self.agents[index]
    }

    pub fn focused(&self) -> Option<&Agent> {
        self.focus.map(|id| self.agent(id))
    }

    pub fn focused_mut(&mut self) -> Option<&mut Agent> {
        let id = self.focus?;
        Some(self.agent_mut(id))
    }

    pub fn desktop_agents(&self) -> Vec<AgentId> {
        self.agents
            .iter()
            .filter(|agent| agent.desktop == self.current_desktop)
            .map(|agent| agent.id)
            .collect()
    }

    pub fn queue(&self) -> Vec<AgentId> {
        let mut waiting: Vec<_> = self
            .agents
            .iter()
            .filter_map(|agent| agent.turn_opened_at.map(|opened| (opened, agent.id)))
            .collect();
        waiting.sort_by_key(|(opened, id)| (*opened, *id));
        waiting.into_iter().map(|(_, id)| id).collect()
    }

    pub fn waiting_count(&self) -> usize {
        self.agents
            .iter()
            .filter(|agent| agent.turn_opened_at.is_some())
            .count()
    }

    pub fn desktop_waiting_count(&self, desktop: u8) -> usize {
        self.agents
            .iter()
            .filter(|agent| agent.desktop == desktop && agent.turn_opened_at.is_some())
            .count()
    }

    pub fn switch_focus(
        &mut self,
        to: Option<AgentId>,
        path: SwitchPath,
        scenario: &str,
        timestamp: u64,
    ) -> Option<&SwitchRecord> {
        let from = self.focus;
        if from == to {
            return None;
        }
        if let Some(id) = to {
            self.current_desktop = self.agent(id).desktop;
        }
        self.focus = to;
        let record = SwitchRecord {
            timestamp,
            from: from.map(|id| self.agent(id).name.clone()),
            to: to.map(|id| self.agent(id).name.clone()),
            path,
            scenario: scenario.to_string(),
            waiting_count: self.waiting_count(),
        };
        self.switches.push(record);
        self.switches.last()
    }

    pub fn go_desktop(
        &mut self,
        desktop: u8,
        scenario: &str,
        timestamp: u64,
    ) -> Option<&SwitchRecord> {
        self.current_desktop = desktop;
        let target = self
            .agents
            .iter()
            .find(|agent| agent.desktop == desktop)
            .map(|agent| agent.id);
        self.switch_focus(target, SwitchPath::Desktop, scenario, timestamp)
    }

    pub fn move_focused(&mut self, desktop: u8) {
        let Some(id) = self.focus else {
            return;
        };
        self.agent_mut(id).desktop = desktop;
        self.current_desktop = desktop;
    }

    pub fn resize_terminal(&mut self, id: AgentId, cols: u16, rows: u16) -> Result<()> {
        let agent = self.agent_mut(id);
        if agent.cols == cols && agent.rows == rows {
            return Ok(());
        }
        agent.terminal.resize(cols, rows, 0, 0)?;
        agent.cols = cols;
        agent.rows = rows;
        Ok(())
    }

    pub fn state_counts(&self) -> [usize; 4] {
        let mut counts = [0; 4];
        for agent in self
            .agents
            .iter()
            .filter(|agent| agent.kind == AgentKind::Simulated)
        {
            let index = match agent.state {
                AgentState::Working => 0,
                AgentState::WaitingInput => 1,
                AgentState::PendingApproval => 2,
                AgentState::Idle => 3,
            };
            counts[index] += 1;
        }
        counts
    }

    pub fn next_age_deadline(&self, now: Duration) -> Option<Duration> {
        self.agents
            .iter()
            .filter_map(|agent| agent.turn_opened_at)
            .map(|opened| opened + Duration::from_secs(now.saturating_sub(opened).as_secs() + 1))
            .min()
    }
}

impl Default for Model {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn add(model: &mut Model, id: AgentId, state: AgentState) {
        model
            .apply(vec![Event::AgentAdded {
                id,
                name: format!("agent/{id}"),
                desktop: 1,
                state,
                output: Vec::new(),
            }])
            .unwrap();
    }

    #[test]
    fn turn_opens_on_attention_state_and_closes_only_when_settled() {
        let mut model = Model::new();
        add(&mut model, 1, AgentState::Working);
        model
            .apply(vec![Event::StateChanged {
                id: 1,
                state: AgentState::WaitingInput,
                at: Duration::from_secs(7),
            }])
            .unwrap();
        assert_eq!(model.agent(1).turn_opened_at, Some(Duration::from_secs(7)));

        model
            .apply(vec![Event::StateChanged {
                id: 1,
                state: AgentState::PendingApproval,
                at: Duration::from_secs(9),
            }])
            .unwrap();
        assert_eq!(model.agent(1).turn_opened_at, Some(Duration::from_secs(7)));

        model.apply(vec![Event::TurnSettled { id: 1 }]).unwrap();
        assert_eq!(model.agent(1).turn_opened_at, None);
    }

    #[test]
    fn queue_is_oldest_turn_first() {
        let mut model = Model::new();
        add(&mut model, 1, AgentState::Working);
        add(&mut model, 2, AgentState::Working);
        model
            .apply(vec![
                Event::StateChanged {
                    id: 2,
                    state: AgentState::WaitingInput,
                    at: Duration::from_secs(2),
                },
                Event::StateChanged {
                    id: 1,
                    state: AgentState::PendingApproval,
                    at: Duration::from_secs(5),
                },
            ])
            .unwrap();
        assert_eq!(model.queue(), vec![2, 1]);
    }

    #[test]
    fn auto_next_uses_the_oldest_remaining_turn() {
        let mut model = Model::new();
        add(&mut model, 1, AgentState::WaitingInput);
        add(&mut model, 2, AgentState::Working);
        model
            .apply(vec![Event::StateChanged {
                id: 2,
                state: AgentState::WaitingInput,
                at: Duration::from_secs(3),
            }])
            .unwrap();
        model.focus = Some(1);
        model
            .apply(vec![
                Event::TurnSettled { id: 1 },
                Event::StateChanged {
                    id: 1,
                    state: AgentState::Working,
                    at: Duration::from_secs(4),
                },
            ])
            .unwrap();
        assert_eq!(model.queue().first(), Some(&2));
    }

    #[test]
    fn turn_age_deadline_is_the_next_visible_second() {
        let mut model = Model::new();
        add(&mut model, 1, AgentState::Working);
        model
            .apply(vec![Event::StateChanged {
                id: 1,
                state: AgentState::WaitingInput,
                at: Duration::from_millis(250),
            }])
            .unwrap();
        assert_eq!(
            model.next_age_deadline(Duration::from_millis(2_900)),
            Some(Duration::from_millis(3_250))
        );
    }

    #[test]
    fn the_first_agent_on_the_current_desktop_is_selectable() {
        let mut model = Model::new();
        model.current_desktop = 7;

        model
            .apply(vec![Event::AgentAdded {
                id: 1,
                name: "sandbox/codex".to_string(),
                desktop: 7,
                state: AgentState::Working,
                output: Vec::new(),
            }])
            .unwrap();

        assert_eq!(model.first_on_current_desktop(), Some(1));
        assert_eq!(model.focus, None);
    }
}
