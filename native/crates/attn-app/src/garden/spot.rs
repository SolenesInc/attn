use rune::{Key, KeyEvent, TextEdit, Tx};
use serde::Serialize;

use crate::State;
use crate::requests::Answer;

/// What one part of one seed page is doing: the words being written for it, a move sent and
/// not answered yet, or why its last move was refused.
#[derive(Default)]
pub(crate) struct Edit {
    writing: bool,
    busy: bool,
    failure: Option<String>,
    words: TextEdit,
    handover: Option<super::handover::Handover>,
}

/// The chord that sends a field's words: Return, where Shift+Return starts a new line, or
/// ⌘Return in a field where Return starts one.
#[derive(Clone, Copy)]
pub(super) enum Sends {
    Return,
    CmdReturn,
}

/// A part of a seed page: its sheet, its seed and its own name on the page.
#[derive(Clone, PartialEq, Eq, Hash)]
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
        state.window.garden.edits.get(self)
    }

    fn busy(&self, state: &State) -> bool {
        self.edit(state).is_some_and(|e| e.busy)
    }

    /// The part's words, as its field types them.
    pub(super) fn words_mut<'a>(&self, state: &'a mut State) -> &'a mut TextEdit {
        let garden = &mut state.window.garden;
        garden.revision += 1;
        &mut garden.edits.entry(self.clone()).or_default().words
    }

    /// The part's words as written, trimmed.
    pub(super) fn words(&self, state: &State) -> String {
        self.edit(state)
            .map_or("", |e| e.words.text.trim())
            .to_owned()
    }

    /// The part's handover, as its fields type it.
    pub(super) fn handover_draft<'a>(
        &self,
        state: &'a mut State,
    ) -> &'a mut super::handover::Handover {
        let garden = &mut state.window.garden;
        garden.revision += 1;
        garden
            .edits
            .entry(self.clone())
            .or_default()
            .handover
            .get_or_insert_with(Default::default)
    }

    pub(super) fn handover_mut<'a>(
        &self,
        state: &'a mut State,
    ) -> Option<&'a mut super::handover::Handover> {
        state
            .window
            .garden
            .edits
            .get_mut(self)
            .and_then(|e| e.handover.as_mut())
    }

    /// Opens the part's field holding `words`, unless its own move is under way. Other parts
    /// keep their words and moves.
    pub(super) fn write(&self, state: &mut State, words: &str, tx: &mut Tx) {
        self.write_with(state, words, None, tx);
    }

    pub(super) fn write_with(
        &self,
        state: &mut State,
        words: &str,
        handover: Option<super::handover::Handover>,
        tx: &mut Tx,
    ) {
        if self.busy(state) {
            return;
        }
        let mut words = TextEdit::new(words);
        words.anchor = 0;
        let garden = &mut state.window.garden;
        garden.edits.insert(
            self.clone(),
            Edit {
                writing: true,
                words,
                handover,
                ..Edit::default()
            },
        );
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
                let words = self.words(state);
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
        if let Some(edit) = garden.edits.get_mut(self) {
            edit.busy = false;
            edit.failure = Some(reason);
        }
        garden.revision += 1;
    }

    /// Puts the part's words away and gives the keyboard to `home`.
    pub(super) fn put_away(&self, state: &mut State, home: &str, tx: &mut Tx) {
        self.close(state, home, true, tx);
    }

    /// Forgets the part's edit, keeping the keyboard where it is.
    pub(super) fn forget(&self, state: &mut State) {
        let garden = &mut state.window.garden;
        garden.edits.remove(self);
        garden.revision += 1;
    }

    fn close(&self, state: &mut State, home: &str, ring: bool, tx: &mut Tx) {
        self.forget(state);
        tx.focus(home.to_owned(), ring);
    }

    /// Whether an answer may move the keyboard: not while it is on another part being written.
    fn may_move_keyboard(&self, state: &State, tx: &Tx) -> bool {
        let Some(focused) = tx.focused() else {
            return true;
        };
        !state
            .window
            .garden
            .edits
            .iter()
            .any(|(spot, edit)| spot != self && edit.writing && focused.starts_with(&spot.key("")))
    }

    /// Sends the part's move, unless its own last move is under way, and drops the refusals
    /// other parts' moves left; their words and their moves under way stay. Once it is done, the
    /// part's field closes and the keyboard goes to `home`, where a pressed control may no
    /// longer show, unless the user is typing in another part; a refusal stays on the part.
    pub(super) fn send<M: Serialize>(
        &self,
        state: &mut State,
        build: impl FnOnce(String) -> M,
        home: String,
        tx: &mut Tx,
    ) {
        self.send_as(state, None, build, home, |_, _, _| {}, tx);
    }

    /// As `send`, under request `id` when given, and running `after` with the answer first; an
    /// `after` that puts the edit away keeps the keyboard where it put it.
    pub(super) fn send_as<M: Serialize>(
        &self,
        state: &mut State,
        id: Option<String>,
        build: impl FnOnce(String) -> M,
        home: String,
        after: impl FnOnce(&mut State, &Answer, &mut Tx) + 'static,
        tx: &mut Tx,
    ) {
        if self.busy(state) {
            return;
        }
        let garden = &mut state.window.garden;
        garden
            .edits
            .retain(|spot, edit| spot == self || edit.writing || edit.busy);
        let edit = garden.edits.entry(self.clone()).or_default();
        let writing = edit.writing;
        edit.busy = true;
        edit.failure = None;
        garden.revision += 1;
        let spot = self.clone();
        let id = id.unwrap_or_else(|| state.next_request_id());
        let command = build(id.clone());
        state.ask_as(
            id,
            &command,
            move |state, answer, tx| {
                after(state, &answer, tx);
                if spot.edit(state).is_none() {
                    return;
                }
                let moves_keyboard = spot.may_move_keyboard(state, tx);
                match answer.outcome() {
                    Ok(_) if moves_keyboard => spot.close(state, &home, writing, tx),
                    Ok(_) => spot.forget(state),
                    Err(reason) => {
                        spot.fail(state, reason);
                        if writing && moves_keyboard {
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
    /// The words being written, while they are.
    pub(super) words: Option<&'a TextEdit>,
    pub(super) handover: Option<&'a super::handover::Handover>,
    pub(super) busy: bool,
    pub(super) failure: Option<&'a str>,
}

impl super::Garden {
    pub(super) fn shown(&self, spot: &Spot) -> Shown<'_> {
        let edit = self.edits.get(spot);
        Shown {
            writing: edit.is_some_and(|e| e.writing),
            words: edit.filter(|e| e.writing).map(|e| &e.words),
            handover: edit.filter(|e| e.writing).and_then(|e| e.handover.as_ref()),
            busy: edit.is_some_and(|e| e.busy),
            failure: edit.and_then(|e| e.failure.as_deref()),
        }
    }
}
