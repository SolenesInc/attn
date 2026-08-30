use std::time::Duration;

use rand::rngs::StdRng;
use rand::{Rng, SeedableRng};

use crate::model::{AgentId, AgentState};
use crate::source::{Command, Event, Scenario, ScenarioAction, Source};

const DEFAULT_SEED: u64 = 0xA77E_0000;

const AGENT_NAMES: [&str; 12] = [
    "garden/claude",
    "hub/codex",
    "docs/pi",
    "bus/claude",
    "queue/codex",
    "terminal/pi",
    "remote/claude",
    "crew/codex",
    "notebook/pi",
    "jobs/claude",
    "app/codex",
    "release/pi",
];

const WORK_OUTPUT: [&str; 8] = [
    "\x1b[38;5;75m→ Read\x1b[0m src/session.rs\r\n",
    "I found the state transition. The queue was keeping the old turn open.\r\n",
    "\x1b[38;5;75m→ Edit\x1b[0m src/session.rs\r\n",
    "\x1b[36m@@ -41,6 +41,8 @@\x1b[0m\r\n\x1b[31m-    settle(id);\x1b[0m\r\n\x1b[32m+    settle(id);\x1b[0m\r\n\x1b[32m+    publish(id);\x1b[0m\r\n",
    "The narrow path is covered. Checking the other entry point now.\r\n",
    "Wide-cell check: 東京 → 🙂\r\n",
    "\x1b[38;5;75m→ Bash\x1b[0m cargo test -p zero\r\n",
    "\x1b[32mtest result: ok. 14 passed; 0 failed\x1b[0m\r\n",
];

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
enum DueKind {
    Ack,
    Output,
    Finish,
    Wake,
}

/// Stands in for the hook round trip: attn learns about a prompt or an answer only once the
/// harness has reacted to it. A guess, not a measurement; take a receipt against real hooks for v1.
pub const INPUT_ACK: Duration = Duration::from_millis(150);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Submitted {
    Prompt,
    Answer(bool),
}

struct SimAgent {
    id: AgentId,
    name: String,
    desktop: u8,
    state: AgentState,
    finish_at: Option<Duration>,
    next_output_at: Option<Duration>,
    wake_at: Option<Duration>,
    output_cursor: usize,
    chunks_left: usize,
    line: Vec<u8>,
    submitted: Option<Submitted>,
    ack_at: Option<Duration>,
}

pub struct Simulator {
    scenario: Scenario,
    speed: u8,
    rng: StdRng,
    agents: Vec<SimAgent>,
    initial_events: Vec<Event>,
    next_id: AgentId,
}

impl Simulator {
    pub fn new(scenario: Scenario) -> Self {
        Self::seeded(scenario, DEFAULT_SEED)
    }

    pub fn seeded(scenario: Scenario, seed: u64) -> Self {
        let mut simulator = Self {
            scenario,
            speed: 1,
            rng: StdRng::seed_from_u64(seed),
            agents: AGENT_NAMES
                .iter()
                .enumerate()
                .map(|(index, name)| SimAgent {
                    id: index as AgentId + 1,
                    name: (*name).to_string(),
                    desktop: index as u8 % 4 + 1,
                    state: AgentState::Idle,
                    finish_at: None,
                    next_output_at: None,
                    wake_at: None,
                    output_cursor: 0,
                    chunks_left: 0,
                    line: Vec::new(),
                    submitted: None,
                    ack_at: None,
                })
                .collect(),
            initial_events: Vec::new(),
            next_id: AGENT_NAMES.len() as AgentId + 1,
        };

        for index in 0..simulator.agents.len() {
            let state = simulator.initial_state(index);
            simulator.set_state_without_events(index, state, Duration::ZERO);
            let agent = &simulator.agents[index];
            simulator.initial_events.push(Event::AgentAdded {
                id: agent.id,
                name: agent.name.clone(),
                desktop: agent.desktop,
                state,
                output: initial_output(state).as_bytes().to_vec(),
            });
        }
        simulator
    }

    fn initial_state(&self, index: usize) -> AgentState {
        match self.scenario {
            Scenario::Calm => match index {
                0 => AgentState::WaitingInput,
                1 => AgentState::PendingApproval,
                2..=5 => AgentState::Working,
                _ => AgentState::Idle,
            },
            Scenario::BusyMorning => match index {
                0..=4 => AgentState::WaitingInput,
                5 => AgentState::PendingApproval,
                6 => AgentState::Working,
                _ => AgentState::Idle,
            },
            Scenario::AllBusy => AgentState::Working,
        }
    }

    fn set_state_without_events(&mut self, index: usize, state: AgentState, now: Duration) {
        self.clear_deadlines(index);
        self.agents[index].state = state;
        match state {
            AgentState::Working => self.schedule_work(index, now),
            AgentState::Idle => self.schedule_wake(index, now),
            AgentState::WaitingInput | AgentState::PendingApproval => {}
        }
    }

    fn clear_deadlines(&mut self, index: usize) {
        let agent = &mut self.agents[index];
        agent.finish_at = None;
        agent.next_output_at = None;
        agent.wake_at = None;
        agent.chunks_left = 0;
        agent.submitted = None;
        agent.ack_at = None;
    }

    fn scaled(&self, duration: Duration) -> Duration {
        duration / u32::from(self.speed)
    }

    fn schedule_work(&mut self, index: usize, now: Duration) {
        let seconds = self.rng.random_range(8..=40);
        let duration = self.scaled(Duration::from_secs(seconds));
        let chunks = self.rng.random_range(4..=7);
        let cursor = self.rng.random_range(0..WORK_OUTPUT.len());
        let agent = &mut self.agents[index];
        agent.finish_at = Some(now + duration);
        agent.next_output_at = Some(now + duration / (chunks as u32 + 1));
        agent.wake_at = None;
        agent.output_cursor = cursor;
        agent.chunks_left = chunks;
    }

    fn schedule_wake(&mut self, index: usize, now: Duration) {
        let seconds = match self.scenario {
            Scenario::Calm => self.rng.random_range(45..=120),
            Scenario::BusyMorning => self.rng.random_range(15..=60),
            Scenario::AllBusy => self.rng.random_range(20..=60),
        };
        self.agents[index].wake_at = Some(now + self.scaled(Duration::from_secs(seconds)));
    }

    fn due_event(&self) -> Option<(Duration, usize, DueKind)> {
        self.agents
            .iter()
            .enumerate()
            .flat_map(|(index, agent)| {
                [
                    agent.ack_at.map(|at| (at, index, DueKind::Ack)),
                    agent.next_output_at.map(|at| (at, index, DueKind::Output)),
                    agent.finish_at.map(|at| (at, index, DueKind::Finish)),
                    agent.wake_at.map(|at| (at, index, DueKind::Wake)),
                ]
                .into_iter()
                .flatten()
            })
            .min_by_key(|(at, index, kind)| (*at, *index, *kind))
    }

    fn emit_output_chunk(&mut self, index: usize, at: Duration, events: &mut Vec<Event>) {
        let agent = &mut self.agents[index];
        let bytes = WORK_OUTPUT[agent.output_cursor % WORK_OUTPUT.len()]
            .as_bytes()
            .to_vec();
        agent.output_cursor += 1;
        agent.chunks_left -= 1;
        agent.next_output_at = if agent.chunks_left == 0 {
            None
        } else {
            let finish_at = agent
                .finish_at
                .expect("working agent has a finish deadline");
            Some(at + (finish_at - at) / (agent.chunks_left as u32 + 1))
        };
        events.push(Event::Output {
            id: agent.id,
            bytes,
        });
    }

    fn wake(&mut self, index: usize, at: Duration, events: &mut Vec<Event>) {
        let id = self.agents[index].id;
        self.agents[index].state = AgentState::Working;
        self.agents[index].wake_at = None;
        self.schedule_work(index, at);
        events.push(Event::StateChanged {
            id,
            state: AgentState::Working,
            at,
        });
        events.push(Event::Output {
            id,
            bytes: b"\r\n\x1b[2mAutomation started a new pass.\x1b[0m\r\n".to_vec(),
        });
    }

    fn finish(
        &mut self,
        index: usize,
        at: Duration,
        force_attention: bool,
        events: &mut Vec<Event>,
    ) {
        self.clear_deadlines(index);
        let owed = self
            .agents
            .iter()
            .filter(|agent| agent.state.owes_turn())
            .count();
        if self.scenario == Scenario::Calm && owed >= 2 && !force_attention {
            let id = self.agents[index].id;
            self.agents[index].state = AgentState::Idle;
            self.schedule_wake(index, at);
            events.push(Event::Output {
                id,
                bytes: b"\r\n\x1b[32mDone. No question this time.\x1b[0m\r\n".to_vec(),
            });
            events.push(Event::StateChanged {
                id,
                state: AgentState::Idle,
                at,
            });
            return;
        }

        let state = if self.rng.random_ratio(1, 4) {
            AgentState::PendingApproval
        } else {
            AgentState::WaitingInput
        };
        let agent = &mut self.agents[index];
        agent.state = state;
        let (bytes, state) = match state {
            AgentState::PendingApproval => (
                b"\r\n\x1b[33mApproval required: apply the generated change? [y/n]\x1b[0m\r\n"
                    .to_vec(),
                AgentState::PendingApproval,
            ),
            AgentState::WaitingInput => (
                b"\r\nI finished this pass. What should I do next?\r\n".to_vec(),
                AgentState::WaitingInput,
            ),
            _ => unreachable!(),
        };
        events.push(Event::Output {
            id: agent.id,
            bytes,
        });
        events.push(Event::StateChanged {
            id: agent.id,
            state,
            at,
        });
    }

    fn input(&mut self, id: AgentId, bytes: &[u8], now: Duration) -> Vec<Event> {
        let Some(index) = self.agents.iter().position(|agent| agent.id == id) else {
            return Vec::new();
        };
        // One keystroke is one chunk; escape sequences (arrows, function keys) mean nothing here.
        if bytes.first() == Some(&0x1b) {
            return Vec::new();
        }
        let agent = &mut self.agents[index];
        let mut echo = Vec::new();
        for &byte in bytes {
            let approval = agent.state == AgentState::PendingApproval;
            let prompts = matches!(agent.state, AgentState::WaitingInput | AgentState::Idle);
            match byte {
                _ if agent.submitted.is_some() => {}
                b'y' | b'n' if approval => {
                    echo.extend_from_slice(&[byte, b'\r', b'\n']);
                    agent.submitted = Some(Submitted::Answer(byte == b'y'));
                    agent.ack_at = Some(now + INPUT_ACK);
                }
                _ if approval => {}
                b'\r' | b'\n' => {
                    let blank = agent.line.iter().all(u8::is_ascii_whitespace);
                    agent.line.clear();
                    echo.extend_from_slice(b"\r\n");
                    if prompts && !blank {
                        agent.submitted = Some(Submitted::Prompt);
                        agent.ack_at = Some(now + INPUT_ACK);
                    }
                }
                0x7f | 0x08 => {
                    if pop_char(&mut agent.line) {
                        echo.extend_from_slice(b"\x08 \x08");
                    }
                }
                0x20.. => {
                    agent.line.push(byte);
                    echo.push(byte);
                }
                _ => {}
            }
        }
        if echo.is_empty() {
            Vec::new()
        } else {
            vec![Event::Output { id, bytes: echo }]
        }
    }

    fn ack(&mut self, index: usize, at: Duration, events: &mut Vec<Event>) {
        let agent = &mut self.agents[index];
        agent.ack_at = None;
        match agent.submitted.take() {
            Some(Submitted::Prompt) => self.prompt(index, at, events),
            Some(Submitted::Answer(approved)) => self.answer(index, approved, at, events),
            None => {}
        }
    }

    fn prompt(&mut self, index: usize, at: Duration, events: &mut Vec<Event>) {
        let state = self.agents[index].state;
        if !matches!(state, AgentState::WaitingInput | AgentState::Idle) {
            return;
        }
        let id = self.agents[index].id;
        self.agents[index].state = AgentState::Working;
        self.clear_deadlines(index);
        self.schedule_work(index, at);
        if state.owes_turn() {
            events.push(Event::TurnSettled { id });
        }
        events.push(Event::Output {
            id,
            bytes: b"\x1b[2mOn it.\x1b[0m\r\n".to_vec(),
        });
        events.push(Event::StateChanged {
            id,
            state: AgentState::Working,
            at,
        });
    }

    fn answer(&mut self, index: usize, approved: bool, at: Duration, events: &mut Vec<Event>) {
        if self.agents[index].state != AgentState::PendingApproval {
            return;
        }
        let id = self.agents[index].id;
        events.push(Event::TurnSettled { id });
        if approved {
            self.agents[index].state = AgentState::Working;
            self.schedule_work(index, at);
            events.push(Event::Output {
                id,
                bytes: b"\x1b[32mApproved. Applying the change now.\x1b[0m\r\n".to_vec(),
            });
            events.push(Event::StateChanged {
                id,
                state: AgentState::Working,
                at,
            });
        } else {
            self.agents[index].state = AgentState::WaitingInput;
            events.push(Event::StateChanged {
                id,
                state: AgentState::Working,
                at,
            });
            events.push(Event::Output {
                id,
                bytes: b"Okay, I won't apply it. What should I do instead?\r\n".to_vec(),
            });
            events.push(Event::StateChanged {
                id,
                state: AgentState::WaitingInput,
                at,
            });
        }
    }

    fn set_scenario(&mut self, scenario: Scenario, now: Duration) -> Vec<Event> {
        if self.scenario == scenario {
            return Vec::new();
        }
        self.scenario = scenario;
        let target_owed: usize = match scenario {
            Scenario::Calm => 2,
            Scenario::BusyMorning => 6,
            Scenario::AllBusy => 0,
        };
        let current_owed = self
            .agents
            .iter()
            .filter(|agent| agent.state.owes_turn())
            .count();
        let mut attention_to_add = target_owed.saturating_sub(current_owed);
        let mut workers_to_add = match scenario {
            Scenario::Calm => 4,
            Scenario::BusyMorning => 1,
            Scenario::AllBusy => usize::MAX,
        };
        let mut events = Vec::new();
        for index in 0..self.agents.len() {
            let previous = self.agents[index].state;
            let state = if previous.owes_turn() {
                previous
            } else if attention_to_add > 0 {
                attention_to_add -= 1;
                AgentState::WaitingInput
            } else if workers_to_add > 0 {
                workers_to_add -= 1;
                AgentState::Working
            } else {
                AgentState::Idle
            };
            let id = self.agents[index].id;
            self.set_state_without_events(index, state, now);
            if previous != state {
                events.push(Event::StateChanged { id, state, at: now });
            }
            events.push(Event::Output {
                id,
                bytes: format!("\r\n\x1b[2mScenario: {}\x1b[0m\r\n", scenario.name()).into_bytes(),
            });
        }
        events
    }

    fn toggle_speed(&mut self, now: Duration) {
        let old_speed = self.speed;
        self.speed = if self.speed == 1 { 5 } else { 1 };
        for agent in &mut self.agents {
            agent.finish_at = agent
                .finish_at
                .map(|at| rescale_deadline(at, now, old_speed, self.speed));
            agent.next_output_at = agent
                .next_output_at
                .map(|at| rescale_deadline(at, now, old_speed, self.speed));
            agent.wake_at = agent
                .wake_at
                .map(|at| rescale_deadline(at, now, old_speed, self.speed));
        }
    }

    fn add_agent(&mut self, desktop: u8, now: Duration) -> Vec<Event> {
        let id = self.next_id;
        self.next_id += 1;
        let mut agent = SimAgent {
            id,
            name: format!("sandbox-{id}/codex"),
            desktop,
            state: AgentState::Working,
            finish_at: None,
            next_output_at: None,
            wake_at: None,
            output_cursor: 0,
            chunks_left: 0,
            line: Vec::new(),
            submitted: None,
            ack_at: None,
        };
        let name = agent.name.clone();
        agent.output_cursor = self.rng.random_range(0..WORK_OUTPUT.len());
        self.agents.push(agent);
        let index = self.agents.len() - 1;
        self.schedule_work(index, now);
        vec![Event::AgentAdded {
            id,
            name,
            desktop,
            state: AgentState::Working,
            output: b"Synthetic agent added. Starting work.\r\n".to_vec(),
        }]
    }

    fn finish_one_now(&mut self, now: Duration) -> Vec<Event> {
        let Some(index) = self
            .agents
            .iter()
            .position(|agent| agent.state == AgentState::Working)
        else {
            return Vec::new();
        };
        let mut events = Vec::new();
        self.finish(index, now, true, &mut events);
        events
    }

    fn make_three_wait_now(&mut self, now: Duration) -> Vec<Event> {
        let indexes: Vec<_> = self
            .agents
            .iter()
            .enumerate()
            .filter(|(_, agent)| !agent.state.owes_turn())
            .take(3)
            .map(|(index, _)| index)
            .collect();
        let mut events = Vec::new();
        for index in indexes {
            let id = self.agents[index].id;
            self.clear_deadlines(index);
            self.agents[index].state = AgentState::WaitingInput;
            events.push(Event::Output {
                id,
                bytes: b"\r\nI need a decision before I can continue.\r\n".to_vec(),
            });
            events.push(Event::StateChanged {
                id,
                state: AgentState::WaitingInput,
                at: now,
            });
        }
        events
    }
}

impl Source for Simulator {
    fn scenario(&self) -> Scenario {
        self.scenario
    }

    fn speed(&self) -> u8 {
        self.speed
    }

    fn next_deadline(&self) -> Option<Duration> {
        if !self.initial_events.is_empty() {
            return Some(Duration::ZERO);
        }
        self.due_event().map(|(at, _, _)| at)
    }

    fn advance(&mut self, now: Duration) -> Vec<Event> {
        let mut events = std::mem::take(&mut self.initial_events);
        while let Some((at, index, kind)) = self.due_event() {
            if at > now {
                break;
            }
            match kind {
                DueKind::Ack => self.ack(index, at, &mut events),
                DueKind::Output => self.emit_output_chunk(index, at, &mut events),
                DueKind::Finish => self.finish(index, at, false, &mut events),
                DueKind::Wake => self.wake(index, at, &mut events),
            }
        }
        events
    }

    fn command(&mut self, now: Duration, command: Command) -> Vec<Event> {
        match command {
            Command::Input { id, bytes } => self.input(id, &bytes, now),
            Command::Resize { id, cols, rows } => vec![Event::Resized { id, cols, rows }],
            Command::Scenario(action) => match action {
                ScenarioAction::Set(scenario) => self.set_scenario(scenario, now),
                ScenarioAction::ToggleSpeed => {
                    self.toggle_speed(now);
                    Vec::new()
                }
                ScenarioAction::AddAgent { desktop } => self.add_agent(desktop, now),
                ScenarioAction::FinishOneNow => self.finish_one_now(now),
                ScenarioAction::MakeThreeWaitNow => self.make_three_wait_now(now),
            },
        }
    }
}

fn initial_output(state: AgentState) -> &'static str {
    match state {
        AgentState::Working => "Running the next synthetic task…\r\n",
        AgentState::WaitingInput => "I have a result ready. What should I do next?\r\n",
        AgentState::PendingApproval => {
            "\x1b[33mApproval required: apply the generated change? [y/n]\x1b[0m\r\n"
        }
        AgentState::Idle => "\x1b[2mIdle. Waiting for scheduled work.\x1b[0m\r\n",
    }
}

fn pop_char(line: &mut Vec<u8>) -> bool {
    let before = line.len();
    while let Some(byte) = line.pop() {
        if byte & 0xC0 != 0x80 {
            break;
        }
    }
    before > line.len()
}

fn rescale_deadline(deadline: Duration, now: Duration, old_speed: u8, new_speed: u8) -> Duration {
    now + deadline.saturating_sub(now) * u32::from(old_speed) / u32::from(new_speed)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn has_state(events: &[Event], id: AgentId, state: AgentState) -> bool {
        events.iter().any(|event| {
            matches!(event, Event::StateChanged { id: event_id, state: event_state, .. }
                if *event_id == id && *event_state == state)
        })
    }

    fn settled(events: &[Event], id: AgentId) -> bool {
        events
            .iter()
            .any(|event| matches!(event, Event::TurnSettled { id: settled } if *settled == id))
    }

    fn input(id: AgentId, bytes: &[u8]) -> Command {
        Command::Input {
            id,
            bytes: bytes.to_vec(),
        }
    }

    fn output(id: AgentId, bytes: &[u8]) -> Event {
        Event::Output {
            id,
            bytes: bytes.to_vec(),
        }
    }

    #[test]
    fn scenarios_seed_the_expected_attention_load() {
        let mut calm = Simulator::seeded(Scenario::Calm, 1);
        let calm_events = calm.advance(Duration::ZERO);
        assert_eq!(calm_events.len(), 12);
        assert_eq!(
            calm.agents
                .iter()
                .filter(|agent| agent.state.owes_turn())
                .count(),
            2
        );

        let busy = Simulator::seeded(Scenario::BusyMorning, 1);
        assert!(
            busy.agents
                .iter()
                .filter(|agent| agent.state.owes_turn())
                .count()
                >= 5
        );

        let all_busy = Simulator::seeded(Scenario::AllBusy, 1);
        assert!(
            all_busy
                .agents
                .iter()
                .all(|agent| agent.state == AgentState::Working)
        );
    }

    #[test]
    fn typed_text_echoes_and_enter_settles_only_after_the_ack() {
        let mut simulator = Simulator::seeded(Scenario::Calm, 2);
        let now = Duration::from_secs(3);
        simulator.advance(now);

        let events = simulator.command(now, input(1, b"check the parallel path"));
        assert_eq!(events, vec![output(1, b"check the parallel path")]);

        let events = simulator.command(now, input(1, b"\r"));
        assert_eq!(events, vec![output(1, b"\r\n")]);
        assert_eq!(simulator.agents[0].state, AgentState::WaitingInput);
        assert_eq!(simulator.agents[0].ack_at, Some(now + INPUT_ACK));

        let events = simulator.advance(now + INPUT_ACK);
        assert!(settled(&events, 1));
        assert!(has_state(&events, 1, AgentState::Working));
        assert_eq!(simulator.agents[0].state, AgentState::Working);
    }

    #[test]
    fn a_blank_line_and_escape_sequences_submit_nothing() {
        let mut simulator = Simulator::seeded(Scenario::Calm, 2);
        let now = Duration::from_secs(3);
        simulator.advance(now);

        assert!(simulator.command(now, input(1, b"\x1b[A")).is_empty());
        assert_eq!(
            simulator.command(now, input(1, b"  \r")),
            vec![output(1, b"  \r\n")]
        );
        assert_eq!(simulator.agents[0].ack_at, None);
        assert_eq!(simulator.agents[0].state, AgentState::WaitingInput);
    }

    #[test]
    fn backspace_removes_one_character_however_many_bytes_it_has() {
        let mut simulator = Simulator::seeded(Scenario::Calm, 2);
        let now = Duration::from_secs(3);
        simulator.advance(now);

        simulator.command(now, input(1, "h東".as_bytes()));
        let events = simulator.command(now, input(1, &[0x7f, 0x7f, 0x7f]));
        assert_eq!(events, vec![output(1, b"\x08 \x08\x08 \x08")]);
        assert!(simulator.agents[0].line.is_empty());
    }

    #[test]
    fn answering_y_settles_and_starts_work_after_the_ack() {
        let mut simulator = Simulator::seeded(Scenario::Calm, 3);
        let now = Duration::from_secs(5);
        simulator.advance(now);

        assert!(simulator.command(now, input(2, b"x")).is_empty());
        let events = simulator.command(now, input(2, b"y"));
        assert_eq!(events, vec![output(2, b"y\r\n")]);
        assert_eq!(simulator.agents[1].state, AgentState::PendingApproval);

        let events = simulator.advance(now + INPUT_ACK);
        assert!(settled(&events, 2));
        assert!(has_state(&events, 2, AgentState::Working));
        assert_eq!(simulator.agents[1].state, AgentState::Working);
    }

    #[test]
    fn answering_n_settles_then_opens_a_fresh_waiting_turn() {
        let mut simulator = Simulator::seeded(Scenario::Calm, 4);
        let now = Duration::from_secs(7);
        simulator.advance(now);

        simulator.command(now, input(2, b"n"));
        let events = simulator.advance(now + INPUT_ACK);
        assert!(settled(&events, 2));
        assert!(has_state(&events, 2, AgentState::WaitingInput));
        assert!(
            events
                .iter()
                .any(|event| matches!(event, Event::Output { bytes, .. }
            if String::from_utf8_lossy(bytes).contains("instead")))
        );
        assert_eq!(simulator.agents[1].state, AgentState::WaitingInput);
    }

    #[test]
    fn advancing_the_manual_clock_streams_and_finishes_work() {
        let mut simulator = Simulator::seeded(Scenario::AllBusy, 5);
        simulator.advance(Duration::ZERO);
        let first_deadline = simulator
            .next_deadline()
            .expect("a working agent has a deadline");
        let first_events = simulator.advance(first_deadline);
        assert!(
            first_events
                .iter()
                .any(|event| matches!(event, Event::Output { .. }))
        );

        let finish_events = simulator.advance(Duration::from_secs(41));
        assert!(finish_events.iter().any(|event| matches!(
            event,
            Event::StateChanged {
                state: AgentState::WaitingInput | AgentState::PendingApproval,
                ..
            }
        )));
        assert!(simulator.agents.iter().all(|agent| agent.state.owes_turn()));
    }

    #[test]
    fn speed_rescales_existing_deadlines() {
        let mut simulator = Simulator::seeded(Scenario::AllBusy, 6);
        simulator.advance(Duration::ZERO);
        let before = simulator.next_deadline().unwrap();
        simulator.command(
            Duration::ZERO,
            Command::Scenario(ScenarioAction::ToggleSpeed),
        );
        assert_eq!(simulator.speed(), 5);
        assert_eq!(simulator.next_deadline().unwrap(), before / 5);
    }

    #[test]
    fn scenario_knobs_add_and_interrupt_agents() {
        let mut simulator = Simulator::seeded(Scenario::AllBusy, 7);
        simulator.advance(Duration::ZERO);
        let added = simulator.command(
            Duration::from_secs(1),
            Command::Scenario(ScenarioAction::AddAgent { desktop: 4 }),
        );
        assert!(matches!(
            added.as_slice(),
            [Event::AgentAdded { desktop: 4, .. }]
        ));
        assert_eq!(simulator.agents.len(), 13);

        let waiting = simulator.command(
            Duration::from_secs(2),
            Command::Scenario(ScenarioAction::MakeThreeWaitNow),
        );
        assert_eq!(
            waiting
                .iter()
                .filter(|event| matches!(
                    event,
                    Event::StateChanged {
                        state: AgentState::WaitingInput,
                        ..
                    }
                ))
                .count(),
            3
        );
    }

    #[test]
    fn changing_scenario_never_settles_an_owed_turn() {
        let mut simulator = Simulator::seeded(Scenario::BusyMorning, 8);
        simulator.advance(Duration::ZERO);
        let owed_before: Vec<_> = simulator
            .agents
            .iter()
            .filter(|agent| agent.state.owes_turn())
            .map(|agent| agent.id)
            .collect();

        let events = simulator.command(
            Duration::from_secs(1),
            Command::Scenario(ScenarioAction::Set(Scenario::Calm)),
        );

        assert!(
            !events
                .iter()
                .any(|event| matches!(event, Event::TurnSettled { .. }))
        );
        assert!(owed_before.into_iter().all(|id| {
            simulator
                .agents
                .iter()
                .find(|agent| agent.id == id)
                .is_some_and(|agent| agent.state.owes_turn())
        }));
    }
}
