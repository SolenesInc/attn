use std::collections::VecDeque;
use std::sync::Arc;

use serde::{Deserialize, Serialize};

use crate::ghostty::{Terminal, TrackedRef};

const MAX_BLOCKS: usize = 200;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MarkerKind {
    PromptStart,
    InputStart,
    PreExec,
    CommandEnd,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Marker {
    pub kind: MarkerKind,
    pub command: Option<String>,
    pub exit_code: Option<i32>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct AttachBlock {
    pub id: u64,
    #[serde(default, skip_serializing_if = "is_false")]
    pub pending: bool,
    pub prompt_row: i32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_row: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_col: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output_start_row: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub end_row: Option<i32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub output_start_col: Option<i32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub end_col: Option<i32>,
    #[serde(default, skip_serializing_if = "is_false")]
    pub end_inclusive: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub command: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub exit_code: Option<i32>,
}

#[allow(clippy::trivially_copy_pass_by_ref)]
fn is_false(value: &bool) -> bool {
    !*value
}

struct TrackedBlock {
    id: u64,
    prompt: Option<Arc<TrackedRef>>,
    input: Option<Arc<TrackedRef>>,
    output: Option<Arc<TrackedRef>>,
    end: Option<Arc<TrackedRef>>,
    end_inclusive: bool,
    command: Option<String>,
    exit_code: Option<i32>,
    has_command: bool,
    alt_screen: bool,
}

pub struct BlockTable {
    completed: VecDeque<TrackedBlock>,
    pending: Option<TrackedBlock>,
    next_id: u64,
    integration_seen: bool,
    at_prompt: bool,
    last_completed_id: u64,
}

impl BlockTable {
    pub fn new() -> Self {
        Self {
            completed: VecDeque::new(),
            pending: None,
            next_id: 1,
            integration_seen: false,
            at_prompt: false,
            last_completed_id: 0,
        }
    }

    pub fn apply(&mut self, marker: Marker, terminal: &Terminal) {
        self.integration_seen = true;
        self.at_prompt = marker.kind != MarkerKind::PreExec;
        let current = terminal.track_cursor().map(Arc::new);
        if self.pending.as_ref().is_some_and(|block| block.has_command)
            && matches!(
                marker.kind,
                MarkerKind::PromptStart | MarkerKind::InputStart | MarkerKind::PreExec
            )
            && let Some(mut pending) = self.pending.take()
        {
            pending.end.clone_from(&current);
            pending.end_inclusive = terminal.cursor_pending_wrap();
            self.complete(pending);
        }

        match marker.kind {
            MarkerKind::PromptStart => {
                self.pending = Some(self.open(current, terminal.alt_screen_active()));
            }
            MarkerKind::InputStart => {
                if self.pending.is_none() {
                    self.pending = Some(self.open(current.clone(), terminal.alt_screen_active()));
                }
                if let Some(pending) = self.pending.as_mut() {
                    pending.input = current;
                }
            }
            MarkerKind::PreExec => {
                if self.pending.is_none() {
                    self.pending = Some(self.open(current.clone(), terminal.alt_screen_active()));
                }
                if let Some(pending) = self.pending.as_mut() {
                    pending.output = current;
                    pending.command = marker.command;
                    pending.has_command = true;
                }
            }
            MarkerKind::CommandEnd => {
                if let Some(mut pending) = self.pending.take() {
                    self.last_completed_id = pending.id;
                    if pending.has_command {
                        pending.end = current;
                        pending.end_inclusive = terminal.cursor_pending_wrap();
                        pending.exit_code = marker.exit_code;
                        self.complete(pending);
                    }
                }
            }
        }
    }

    pub fn restore(blocks: &[AttachBlock], next_id: u64, terminal: &Terminal) -> Self {
        let pin = |col: Option<i32>, row: Option<i32>| {
            let row = u32::try_from(row?).ok()?;
            let col = u16::try_from(col.unwrap_or(0)).ok()?;
            terminal.track_screen_point(col, row).map(Arc::new)
        };
        let mut table = Self {
            completed: VecDeque::new(),
            pending: None,
            next_id,
            integration_seen: !blocks.is_empty(),
            at_prompt: blocks
                .last()
                .is_some_and(|b| b.pending && b.command.is_none()),
            last_completed_id: 0,
        };
        for block in blocks {
            let tracked = TrackedBlock {
                id: block.id,
                prompt: pin(None, Some(block.prompt_row)),
                input: pin(block.input_col, block.input_row),
                output: pin(block.output_start_col, block.output_start_row),
                end: pin(block.end_col, block.end_row),
                end_inclusive: block.end_inclusive,
                command: block.command.clone(),
                exit_code: block.exit_code,
                has_command: block.command.is_some(),
                alt_screen: false,
            };
            if block.pending {
                table.pending = Some(tracked);
            } else {
                table.complete(tracked);
            }
        }
        table
    }

    pub fn next_id(&self) -> u64 {
        self.next_id
    }

    pub fn read_state(&self) -> serde_json::Value {
        serde_json::json!({"integration_seen": self.integration_seen, "at_prompt": self.at_prompt, "last_block_id": self.last_completed_id})
    }

    pub fn read_block(
        &self,
        terminal: &Terminal,
        cols: u16,
        after: Option<u64>,
    ) -> Result<serde_json::Value, String> {
        let block = match after {
            Some(id) => self.completed.iter().find(|b| b.id > id),
            None => self.completed.back(),
        }
        .ok_or_else(|| "no completed command block is available (OSC 133 command-start marks are required to read output)".to_owned())?;
        let start = point(block.output.as_ref());
        let end = point(block.end.as_ref());
        let (Some(start), Some(end)) = (start, end) else {
            return Err(format!(
                "command block {} output is no longer in scrollback",
                block.id
            ));
        };
        let output = terminal.text_range(cols, start, end, block.end_inclusive)?;
        Ok(
            serde_json::json!({"id": block.id, "command": block.command, "exit_code": block.exit_code, "output": output}),
        )
    }

    pub fn snapshot(&self) -> Vec<AttachBlock> {
        let mut blocks = self
            .completed
            .iter()
            .filter_map(|block| resolve(block, false))
            .collect::<Vec<_>>();
        if let Some(block) = self.pending.as_ref().and_then(|block| resolve(block, true)) {
            blocks.push(block);
        }
        blocks
    }

    fn open(&mut self, prompt: Option<Arc<TrackedRef>>, alt_screen: bool) -> TrackedBlock {
        let block = TrackedBlock {
            id: self.next_id,
            prompt,
            input: None,
            output: None,
            end: None,
            end_inclusive: false,
            command: None,
            exit_code: None,
            has_command: false,
            alt_screen,
        };
        self.next_id += 1;
        block
    }

    fn complete(&mut self, block: TrackedBlock) {
        self.last_completed_id = block.id;
        self.completed.push_back(block);
        while self.completed.len() > MAX_BLOCKS {
            self.completed.pop_front();
        }
    }
}

fn point(reference: Option<&Arc<TrackedRef>>) -> Option<(i32, i32)> {
    reference?
        .screen_point()
        .map(|(x, y)| (i32::from(x), i32::try_from(y).unwrap_or(i32::MAX)))
}

fn resolve(block: &TrackedBlock, pending: bool) -> Option<AttachBlock> {
    if block.alt_screen {
        return None;
    }
    let (_, prompt_row) = point(block.prompt.as_ref())?;
    let input = point(block.input.as_ref());
    let output_start_row = point(block.output.as_ref()).map(|(_, row)| row);
    let end_row = if pending {
        None
    } else {
        Some(point(block.end.as_ref())?.1)
    };
    Some(AttachBlock {
        id: block.id,
        pending,
        prompt_row,
        input_row: input.map(|(_, row)| row),
        input_col: input.map(|(col, _)| col),
        output_start_row,
        output_start_col: point(block.output.as_ref()).map(|(col, _)| col),
        end_col: point(block.end.as_ref()).map(|(col, _)| col),
        end_inclusive: block.end_inclusive,
        end_row,
        command: block
            .has_command
            .then(|| block.command.clone().unwrap_or_default()),
        exit_code: block.exit_code,
    })
}

pub fn parse_marker(payload: &str) -> Option<Marker> {
    let kind = payload.as_bytes().first().copied()?;
    match kind {
        b'A' => Some(marker(MarkerKind::PromptStart)),
        b'B' => Some(marker(MarkerKind::InputStart)),
        b'C' => {
            let mut command = None;
            for part in payload.get(2..).unwrap_or_default().split(';') {
                if let Some(value) = part.strip_prefix("cmdline_url=") {
                    command = percent_decode(value);
                } else if command.is_none() {
                    command = part.strip_prefix("cmdline=").map(str::to_owned);
                }
            }
            Some(Marker {
                kind: MarkerKind::PreExec,
                command,
                exit_code: None,
            })
        }
        b'D' => Some(Marker {
            kind: MarkerKind::CommandEnd,
            command: None,
            exit_code: parse_i32_prefix(payload.get(2..).unwrap_or_default()),
        }),
        _ => None,
    }
}

fn marker(kind: MarkerKind) -> Marker {
    Marker {
        kind,
        command: None,
        exit_code: None,
    }
}

fn percent_decode(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut result = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            let pair = bytes.get(index + 1..index + 3)?;
            let text = std::str::from_utf8(pair).ok()?;
            result.push(u8::from_str_radix(text, 16).ok()?);
            index += 3;
        } else {
            result.push(bytes[index]);
            index += 1;
        }
    }
    String::from_utf8(result).ok()
}

fn parse_i32_prefix(value: &str) -> Option<i32> {
    let trimmed = value.trim_start_matches(|c: char| c.is_ascii_whitespace());
    let mut end = 0;
    for (index, byte) in trimmed.bytes().enumerate() {
        if index == 0 && matches!(byte, b'+' | b'-') {
            end = 1;
            continue;
        }
        if !byte.is_ascii_digit() {
            break;
        }
        end = index + 1;
    }
    if end == 0 || matches!(&trimmed[..end], "+" | "-") {
        return None;
    }
    trimmed[..end].parse().ok()
}

#[cfg(test)]
mod tests {
    use super::{MarkerKind, parse_marker};

    #[test]
    fn parses_command_and_exit_markers() {
        let marker = parse_marker("C;cmdline_url=printf%20a%2Bb").expect("marker");
        assert_eq!(marker.kind, MarkerKind::PreExec);
        assert_eq!(marker.command.as_deref(), Some("printf a+b"));
        assert_eq!(
            parse_marker("D;-17;ignored").expect("marker").exit_code,
            Some(-17)
        );
    }
}
