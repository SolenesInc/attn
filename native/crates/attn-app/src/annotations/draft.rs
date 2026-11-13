//! An agent terminal's draft of annotations as the daemon keeps it: fetched when the terminal
//! is drawn and on every connection, saved whole at the next generation after each change, and
//! read again when the daemon holds a newer one.

use attn_client::Connection;
use attn_protocol::{
    SessionAnnotationsClearMessage, SessionAnnotationsClearResultMessage,
    SessionAnnotationsGetMessage, SessionAnnotationsGetResultMessage,
    SessionAnnotationsSaveMessage, SessionAnnotationsSaveResultMessage,
    SessionAnnotationsSubmitMessage, SessionAnnotationsSubmitResultMessage,
    SessionMessagesGetMessage, SessionMessagesGetResultMessage,
};
use rune::{TextEdit, Tx};

use crate::State;
use crate::terminal::{Anchor, Annotation, Miss, Notice, Outcome, Popup, Transcript};

/// How long a drag that marked nothing says why.
const NOTICE_SECONDS: f64 = 5.0;
/// How long typing in the note pauses before the note is saved, as the Tauri panel waits:
/// measured to clear an ordinary gap between keystrokes, 100 to 250 ms.
const NOTE_PAUSE: f64 = 0.4;
/// How long the panel says the marks went before it closes.
const SENT_SECONDS: f64 = 2.2;

/// Fetches what each drawn agent terminal is missing: its session's messages, or its draft.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    if state.facts.connection != Connection::Connected {
        return;
    }
    for (id, session, messages, draft) in state.terminals.markings_wanting() {
        if messages {
            fetch_messages(state, &id, &session, tx);
        }
        if draft {
            fetch_draft(state, &id, &session, tx);
        }
    }
}

fn fetch_messages(state: &mut State, id: &str, session: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    marking.wants_messages = false;
    marking.messages_asked += 1;
    let asked = marking.messages_asked;
    let (id, session) = (id.to_owned(), session.to_owned());
    let session_id = session.clone();
    state.ask(
        move |request_id| SessionMessagesGetMessage {
            request_id,
            session_id,
            ..SessionMessagesGetMessage::new()
        },
        move |state, answer, _| {
            let Some(result) = answer.parse::<SessionMessagesGetResultMessage>() else {
                return;
            };
            let Some(marking) = state
                .terminals
                .marking_mut(&id)
                .filter(|m| m.session == session && m.messages_asked == asked)
            else {
                return;
            };
            let transcript = match (result.success, result.status.as_str()) {
                (true, "discovering") => Some(Transcript::Discovering),
                (true, "ready") => Some(Transcript::Ready),
                (true, "unavailable") => Some(Transcript::Unavailable(result.detail.clone())),
                _ => None,
            };
            let Some(transcript) = transcript else {
                let why = result
                    .error
                    .unwrap_or_else(|| format!("an unknown status: {}", result.status));
                eprintln!("attn-app: {session}: message window unavailable: {why}");
                marking.transcript = Transcript::Unreadable(why);
                return;
            };
            marking.transcript = transcript;
            marking.set_messages(result.messages);
        },
        tx,
    );
}

fn fetch_draft(state: &mut State, id: &str, session: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    marking.wants_draft = false;
    let (id, session) = (id.to_owned(), session.to_owned());
    let session_id = session.clone();
    state.ask(
        move |request_id| SessionAnnotationsGetMessage {
            request_id,
            session_id,
            ..SessionAnnotationsGetMessage::new()
        },
        move |state, answer, _| {
            let Some(stored) = answer
                .parse::<SessionAnnotationsGetResultMessage>()
                .filter(|r| r.success)
            else {
                return;
            };
            if let Some(marking) = state
                .terminals
                .marking_mut(&id)
                .filter(|m| m.session == session)
            {
                marking.hydrate(
                    &stored.annotations,
                    stored.note.as_deref().unwrap_or(""),
                    stored.generation,
                );
            }
        },
        tx,
    );
}

/// Saves terminal `id`'s whole draft at the next generation. A daemon that holds a newer one
/// answers that this one is stale, and the newer one replaces it here.
pub(crate) fn persist(state: &mut State, id: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    marking.generation += 1;
    let save = SessionAnnotationsSaveMessage {
        session_id: marking.session.clone(),
        annotations: marking.marks.iter().map(|m| m.wire()).collect(),
        note: Some(marking.note.text.clone()),
        generation: marking.generation,
        ..SessionAnnotationsSaveMessage::new()
    };
    let (id, session) = (id.to_owned(), marking.session.clone());
    state.ask(
        move |request_id| SessionAnnotationsSaveMessage { request_id, ..save },
        move |state, answer, tx| {
            let stale = answer
                .parse::<SessionAnnotationsSaveResultMessage>()
                .is_some_and(|r| !r.success && r.stale == Some(true));
            if stale {
                fetch_draft(state, &id, &session, tx);
            }
        },
        tx,
    );
}

/// The comment box of terminal `id`'s popup.
pub(crate) fn comment_field_key(id: &str) -> String {
    format!("terminal.{id}.annotation{}", crate::keys::FIELD_KEY_SUFFIX)
}

/// Whether terminal `id`'s popup is writing a comment, which takes the keyboard back to its
/// box instead of opening another mark.
fn holds_writing(state: &mut State, id: &str, tx: &mut Tx) -> bool {
    let writing = state
        .terminals
        .marking(id)
        .and_then(|m| m.popup.as_ref())
        .is_some_and(|p| p.writing);
    if writing {
        tx.focus(comment_field_key(id), false);
    }
    writing
}

fn open_popup(state: &mut State, id: &str, popup: Popup, tx: &mut Tx) {
    let writing = popup.writing;
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    marking.popup = Some(popup);
    marking.notice = None;
    if writing {
        tx.focus(comment_field_key(id), false);
    }
}

/// A ⌥-drag in terminal `id` let go at `at` over an agent's message: it is marked, and the
/// popup opens to label it.
pub(crate) fn anchored(state: &mut State, id: &str, anchor: &Anchor, at: (f32, f32), tx: &mut Tx) {
    if holds_writing(state, id, tx) {
        return;
    }
    let Some(mark) = state
        .terminals
        .marking_mut(id)
        .and_then(|m| m.add(anchor))
        .map(|m| m.id.clone())
    else {
        return;
    };
    open_popup(state, id, Popup::new(mark, at, false, ""), tx);
}

/// A ⌥-click at `at` landed on mark `mark`: its popup opens, writing when it has a comment.
pub(crate) fn opened(state: &mut State, id: &str, mark: &str, at: (f32, f32), tx: &mut Tx) {
    if holds_writing(state, id, tx) {
        return;
    }
    let Some(comment) = state
        .terminals
        .marking(id)
        .and_then(|m| m.mark(mark))
        .map(|m| m.comment.clone())
    else {
        return;
    };
    let writing = !comment.is_empty();
    open_popup(
        state,
        id,
        Popup::new(mark.to_owned(), at, writing, &comment),
        tx,
    );
}

/// A ⌥-drag let go at `at` without marking anything: a notice there says why, for a while.
pub(crate) fn missed(state: &mut State, id: &str, miss: Miss, at: (f32, f32), tx: &mut Tx) {
    let until = tx.clock() + NOTICE_SECONDS;
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    let text = match (miss, &marking.transcript) {
        (Miss::OutsideMessages, _) => "Only what the agent wrote can be annotated. This text is the TUI’s own, your own, or from a turn that has scrolled out of the window.".to_owned(),
        (Miss::NoMessages, Transcript::Unreadable(_)) => "No transcript could be read for this session, so there is nothing to annotate. The daemon log names the lookup that failed.".to_owned(),
        (Miss::NoMessages, Transcript::Discovering) => "The agent’s first completed message is still being recorded. Try again in a moment.".to_owned(),
        (Miss::NoMessages, Transcript::Unavailable(detail)) => detail
            .clone()
            .filter(|d| !d.is_empty())
            .unwrap_or_else(|| "No exact transcript is available for this session.".to_owned()),
        (Miss::NoMessages, Transcript::Ready) => "The agent has not written a message to annotate yet.".to_owned(),
    };
    marking.notice = Some(Notice { text, at, until });
    tx.wake_at(until);
}

/// Takes down each notice whose time is up: true when one went.
pub(crate) fn expire(state: &mut State, now: f64) -> bool {
    state.terminals.expire_notices(now)
}

/// Closes terminal `id`'s popup, giving the keyboard back to the terminal.
fn close(state: &mut State, id: &str, tx: &mut Tx) {
    if let Some(marking) = state.terminals.marking_mut(id) {
        marking.popup = None;
    }
    tx.blur();
}

/// Dismisses terminal `id`'s popup: a mark left without a label or a comment goes with it.
pub(crate) fn dismiss(state: &mut State, id: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    let Some(popup) = marking.popup.as_ref() else {
        return;
    };
    let mark = popup.mark.clone();
    let empty = marking
        .mark(&mark)
        .is_some_and(|m| m.label.is_empty() && m.comment.is_empty());
    if empty {
        marking.remove(&mark);
        persist(state, id, tx);
    }
    close(state, id, tx);
}

/// A press on a terminal outside the popups dismisses each popup that is not writing, or first
/// closes its open label list.
pub(crate) fn pressed_anywhere(state: &mut State, tx: &mut Tx) {
    for id in state.terminals.popups() {
        let Some(popup) = state
            .terminals
            .marking_mut(&id)
            .and_then(|m| m.popup.as_mut())
        else {
            continue;
        };
        if popup.picking {
            popup.picking = false;
            popup.hint = None;
        } else if !popup.writing {
            dismiss(state, &id, tx);
        }
    }
}

/// Escape with nothing focused: an open label list closes, or else the keyboard terminal's
/// popup is dismissed. True when it took the key.
pub(crate) fn escape(state: &mut State, tx: &mut Tx) -> bool {
    let Some(id) = state.window.keyboard_terminal(&state.facts) else {
        return false;
    };
    let Some(popup) = state
        .terminals
        .marking_mut(&id)
        .and_then(|m| m.popup.as_mut())
    else {
        return false;
    };
    if popup.picking {
        popup.picking = false;
        popup.hint = None;
        return true;
    }
    dismiss(state, &id, tx);
    true
}

/// Picks label `label` for the popup's mark, or takes it off when it was the mark's label. A
/// mark left with nothing goes; one without a comment is done and the popup closes.
pub(crate) fn pick_label(state: &mut State, id: &str, label: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    let Some(popup) = marking.popup.as_mut() else {
        return;
    };
    popup.picking = false;
    popup.hint = None;
    let (mark, writing) = (popup.mark.clone(), popup.writing);
    let Some(annotation) = marking.mark_mut(&mark) else {
        return;
    };
    annotation.label = if annotation.label == label {
        String::new()
    } else {
        label.to_owned()
    };
    let (bare, uncommented) = (annotation.label.is_empty(), annotation.comment.is_empty());
    if bare && uncommented && !writing {
        marking.remove(&mark);
    }
    persist(state, id, tx);
    if uncommented && !writing {
        close(state, id, tx);
    }
}

/// The popup starts writing a comment on its mark.
pub(crate) fn write(state: &mut State, id: &str, tx: &mut Tx) {
    if let Some(popup) = state
        .terminals
        .marking_mut(id)
        .and_then(|m| m.popup.as_mut())
    {
        popup.writing = true;
        tx.focus(comment_field_key(id), false);
    }
}

/// Keeps the comment written in the popup; a mark left with nothing goes.
pub(crate) fn save_comment(state: &mut State, id: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    let Some(popup) = marking.popup.as_ref() else {
        return;
    };
    let (mark, comment) = (popup.mark.clone(), popup.comment.text.trim().to_owned());
    if let Some(annotation) = marking.mark_mut(&mark) {
        annotation.comment = comment;
        if annotation.comment.is_empty() && annotation.label.is_empty() {
            marking.remove(&mark);
        }
    }
    persist(state, id, tx);
    close(state, id, tx);
}

/// Takes mark `mark` off terminal `id`, closing its popup.
pub(crate) fn remove(state: &mut State, id: &str, mark: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    marking.remove(mark);
    let shown = marking.popup.as_ref().is_some_and(|p| p.mark == mark);
    if shown {
        close(state, id, tx);
    }
    persist(state, id, tx);
}

/// Terminal `id`'s popup was laid out at `size`: true when that moves it.
pub(crate) fn measured(state: &mut State, id: &str, size: (f32, f32)) -> bool {
    let Some(popup) = state
        .terminals
        .marking_mut(id)
        .and_then(|m| m.popup.as_mut())
    else {
        return false;
    };
    popup.size.replace(size) != Some(size)
}

/// Terminal `id`'s panel was laid out at `size`: true when that moves it.
pub(crate) fn panel_measured(state: &mut State, id: &str, size: (f32, f32)) -> bool {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return false;
    };
    marking.panel_size.replace(size) != Some(size)
}

/// A digit with the label list open picks the label it numbers, 0 the tenth. True when it took
/// the key.
pub(crate) fn digit(state: &mut State, digit: u32, tx: &mut Tx) -> bool {
    let Some(id) = state.window.keyboard_terminal(&state.facts) else {
        return false;
    };
    let picking = state
        .terminals
        .marking(&id)
        .and_then(|m| m.popup.as_ref())
        .is_some_and(|p| p.picking);
    if !picking {
        return false;
    }
    let index = if digit == 0 { 9 } else { digit as usize - 1 };
    let labels: Vec<_> = crate::annotations::picker_groups()
        .into_iter()
        .flatten()
        .collect();
    if let Some(label) = labels.get(index) {
        pick_label(state, &id, label.id, tx);
    }
    true
}

/// Opens or closes the list of every other label.
pub(crate) fn toggle_picker(state: &mut State, id: &str) {
    if let Some(popup) = state
        .terminals
        .marking_mut(id)
        .and_then(|m| m.popup.as_mut())
    {
        popup.picking = !popup.picking;
    }
}

/// The pointer came over a control that does `hint`, or left it.
pub(crate) fn hint(state: &mut State, id: &str, hint: Option<&str>) {
    if let Some(popup) = state
        .terminals
        .marking_mut(id)
        .and_then(|m| m.popup.as_mut())
    {
        match hint {
            Some(hint) => popup.hint = Some(hint.to_owned()),
            None => popup.hint = None,
        }
    }
}

impl Popup {
    fn new(mark: String, at: (f32, f32), writing: bool, comment: &str) -> Popup {
        let mut comment = TextEdit::new(comment);
        comment.set_caret(comment.text.len());
        Popup {
            mark,
            at,
            writing,
            comment,
            picking: false,
            hint: None,
            size: None,
            moved: None,
        }
    }
}

/// The note changed: it is saved once typing pauses.
pub(crate) fn note_edited(state: &mut State, id: &str, tx: &mut Tx) {
    let due = tx.clock() + NOTE_PAUSE;
    if let Some(marking) = state.terminals.marking_mut(id) {
        marking.note_due = Some(due);
        tx.wake_at(due);
    }
}

/// Saves terminal `id`'s note now when an edit to it is waiting to be saved.
pub(crate) fn flush_note(state: &mut State, id: &str, tx: &mut Tx) {
    let waiting = state
        .terminals
        .marking_mut(id)
        .and_then(|m| m.note_due.take())
        .is_some();
    if waiting {
        persist(state, id, tx);
    }
}

/// Saves each note whose typing paused, and closes each panel whose sent marks have shown long
/// enough. True when either happened.
pub(crate) fn due(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let (notes, sent) = state.terminals.annotations_due(now);
    for id in &notes {
        flush_note(state, id, tx);
    }
    !notes.is_empty() || sent
}

/// The send key: the marks of the terminal that holds the keyboard go to its session.
pub(crate) fn send_key(state: &mut State, tx: &mut Tx) {
    let Some(id) = state.window.keyboard_terminal(&state.facts) else {
        return;
    };
    let has_marks = state
        .terminals
        .marking(&id)
        .is_some_and(|m| !m.marks.is_empty());
    if has_marks {
        send(state, &id, tx);
    }
}

/// Sends terminal `id`'s marks and note to its session as one message, keeping a comment the
/// popup is writing. Marks made or changed while it is in flight stay.
pub(crate) fn send(state: &mut State, id: &str, tx: &mut Tx) {
    let sending_now = state
        .terminals
        .marking(id)
        .is_none_or(|m| m.marks.is_empty() || m.outcome == Some(Outcome::Sending));
    if sending_now {
        return;
    }
    flush_note(state, id, tx);
    let marking = state.terminals.marking_mut(id).expect("checked above");
    if let Some(popup) = marking.popup.take().filter(|p| p.writing) {
        let comment = popup.comment.text.trim().to_owned();
        if let Some(mark) = marking.mark_mut(&popup.mark) {
            mark.comment = comment;
            if mark.comment.is_empty() && mark.label.is_empty() {
                marking.remove(&popup.mark);
            }
        }
    }
    tx.blur();
    let sending = marking.marks.clone();
    if sending.is_empty() {
        persist(state, id, tx);
        return;
    }
    let note = marking.note.text.trim().to_owned();
    let quoted: Vec<crate::annotations::Quoted> = sending
        .iter()
        .map(|m| crate::annotations::Quoted {
            quote: &m.quote,
            label: &m.label,
            comment: &m.comment,
            start: m.start,
        })
        .collect();
    let text = match crate::annotations::terminal_payload(&quoted, &note) {
        Ok(text) => text,
        Err(why) => {
            marking.outcome = Some(Outcome::Failed(why));
            return;
        }
    };
    marking.outcome = Some(Outcome::Sending);
    let session = marking.session.clone();
    let (id, session_id) = (id.to_owned(), session.clone());
    state.ask(
        move |request_id| SessionAnnotationsSubmitMessage {
            request_id,
            session_id,
            text,
            ..SessionAnnotationsSubmitMessage::new()
        },
        move |state, answer, tx| {
            let result = answer.parse::<SessionAnnotationsSubmitResultMessage>();
            sent(state, &id, &session, result, &sending, &note, tx);
        },
        tx,
    );
}

/// The daemon answered a send of `sending` and `note`: what went is taken off, and the panel
/// says how it went.
fn sent(
    state: &mut State,
    id: &str,
    session: &str,
    result: Option<SessionAnnotationsSubmitResultMessage>,
    sending: &[Annotation],
    note: &str,
    tx: &mut Tx,
) {
    let outcome = match &result {
        None => Some(Outcome::Failed("Send failed".into())),
        Some(r) if !r.success && r.status == "skipped_pending_approval" => Some(Outcome::Skipped),
        Some(r) if !r.success => Some(Outcome::Failed(
            r.error
                .clone()
                .unwrap_or_else(|| "Session annotation send failed".into()),
        )),
        Some(r) if r.status != "delivered" => Some(Outcome::Failed(
            "The session did not take the feedback. Nothing was sent.".into(),
        )),
        Some(_) => None,
    };
    let Some(marking) = state
        .terminals
        .marking_mut(id)
        .filter(|m| m.session == session)
    else {
        if outcome.is_none() {
            keep_unsent(state, session, sending, note, tx);
        }
        return;
    };
    if let Some(outcome) = outcome {
        marking.outcome = Some(outcome);
        return;
    }
    let delivered = delivered_unchanged(&marking.marks, sending);
    marking.marks.retain(|m| !delivered.contains(&m.id));
    if !note.is_empty() && marking.note.text.trim() == note {
        marking.note = TextEdit::default();
    }
    let kept = marking.marks.len();
    let until = tx.clock() + SENT_SECONDS;
    marking.outcome = Some(Outcome::Sent {
        count: sending.len(),
        kept,
        until,
    });
    tx.wake_at(until);
    if kept > 0 || !marking.note.text.trim().is_empty() {
        persist(state, id, tx);
        return;
    }
    marking.generation += 1;
    let (generation, id) = (marking.generation, id.to_owned());
    let session_id = session.to_owned();
    state.ask(
        move |request_id| SessionAnnotationsClearMessage {
            request_id,
            session_id,
            generation,
            ..SessionAnnotationsClearMessage::new()
        },
        move |state, answer, _| {
            let Some(cleared) = answer.parse::<SessionAnnotationsClearResultMessage>() else {
                return;
            };
            if let Some(marking) = state.terminals.marking_mut(&id) {
                marking.generation = marking.generation.max(cleared.generation);
            }
        },
        tx,
    );
}

/// The ids of `current` marks that went in `sent` and have not changed since.
fn delivered_unchanged(current: &[Annotation], sent: &[Annotation]) -> Vec<String> {
    current
        .iter()
        .filter(|m| {
            sent.iter()
                .any(|s| s.id == m.id && s.label == m.label && s.comment == m.comment)
        })
        .map(|m| m.id.clone())
        .collect()
}

/// The terminal that sent `sending` no longer shows its session: the session's stored draft
/// loses what went, read and saved in the background.
fn keep_unsent(state: &mut State, session: &str, sending: &[Annotation], note: &str, tx: &mut Tx) {
    let (sending, note) = (sending.to_vec(), note.to_owned());
    let (session, session_id) = (session.to_owned(), session.to_owned());
    state.ask(
        move |request_id| SessionAnnotationsGetMessage {
            request_id,
            session_id,
            ..SessionAnnotationsGetMessage::new()
        },
        move |state, answer, tx| {
            let Some(stored) = answer
                .parse::<SessionAnnotationsGetResultMessage>()
                .filter(|r| r.success)
            else {
                return;
            };
            let mut marking = crate::terminal::Marking::new(session.clone());
            let stored_note = stored.note.clone().unwrap_or_default();
            marking.hydrate(&stored.annotations, &stored_note, stored.generation);
            let delivered = delivered_unchanged(&marking.marks, &sending);
            let kept: Vec<_> = marking
                .marks
                .iter()
                .filter(|m| !delivered.contains(&m.id))
                .map(Annotation::wire)
                .collect();
            let note = if !note.is_empty() && stored_note.trim() == note {
                String::new()
            } else {
                stored_note
            };
            let save = SessionAnnotationsSaveMessage {
                session_id: session.clone(),
                annotations: kept,
                note: Some(note),
                generation: stored.generation + 1,
                ..SessionAnnotationsSaveMessage::new()
            };
            state.ask(
                move |request_id| SessionAnnotationsSaveMessage { request_id, ..save },
                |_, _, _| {},
                tx,
            );
        },
        tx,
    );
}

/// The panel's card for mark `mark` was pressed: its popup opens, writing.
pub(crate) fn open_card(state: &mut State, id: &str, mark: &str, at: (f32, f32), tx: &mut Tx) {
    if holds_writing(state, id, tx) {
        return;
    }
    let Some(comment) = state
        .terminals
        .marking(id)
        .and_then(|m| m.mark(mark))
        .map(|m| m.comment.clone())
    else {
        return;
    };
    open_popup(
        state,
        id,
        Popup::new(mark.to_owned(), at, true, &comment),
        tx,
    );
}

/// Takes mark `mark` off from the panel, leaving the keyboard where it is.
pub(crate) fn remove_card(state: &mut State, id: &str, mark: &str, tx: &mut Tx) {
    let Some(marking) = state.terminals.marking_mut(id) else {
        return;
    };
    marking.remove(mark);
    if marking.popup.as_ref().is_some_and(|p| p.mark == mark) {
        marking.popup = None;
    }
    persist(state, id, tx);
}
