//! The command blocks a shell marks with OSC 133, kept as the Tauri terminal's block store
//! keeps them (`terminalBlocks.ts`) and the daemon's worker does: one per command, from its
//! prompt to the row the cursor was on when it ended.

use crate::vt::{Mark, PromptStep};

/// The newest blocks kept, as the Tauri terminal keeps them.
pub const MAX_BLOCKS: usize = 200;

/// Where a block's step happened. The app keeps a `Mark`, so the block stays on its text as
/// the history drops rows and the screen reflows; a plain row and column stays put.
pub trait At {
    /// The row and column now, or None once the row left the history.
    fn point(&self) -> Option<(u32, u16)>;
}

impl At for Mark {
    fn point(&self) -> Option<(u32, u16)> {
        Mark::point(self)
    }
}

impl At for (u32, u16) {
    fn point(&self) -> Option<(u32, u16)> {
        Some(*self)
    }
}

/// One command: its prompt, where its input began, where its output began, and the row its
/// end left the cursor on, which the block does not include.
pub struct Block<P> {
    pub id: u64,
    pub prompt: P,
    pub input: Option<P>,
    pub output: Option<P>,
    pub end: Option<P>,
    pub command: Option<String>,
    pub exit_code: Option<i32>,
}

/// A block's rows as they are now: from its prompt row up to `end`, not included.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Rows {
    pub prompt: u32,
    pub input: Option<(u32, u16)>,
    pub output: u32,
    pub end: u32,
}

impl<P: At> Block<P> {
    /// The block's rows now, or None once any of them left the history.
    pub fn rows(&self) -> Option<Rows> {
        let row = |p: &Option<P>| p.as_ref().and_then(At::point).map(|(row, _)| row);
        Some(Rows {
            prompt: self.prompt.point()?.0,
            input: match &self.input {
                Some(input) => Some(input.point()?),
                None => None,
            },
            output: row(&self.output)?,
            end: row(&self.end)?,
        })
    }
}

pub struct Blocks<P> {
    completed: Vec<Block<P>>,
    pending: Option<Block<P>>,
    next_id: u64,
}

impl<P> Default for Blocks<P> {
    fn default() -> Self {
        Blocks {
            completed: Vec::new(),
            pending: None,
            next_id: 1,
        }
    }
}

impl<P: At> Blocks<P> {
    /// Takes the shell's step at `at`. A prompt, input or command starting while a command is
    /// still open closes that command there, since some shells (fish) can lose its end; `again`
    /// makes a second position on the same cell for that end.
    pub fn apply(&mut self, step: PromptStep, at: P, mut again: impl FnMut(&P) -> Option<P>) {
        let opens = matches!(
            step,
            PromptStep::PromptStart | PromptStep::InputStart | PromptStep::OutputStart(_)
        );
        if opens && let Some(mut open) = self.pending.take_if(|p| p.output.is_some()) {
            open.end = again(&at);
            self.push(open);
        }
        match step {
            PromptStep::PromptStart => self.pending = Some(self.open(at)),
            PromptStep::InputStart => match &mut self.pending {
                Some(pending) => pending.input = Some(at),
                None => {
                    let mut pending = self.open(at);
                    pending.input = again(&pending.prompt);
                    self.pending = Some(pending);
                }
            },
            PromptStep::OutputStart(command) => {
                let pending = match self.pending.take() {
                    Some(mut pending) => {
                        pending.output = Some(at);
                        pending
                    }
                    None => {
                        let mut pending = self.open(at);
                        pending.output = again(&pending.prompt);
                        pending
                    }
                };
                self.pending = Some(Block { command, ..pending });
            }
            PromptStep::CommandEnd(exit_code) => {
                if let Some(mut pending) = self.pending.take()
                    && pending.output.is_some()
                {
                    pending.end = Some(at);
                    pending.exit_code = exit_code;
                    self.push(pending);
                }
            }
        }
    }

    /// The completed blocks, oldest first.
    pub fn blocks(&self) -> &[Block<P>] {
        &self.completed
    }

    /// The command still running or being typed, if any.
    pub fn pending(&self) -> Option<&Block<P>> {
        self.pending.as_ref()
    }

    /// The id the next block will take.
    pub fn next_id(&self) -> u64 {
        self.next_id
    }

    /// The newest completed block whose rows hold screen row `row` now.
    pub fn block_at(&self, row: u32) -> Option<&Block<P>> {
        self.completed
            .iter()
            .rev()
            .find(|b| b.rows().is_some_and(|r| (r.prompt..r.end).contains(&row)))
    }

    /// The completed block `id`.
    pub fn block(&self, id: u64) -> Option<&Block<P>> {
        self.completed.iter().find(|b| b.id == id)
    }

    /// Replaces the blocks with those the daemon kept, as an attach restores them.
    pub fn seed(&mut self, blocks: Vec<Block<P>>, pending: Option<Block<P>>) {
        self.next_id = blocks
            .iter()
            .chain(pending.as_ref())
            .map(|b| b.id + 1)
            .max()
            .unwrap_or(1);
        self.completed = blocks;
        self.pending = pending;
    }

    fn open(&mut self, prompt: P) -> Block<P> {
        let id = self.next_id;
        self.next_id += 1;
        Block {
            id,
            prompt,
            input: None,
            output: None,
            end: None,
            command: None,
            exit_code: None,
        }
    }

    fn push(&mut self, block: Block<P>) {
        self.completed.push(block);
        if self.completed.len() > MAX_BLOCKS {
            self.completed.drain(..self.completed.len() - MAX_BLOCKS);
        }
    }
}
