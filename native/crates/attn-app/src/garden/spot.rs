use rune::{Key, KeyEvent, TextEdit, Tx};
use serde::Serialize;

use crate::State;

/// What one part of one seed page is doing: words being written for it, a move sent and not
/// answered yet, or why the last move was refused. One part at a time.
pub(crate) struct Edit {
    at: Spot,
    writing: bool,
    busy: bool,
    failure: Option<String>,
}

/// The chord that sends a field's words: Return, where Shift+Return starts a new line, or
/// ⌘Return in a field where Return starts one.
#[derive(Clone, Copy)]
pub(super) enum Sends {
    Return,
    CmdReturn,
}

/// A part of a seed page: its sheet, its seed and its own name on the page.
#[derive(Clone, PartialEq)]
pub(super) struct Spot {
    at: String,
    seed: String,
    part: String,
}

impl Spot {
    pub(super) fn new(at: &str, seed: &str, part: &str) -> Spot {
        Spot {
            at: at.to_owned(),
            seed: seed.to_owned(),
            part: part.to_owned(),
        }
    }

    pub(super) fn key(&self, suffix: &str) -> String {
        format!("{}.{}.{suffix}", self.at, self.part)
    }

    pub(super) fn field(&self) -> String {
        self.key("field")
    }

    pub(super) fn seed(&self) -> &str {
        &self.seed
    }

    pub(super) fn part(&self) -> &str {
        &self.part
    }

    fn edit<'a>(&self, state: &'a State) -> Option<&'a Edit> {
        state.window.garden.edit.as_ref().filter(|e| e.at == *self)
    }

    fn busy(state: &State) -> bool {
        state.window.garden.edit.as_ref().is_some_and(|e| e.busy)
    }

    /// Opens the part's field holding `words`, unless another move is under way.
    pub(super) fn write(&self, state: &mut State, words: &str, tx: &mut Tx) {
        if Spot::busy(state) {
            return;
        }
        let garden = &mut state.window.garden;
        garden.edit = Some(Edit {
            at: self.clone(),
            writing: true,
            busy: false,
            failure: None,
        });
        garden.words = TextEdit::new(words);
        garden.words.anchor = 0;
        garden.revision += 1;
        tx.focus(self.field(), true);
    }

    /// The chord that `sends` sends the words through `commit`; Escape puts them away and gives
    /// the keyboard to `home`. Neither acts while the words sent last wait for their answer.
    pub(super) fn field_key(
        &self,
        state: &mut State,
        ev: &KeyEvent,
        (home, sends): (&str, Sends),
        commit: impl FnOnce(&mut State, String, &mut Tx),
        tx: &mut Tx,
    ) -> bool {
        let answer_due = self.edit(state).is_some_and(|e| e.busy);
        let send = match sends {
            Sends::Return => !ev.mods.shift,
            Sends::CmdReturn => ev.mods.cmd,
        };
        match ev.key {
            Key::Enter | Key::Escape if answer_due => true,
            Key::Enter if send => {
                let words = state.window.garden.words.text.trim().to_owned();
                commit(state, words, tx);
                true
            }
            Key::Escape => {
                self.close(state, home, true, tx);
                true
            }
            _ => false,
        }
    }

    pub(super) fn fail(&self, state: &mut State, reason: String) {
        let garden = &mut state.window.garden;
        if let Some(edit) = garden.edit.as_mut().filter(|e| e.at == *self) {
            edit.busy = false;
            edit.failure = Some(reason);
        }
        garden.revision += 1;
    }

    /// Puts the part's words away and gives the keyboard to `home`.
    pub(super) fn put_away(&self, state: &mut State, home: &str, tx: &mut Tx) {
        self.close(state, home, true, tx);
    }

    fn close(&self, state: &mut State, home: &str, ring: bool, tx: &mut Tx) {
        let garden = &mut state.window.garden;
        garden.edit = None;
        garden.revision += 1;
        tx.focus(home.to_owned(), ring);
    }

    /// Sends the part's move, unless another is under way. Once it is done, the part's field
    /// closes and the keyboard goes to `home`, where a pressed control may no longer show; a
    /// refusal stays on the part.
    pub(super) fn send<M: Serialize>(
        &self,
        state: &mut State,
        build: impl FnOnce(String) -> M,
        home: String,
        tx: &mut Tx,
    ) {
        if Spot::busy(state) {
            return;
        }
        let writing = self.edit(state).is_some_and(|e| e.writing);
        let garden = &mut state.window.garden;
        garden.edit = Some(Edit {
            at: self.clone(),
            writing,
            busy: true,
            failure: None,
        });
        garden.revision += 1;
        let spot = self.clone();
        state.ask(
            build,
            move |state, answer, tx| {
                if state
                    .window
                    .garden
                    .edit
                    .as_ref()
                    .is_none_or(|e| e.at != spot)
                {
                    return;
                }
                match answer.outcome() {
                    Ok(_) => spot.close(state, &home, writing, tx),
                    Err(reason) => {
                        spot.fail(state, reason);
                        if writing {
                            tx.focus(spot.field(), true);
                        }
                    }
                }
            },
            tx,
        );
    }
}

/// What a part of a page shows of its edit.
pub(super) struct Shown<'a> {
    pub(super) writing: bool,
    pub(super) busy: bool,
    pub(super) failure: Option<&'a str>,
}

impl super::Garden {
    pub(super) fn shown(&self, spot: &Spot) -> Shown<'_> {
        let edit = self.edit.as_ref().filter(|e| e.at == *spot);
        Shown {
            writing: edit.is_some_and(|e| e.writing),
            busy: edit.is_some_and(|e| e.busy),
            failure: edit.and_then(|e| e.failure.as_deref()),
        }
    }
}
