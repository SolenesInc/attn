use std::time::Duration;

use crate::model::{AgentId, AgentState};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Scenario {
    Calm,
    BusyMorning,
    AllBusy,
}

impl Scenario {
    pub const ALL: [Self; 3] = [Self::Calm, Self::BusyMorning, Self::AllBusy];

    pub fn name(self) -> &'static str {
        match self {
            Self::Calm => "calm",
            Self::BusyMorning => "busy-morning",
            Self::AllBusy => "all-busy",
        }
    }

    pub fn parse(value: &str) -> Option<Self> {
        Self::ALL
            .into_iter()
            .find(|scenario| scenario.name() == value)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ScenarioAction {
    Set(Scenario),
    ToggleSpeed,
    AddAgent { desktop: u8 },
    FinishOneNow,
    MakeThreeWaitNow,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Command {
    Prompt { id: AgentId, text: String },
    Approval { id: AgentId, approved: bool },
    Resize { id: AgentId, cols: u16, rows: u16 },
    Scenario(ScenarioAction),
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Event {
    AgentAdded {
        id: AgentId,
        name: String,
        desktop: u8,
        state: AgentState,
        output: Vec<u8>,
    },
    StateChanged {
        id: AgentId,
        state: AgentState,
        at: Duration,
    },
    TurnSettled {
        id: AgentId,
    },
    Output {
        id: AgentId,
        bytes: Vec<u8>,
    },
    Resized {
        id: AgentId,
        cols: u16,
        rows: u16,
    },
}

pub trait Source {
    fn scenario(&self) -> Scenario;
    fn speed(&self) -> u8;
    fn next_deadline(&self) -> Option<Duration>;
    fn advance(&mut self, now: Duration) -> Vec<Event>;
    fn command(&mut self, now: Duration, command: Command) -> Vec<Event>;
}
