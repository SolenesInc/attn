use std::collections::BTreeMap;

use attn_protocol::SetSettingMessage;
use rune::{TextEdit, Tx};

use crate::State;

/// How long a field's Saved mark shows once its value lands.
const SAVED_MARK_SECONDS: f64 = 1.6;

/// How a field reads its draft from the daemon's settings, and the value a draft saves as, or
/// why it can't be saved.
#[derive(Clone, Copy)]
pub(crate) struct Codec {
    pub(crate) actual: fn(&BTreeMap<String, String>, &str) -> String,
    pub(crate) serialize: fn(&str, &str) -> Result<String, String>,
}

fn stored(settings: &BTreeMap<String, String>, key: &str) -> String {
    settings.get(key).cloned().unwrap_or_default()
}

/// The setting as the daemon holds it, saved as typed.
pub(crate) const PLAIN: Codec = Codec {
    actual: stored,
    serialize: |_, draft| Ok(draft.to_owned()),
};

/// The setting as the daemon holds it, saved trimmed.
pub(crate) const TRIMMED: Codec = Codec {
    actual: stored,
    serialize: |_, draft| Ok(draft.trim().to_owned()),
};

/// The dialog's settings as the user edits them, each saved to the daemon as soon as it is
/// committed. A field keeps what the user typed while the daemon's value moves underneath it,
/// and keeps a refused value with its reason until the user changes or retries it.
#[derive(Default)]
pub(crate) struct Autosave {
    fields: BTreeMap<String, Field>,
}

pub(crate) struct Field {
    pub(crate) draft: TextEdit,
    /// The value the daemon holds, as far as this field knows.
    saved: String,
    /// The daemon's value as last heard.
    actual: String,
    /// The value last sent, kept after its answer so leaving the field does not resend a
    /// refused value it still holds.
    attempting: Option<String>,
    pending: bool,
    /// The draft changed while a write was out: write it again when that one lands, for what
    /// caused the change.
    requested: Option<Cause>,
    pub(crate) error: Option<String>,
    confirmed: bool,
    /// The field's Saved mark shows until this time.
    saved_mark: Option<f64>,
    codec: Codec,
}

impl Default for Field {
    fn default() -> Field {
        Field {
            draft: TextEdit::default(),
            saved: String::new(),
            actual: String::new(),
            attempting: None,
            pending: false,
            requested: None,
            error: None,
            confirmed: false,
            saved_mark: None,
            codec: PLAIN,
        }
    }
}

impl Field {
    fn dirty(&self) -> bool {
        self.draft.text != self.saved || self.pending || self.error.is_some()
    }
}

impl Autosave {
    pub(crate) fn field(&self, key: &str) -> Option<&Field> {
        self.fields.get(key)
    }

    pub(crate) fn draft(&self, key: &str) -> &str {
        self.fields.get(key).map_or("", |f| f.draft.text.as_str())
    }

    pub(crate) fn draft_mut(&mut self, key: &str) -> &mut TextEdit {
        &mut self.fields.entry(key.to_owned()).or_default().draft
    }

    /// Starts following `key`, seeded with the daemon's value. A field it already follows
    /// keeps its state.
    pub(crate) fn ensure(&mut self, key: &str, settings: &BTreeMap<String, String>, codec: Codec) {
        self.fields.entry(key.to_owned()).or_insert_with(|| {
            let actual = (codec.actual)(settings, key);
            Field {
                draft: TextEdit::new(actual.clone()),
                saved: actual.clone(),
                actual,
                codec,
                ..Field::default()
            }
        });
    }

    /// Takes the daemon's values: a field the user has not changed shows the new value; a
    /// field with a change of its own keeps it.
    pub(crate) fn sync(&mut self, settings: &BTreeMap<String, String>) {
        for (key, field) in &mut self.fields {
            let actual = (field.codec.actual)(settings, key);
            if field.actual == actual {
                continue;
            }
            if field.draft.text == field.saved && !field.pending {
                field.draft = TextEdit::new(actual.clone());
                field.saved = actual.clone();
            }
            field.actual = actual;
        }
    }

    pub(crate) fn needs_flush(&self) -> bool {
        self.fields
            .values()
            .any(|f| f.pending || f.draft.text != f.saved || f.error.is_some())
    }

    /// Whether `key`'s save is out.
    pub(crate) fn sending(&self, key: &str) -> bool {
        self.fields.get(key).is_some_and(|f| f.pending)
    }

    pub(crate) fn saving(&self) -> bool {
        self.fields.values().any(|f| f.pending)
    }

    /// The refusals, by setting, in key order.
    pub(crate) fn refusals(&self) -> impl Iterator<Item = (&str, &str)> {
        self.fields
            .iter()
            .filter_map(|(key, f)| f.error.as_deref().map(|e| (key.as_str(), e)))
    }

    /// Every write landed and nothing is changed or refused, and at least one write landed.
    pub(crate) fn saved(&self) -> bool {
        !self.saving()
            && self.fields.values().all(|f| f.error.is_none())
            && self.fields.values().any(|f| f.confirmed)
            && self.fields.values().all(|f| f.draft.text == f.saved)
    }

    pub(crate) fn shows_saved_mark(&self, key: &str) -> bool {
        self.fields.get(key).is_some_and(|f| f.saved_mark.is_some())
    }

    pub(crate) fn keys(&self) -> Vec<String> {
        self.fields.keys().cloned().collect()
    }

    /// Lets each Saved mark go once its time is up; whether one went.
    pub(crate) fn expire(&mut self, now: f64) -> bool {
        let mut went = false;
        for field in self.fields.values_mut() {
            if field.saved_mark.is_some_and(|until| now >= until) {
                field.saved_mark = None;
                went = true;
            }
        }
        went
    }
}

fn autosave(state: &mut State) -> &mut Autosave {
    &mut state.window.settings.autosave
}

/// Sets `key` to `value` and saves it: a switch or a choice the user clicked.
pub(crate) fn apply(state: &mut State, key: &str, value: &str, tx: &mut Tx) {
    let settings = crate::machine::settings(&state.facts).into_owned();
    let fields = autosave(state);
    fields.ensure(key, &settings, PLAIN);
    let field = fields.fields.get_mut(key).expect("ensured");
    field.draft = TextEdit::new(value);
    field.confirmed = false;
    commit(state, key, tx);
}

/// What caused a save: the person acting, with the control to show beside a refusal and how
/// many inputs they had given by then (`Tx::inputs`), or leaving a field for somewhere else.
#[derive(Clone)]
pub(crate) enum Cause {
    At {
        control: Option<String>,
        inputs: u64,
    },
    Left,
}

impl Cause {
    /// The person acting in `control`.
    pub(crate) fn at(control: impl Into<String>, tx: &Tx) -> Cause {
        Cause::At {
            control: Some(control.into()),
            inputs: tx.inputs(),
        }
    }

    /// The person acting where the focus is.
    pub(crate) fn here(tx: &Tx) -> Cause {
        Cause::At {
            control: tx.focused().map(str::to_owned),
            inputs: tx.inputs(),
        }
    }

    /// Lands the refusal keyed `refusal` at once. When the person has done nothing since they
    /// acted, it shows them the refusal and the control they acted in; when they have, or
    /// caused it by leaving a field, the control they are in now stays where it shows.
    pub(crate) fn show(&self, refusal: String, tx: &mut Tx) {
        match self {
            Cause::At { control, inputs } if *inputs == tx.inputs() => {
                attn_ui::blocks::show_refusal(tx, super::BODY, refusal, control.as_deref())
            }
            _ => {
                tx.instant();
                tx.keep_focus_on_screen();
            }
        }
    }
}

/// Saves what the field holds, when it differs from what the daemon has.
pub(crate) fn commit(state: &mut State, key: &str, tx: &mut Tx) {
    send(state, key, Cause::here(tx), tx);
}

fn send(state: &mut State, key: &str, cause: Cause, tx: &mut Tx) {
    let Some(field) = autosave(state).fields.get_mut(key) else {
        return;
    };
    if field.pending {
        if field.attempting.as_deref() != Some(field.draft.text.as_str()) {
            field.requested = Some(cause);
        }
        return;
    }
    if field.draft.text == field.saved {
        field.error = None;
        return;
    }
    let draft = field.draft.text.clone();
    field.attempting = Some(draft.clone());
    let value = match (field.codec.serialize)(key, &draft) {
        Ok(value) => value,
        Err(reason) => {
            field.error = Some(reason);
            field.confirmed = false;
            cause.show(refusal_key(key), tx);
            super::flushed(state, tx);
            return;
        }
    };
    tx.instant();
    field.pending = true;
    let key = key.to_owned();
    let id = state.next_request_id();
    let command = SetSettingMessage {
        key: key.clone(),
        value,
        request_id: Some(id.clone()),
        ..SetSettingMessage::new()
    };
    state.ask_on(
        crate::machine::reach(&state.facts, &key),
        id,
        &command,
        move |state, answer, tx| {
            let outcome = answer.outcome().map(|_| ());
            landed(state, &key, draft, outcome, cause, tx);
        },
        tx,
    );
}

/// Commits a text field the focus left, unless it still holds the value the daemon refused.
pub(crate) fn commit_on_leave(state: &mut State, key: &str, tx: &mut Tx) {
    let Some(field) = autosave(state).fields.get(key) else {
        return;
    };
    if !field.dirty() {
        return;
    }
    if field.error.is_some() && field.attempting.as_deref() == Some(field.draft.text.as_str()) {
        return;
    }
    send(state, key, Cause::Left, tx);
}

fn refusal_key(key: &str) -> String {
    format!("settings.refusal.{key}")
}

fn landed(
    state: &mut State,
    key: &str,
    draft: String,
    outcome: Result<(), String>,
    cause: Cause,
    tx: &mut Tx,
) {
    let now = tx.clock();
    let Some(field) = autosave(state).fields.get_mut(key) else {
        return;
    };
    field.pending = false;
    match outcome {
        Ok(()) => {
            field.saved = draft;
            field.confirmed = true;
            field.error = None;
            field.saved_mark = Some(now + SAVED_MARK_SECONDS);
            tx.wake_at(now + SAVED_MARK_SECONDS);
        }
        Err(reason) => {
            field.error = Some(reason);
            field.confirmed = false;
        }
    }
    tx.instant();
    let refused = field.error.is_some();
    let requested = field.requested.take();
    if refused {
        cause.show(refusal_key(key), tx);
    }
    if let Some(requested) = requested {
        send(state, key, requested, tx);
    }
    super::flushed(state, tx);
}

/// Commits every field: before the dialog closes or changes page.
pub(crate) fn flush(state: &mut State, tx: &mut Tx) {
    for key in autosave(state).keys() {
        commit(state, &key, tx);
    }
}
