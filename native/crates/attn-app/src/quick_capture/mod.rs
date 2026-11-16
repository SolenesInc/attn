//! Quick Capture: a panel that opens over whatever app is in front, where the person writes a
//! message, attaches files and sends it to the chief or a crew member of the profile the app
//! shows. The panel is a window of its own, with its own state, bound to the process's store;
//! the main window opens it and hands it the daemon's answers to its requests.

mod attach;
pub mod automation;
mod draft;
mod history;
#[cfg(target_os = "macos")]
pub mod mac;
mod previews;
pub(crate) mod shortcut;
pub use shortcut::{DEFAULT as DEFAULT_SHORTCUT, stored as stored_shortcut};
mod view;

use std::path::PathBuf;
use std::sync::Arc;

use attn_client::{Commands, Connection, Facts, Reply, Slices, Store, Subscription};
use attn_protocol::{
    QuickCaptureGetMessage, QuickCaptureMailbox, QuickCaptureRecord, QuickCaptureResultMessage,
    QuickCaptureSendMessage,
};
use rune::{AppSpec, Key, KeyEvent, TextEdit, Tx};
use serde::Serialize;

use crate::State;
use crate::requests::{Answer, Requests};

pub(crate) use attach::Attachment;
use draft::{Folder, Stored, StoredFile};
pub(crate) use history::History;

/// The mailbox a new draft goes to: the profile's chief, the roster's first entry.
pub(crate) const CHIEF: &str = "chief";
/// The panel's size in points, as the Tauri app's.
pub const SIZE: (f32, f32) = (560.0, 390.0);
/// The setting that stores the global shortcut; empty turns it off.
pub const SHORTCUT_SETTING: &str = "capture.shortcut";

/// A change for the panel's state, made off its thread and run on it.
pub type Update = Box<dyn FnOnce(&mut Panel, &mut Tx) + Send>;

/// The window the panel shows in: the platform's floating panel in the app, a recorded window
/// in a wire test.
pub trait Window: Send + Sync {
    /// Brings the panel over the app in front, with the keyboard, leaving that app in front.
    fn show(&self);
    /// Hides the panel and gives the keyboard back to the app that had it.
    fn hide(&self);
    /// Runs `update` on the panel's state.
    fn update(&self, update: Update);
    /// Has the system open the panel on `shortcut` from any app, in place of the shortcut it
    /// held, or on none. A shortcut the system refuses leaves the previous one held.
    fn bind(&self, shortcut: Option<&rune::Shortcut>) -> Result<(), String>;
}

/// Which of its two faces the panel shows.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum Face {
    #[default]
    Compose,
    Recent,
}

/// What the panel waits on the daemon for, during which the draft cannot change.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum Busy {
    #[default]
    Idle,
    /// Sending the draft, or removing a file from it.
    Saving,
    /// Asking whether a send whose answer was lost was saved.
    Checking,
}

/// The draft being written, for the profile it belongs to.
#[derive(Default)]
pub(crate) struct Draft {
    pub profile: String,
    pub id: String,
    pub text: TextEdit,
    /// The mailbox chosen, or `CHIEF` for the roster's first.
    pub mailbox: String,
    pub files: Vec<Attachment>,
    /// The draft was handed to the daemon and no answer said whether the daemon kept it.
    pub uncertain: bool,
    /// The draft on disk has been read; until then nothing may change it.
    pub restored: bool,
}

impl Draft {
    fn fresh(profile: &str) -> Draft {
        Draft {
            profile: profile.to_owned(),
            id: uuid::Uuid::new_v4().to_string(),
            mailbox: CHIEF.to_owned(),
            restored: true,
            ..Draft::default()
        }
    }

    fn stored(&self) -> Stored {
        Stored {
            id: self.id.clone(),
            text: self.text.text.clone(),
            mailbox: self.mailbox.clone(),
            files: self
                .files
                .iter()
                .filter(|f| f.ready())
                .map(|f| StoredFile {
                    id: f.id.clone(),
                    name: f.name.clone(),
                })
                .collect(),
            uncertain: self.uncertain,
        }
    }
}

/// One entry of the roster the panel sends to.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Mailbox {
    pub id: String,
    pub name: String,
    pub in_session: bool,
}

/// A save that waits for the person to pause: one second after the last edit, and no later
/// than five seconds after the first edit it holds.
#[derive(Clone, Copy, Debug)]
struct Due {
    first: f64,
    at: f64,
}

#[derive(Default)]
pub struct Panel {
    pub(crate) facts: Facts,
    daemon: Commands,
    requests: Requests<Panel>,
    window: Option<Arc<dyn Window>>,
    data_dir: PathBuf,
    pub(crate) shown: bool,
    pub(crate) face: Face,
    pub(crate) draft: Draft,
    pub(crate) busy: Busy,
    /// The mailbox the last send was saved for, until the draft changes.
    pub(crate) saved_for: Option<String>,
    pub(crate) error: Option<String>,
    /// The roster entry the open mailbox list highlights.
    pub(crate) picking: Option<usize>,
    pub(crate) history: History,
    save: Option<Due>,
    /// Files being copied into a draft's folder, of any profile.
    copies: Vec<attach::Copy>,
    /// What runs once the daemon holds every file of the draft: the send that waits on them.
    after_staging: Option<fn(&mut Panel, &mut Tx)>,
    /// A file is dragged over the panel.
    pub(crate) dropping: bool,
    /// The panel's window holds the keyboard.
    pub(crate) key_window: bool,
    pub(crate) previews: previews::Previews,
    /// Wall-clock milliseconds when the panel's clock read zero.
    epoch_ms: i64,
    /// The user's calendar day when the panel last drew.
    pub(crate) today: i64,
}

const READS: Slices = Slices::CONNECTION
    .union(Slices::PROFILES)
    .union(Slices::CREW)
    .union(Slices::SETTINGS)
    .union(Slices::CAPTURE_READS);

/// Binds the panel to the store: it sends on `daemon`, keeps drafts under `data_dir`, hides
/// through `window`, reads the wall clock as `epoch_ms` when its clock reads zero, and hears
/// every change to what it reads through `deliver`.
pub fn bind(
    store: &Store,
    daemon: Commands,
    window: Arc<dyn Window>,
    data_dir: PathBuf,
    epoch_ms: i64,
    deliver: impl Fn(Update) + Send + Sync + 'static,
) -> Subscription {
    let deliver = Arc::new(deliver);
    let woken = deliver.clone();
    let wake: rune::Waker = Arc::new(move || woken(Box::new(|_, _| {})));
    deliver(Box::new(move |panel, _| {
        panel.previews.start(wake);
        panel.epoch_ms = epoch_ms;
        panel.daemon = daemon;
        panel.window = Some(window);
        panel.data_dir = data_dir;
    }));
    store.subscribe(READS, move |facts, _| {
        let facts = facts.clone();
        deliver(Box::new(move |panel, tx| {
            panel.facts = facts;
            follow(panel, tx);
        }));
    })
}

/// The panel as its window runs it, and as a wire test runs it.
pub fn app() -> rune::App<Panel> {
    rune::App::new(AppSpec {
        on_key: Some(on_key),
        poll: Some(poll),
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        min_size: Some(SIZE),
        ..AppSpec::new("Quick Capture", SIZE, Panel::default, view::view)
    })
}

/// Opens the panel: the window comes over the app in front, then the panel greets the person
/// with the message field.
pub(crate) fn open(state: &State) {
    if let Some(window) = &state.capture {
        window.show();
        window.update(Box::new(opened));
    }
}

/// Hands the panel the replies no window of the main app claimed.
pub(crate) fn hand(state: &State, replies: &[Reply]) {
    if let (Some(window), false) = (&state.capture, replies.is_empty()) {
        let replies = replies.to_vec();
        window.update(Box::new(move |panel, tx| {
            panel.requests.inbox.put(replies);
            answer(panel, tx);
        }));
    }
}

impl Panel {
    /// Whether files are still being copied into the draft's folder, off the UI thread.
    pub fn copying(&self) -> bool {
        self.copies.iter().any(attach::Copy::running)
    }

    /// Puts `next` in the draft's place and returns the draft it replaced. The message field
    /// keeps its model, which holds whether it has the keyboard.
    fn replace_draft(&mut self, mut next: Draft) -> Draft {
        let mut field = std::mem::take(&mut self.draft.text);
        let replaced = TextEdit::new(field.text.clone());
        field.replace_text(std::mem::take(&mut next.text.text));
        next.text = field;
        let mut previous = std::mem::replace(&mut self.draft, next);
        previous.text = replaced;
        previous
    }

    pub(crate) fn connected(&self) -> bool {
        self.facts.connection == Connection::Connected && !self.facts.profiles.selected.is_empty()
    }

    /// The selected profile's chief first, then its other members.
    pub(crate) fn roster(&self) -> Vec<Mailbox> {
        let profile = &self.facts.profiles.selected;
        let mut members: Vec<_> = self
            .facts
            .crew
            .iter()
            .filter(|m| &m.profile_id == profile)
            .collect();
        members.sort_by_key(|m| !m.chief.unwrap_or(false));
        members
            .into_iter()
            .map(|m| Mailbox {
                id: m.id.clone(),
                name: m.name.clone(),
                in_session: m.binding_session.is_some(),
            })
            .collect()
    }

    /// The mailbox the draft goes to: the one chosen, or the roster's first for `CHIEF`.
    pub(crate) fn mailbox(&self) -> String {
        if self.draft.mailbox == CHIEF {
            self.roster()
                .first()
                .map_or_else(|| CHIEF.to_owned(), |m| m.id.clone())
        } else {
            self.draft.mailbox.clone()
        }
    }

    pub(crate) fn label(&self, id: &str) -> String {
        self.roster()
            .into_iter()
            .find(|m| m.id == id)
            .map(|m| m.name)
            .unwrap_or_else(|| {
                if id == CHIEF {
                    "Chief".to_owned()
                } else {
                    id.to_owned()
                }
            })
    }

    /// Whether the draft can change now: read from disk, not being sent or checked.
    pub(crate) fn editable(&self) -> bool {
        self.draft.restored && !self.draft.uncertain && self.busy == Busy::Idle
    }

    pub(crate) fn can_send(&self) -> bool {
        self.draft.restored
            && self.busy == Busy::Idle
            && self.draft.files.iter().all(Attachment::ready)
            && (!self.draft.text.text.trim().is_empty() || !self.draft.files.is_empty())
    }

    /// Sends `command`, whose answer names `id`, and runs `then` with that answer on the panel.
    fn ask(
        &mut self,
        command: impl FnOnce(String) -> Message,
        then: impl FnOnce(&mut Panel, Answer, &mut Tx) + 'static,
        tx: &mut Tx,
    ) {
        let id = format!("native-{}", uuid::Uuid::new_v4());
        let mut message = command(id.clone());
        message.set_profile(&self.draft.profile);
        let sent = match &message {
            Message::Send(m) => self.daemon.send(m),
            Message::Get(m) => self.daemon.send(m),
            Message::List(m) => self.daemon.send(m),
            Message::Put(m) => self.daemon.send(m),
            Message::Discard(m) => self.daemon.send(m),
            Message::Download(m) => self.daemon.send(m),
        };
        match sent {
            Ok(()) => self.requests.wait(id, &message, Box::new(then)),
            Err(_) => then(self, Answer::NotSent, tx),
        }
    }

    fn folder(&self) -> Result<Folder, String> {
        Folder::of(&self.data_dir, &self.draft.profile)
    }

    /// Holds a save of the draft until the person pauses.
    pub(crate) fn edited(&mut self, tx: &mut Tx) {
        self.saved_for = None;
        let now = tx.clock();
        let first = self.save.map_or(now, |due| due.first);
        let at = now + (5.0 - (now - first)).clamp(0.0, 1.0);
        self.save = Some(Due { first, at });
        tx.wake_at(at);
    }

    /// Writes the draft now, whether or not a save waits.
    fn write(&mut self) -> Result<(), String> {
        self.save = None;
        self.folder()?.write(&self.draft.stored())
    }

    /// Writes a save that waits, now.
    pub(crate) fn flush(&mut self) {
        if self.save.is_some()
            && let Err(why) = self.write()
        {
            self.error = Some(format!("Draft could not be saved: {why}"));
        }
    }

    pub(crate) fn hide(&mut self) {
        self.flush();
        if let Some(window) = &self.window {
            window.hide();
        }
        hidden(self);
    }
}

/// The commands the panel sends, each answered by a `quick_capture_result`.
#[derive(Serialize)]
#[serde(untagged)]
enum Message {
    Send(QuickCaptureSendMessage),
    Get(QuickCaptureGetMessage),
    List(attn_protocol::QuickCaptureListMessage),
    Put(attn_protocol::QuickCaptureAttachmentPutMessage),
    Discard(attn_protocol::QuickCaptureAttachmentDiscardMessage),
    Download(attn_protocol::QuickCaptureAttachmentGetMessage),
}

impl Message {
    fn set_profile(&mut self, profile: &str) {
        let field = match self {
            Message::Send(m) => &mut m.profile_id,
            Message::Get(m) => &mut m.profile_id,
            Message::List(m) => &mut m.profile_id,
            Message::Put(m) => &mut m.profile_id,
            Message::Discard(m) => &mut m.profile_id,
            Message::Download(m) => &mut m.profile_id,
        };
        *field = Some(profile.to_owned());
    }
}

/// The result an answer carries when the daemon did what was asked, or why it did not.
pub(crate) fn result(answer: Answer) -> Result<attn_protocol::QuickCaptureResult, Failure> {
    let reply = match answer {
        Answer::Reply(reply) => reply,
        Answer::NotSent | Answer::Dropped => return Err(Failure::Lost),
    };
    let message: Option<QuickCaptureResultMessage> = reply.parse();
    match message {
        Some(m) if m.success => Ok(m.result.unwrap_or_default()),
        Some(m) => Err(Failure::Refused {
            code: m.error_code.unwrap_or_default(),
            error: m
                .error
                .filter(|e| !e.is_empty())
                .unwrap_or_else(|| "Quick capture request failed".into()),
        }),
        None => Err(Failure::Refused {
            code: String::new(),
            error: reply.body["error"]
                .as_str()
                .unwrap_or("Quick capture request failed")
                .to_owned(),
        }),
    }
}

/// Why a request did not do what it asked.
pub(crate) enum Failure {
    /// The connection ended before the answer, or the request never left.
    Lost,
    Refused {
        code: String,
        error: String,
    },
}

impl Failure {
    pub(crate) fn say(&self) -> String {
        match self {
            Failure::Lost => "Quick Capture is not connected. Your draft is retained.".into(),
            Failure::Refused { error, .. } => error.clone(),
        }
    }
}

/// Follows the facts: the selected profile's draft, and a send to check once connected.
fn follow(panel: &mut Panel, tx: &mut Tx) {
    let profile = panel.facts.profiles.selected.clone();
    if !profile.is_empty() && profile != panel.draft.profile {
        panel.flush();
        restore(panel, &profile, tx);
    }
    if !panel.connected() {
        attach::disconnected(panel);
        for then in panel.requests.unanswerable() {
            then(panel, Answer::Dropped, tx);
        }
    }
    check(panel, tx);
    attach::stage(panel, tx);
    history::follow(panel, tx);
}

/// Reads `profile`'s draft from disk, or starts a fresh one. What the previous profile asked
/// the daemon is forgotten: its draft waits on disk, and a send whose answer it misses is
/// resolved by id when the person returns to it.
fn restore(panel: &mut Panel, profile: &str, tx: &mut Tx) {
    drop(panel.requests.unanswerable());
    panel.busy = Busy::Idle;
    panel.after_staging = None;
    panel.replace_draft(Draft::fresh(profile));
    panel.draft.restored = false;
    panel.face = Face::Compose;
    panel.picking = None;
    panel.saved_for = None;
    panel.error = None;
    panel.history = History::default();
    let copying = attach::copying_into(panel, profile);
    match panel
        .folder()
        .and_then(|f| f.read(&copying).map(|d| (f, d)))
    {
        Ok((folder, Some(stored))) => {
            panel.draft.id = stored.id;
            panel.draft.text.replace_text(stored.text);
            panel.draft.mailbox = stored.mailbox;
            panel.draft.uncertain = stored.uncertain;
            panel.draft.restored = true;
            panel.draft.files = attach::restored(panel, &folder, stored.files);
        }
        Ok((_, None)) => panel.draft.restored = true,
        Err(why) => panel.error = Some(format!("Cannot restore draft: {why}")),
    }
    let _ = tx;
}

/// The window came over the app in front.
fn opened(panel: &mut Panel, tx: &mut Tx) {
    panel.shown = true;
    panel.face = Face::Compose;
    panel.picking = None;
    tx.focus(view::MESSAGE, false);
    check(panel, tx);
}

fn hidden(panel: &mut Panel) {
    panel.shown = false;
    panel.face = Face::Compose;
    panel.picking = None;
}

/// Sends the draft, or, when an earlier send's answer was lost, first asks whether the daemon
/// kept it.
pub(crate) fn send(panel: &mut Panel, tx: &mut Tx) {
    if !panel.can_send() {
        return;
    }
    if !panel.connected() {
        panel.error = Some("Quick Capture is not connected. Your draft is retained.".into());
        return;
    }
    panel.busy = Busy::Saving;
    if panel.draft.uncertain {
        let capture = panel.draft.id.clone();
        panel.ask(
            |request_id| get(capture, request_id),
            |panel, answer, tx| match found(answer) {
                Ok(Some(record)) => accept(panel, record, true, tx),
                Ok(None) => {
                    panel.draft.uncertain = false;
                    submit(panel, tx);
                }
                Err(failure) => fail(panel, failure, tx),
            },
            tx,
        );
    } else {
        submit(panel, tx);
    }
}

/// Stages the draft's files, marks the draft uncertain on disk, then hands it to the daemon.
fn submit(panel: &mut Panel, tx: &mut Tx) {
    panel.draft.uncertain = false;
    if let Err(why) = panel.write() {
        return fail_with(panel, format!("Draft could not be saved: {why}"));
    }
    if !attach::staged(panel) {
        return attach::stage_then(panel, tx, submit);
    }
    panel.draft.uncertain = true;
    if let Err(why) = panel.write() {
        panel.draft.uncertain = false;
        return fail_with(panel, format!("Draft could not be saved: {why}"));
    }
    let draft = &panel.draft;
    let message = QuickCaptureSendMessage {
        capture_id: draft.id.clone(),
        mailbox: QuickCaptureMailbox {
            kind: "crew_member".into(),
            member_id: Some(panel.mailbox()),
            session_id: None,
        },
        content: draft.text.text.clone(),
        attachment_ids: draft.files.iter().map(|f| f.id.clone()).collect(),
        ..QuickCaptureSendMessage::new()
    };
    panel.ask(
        |request_id| {
            Message::Send(QuickCaptureSendMessage {
                request_id: Some(request_id),
                ..message
            })
        },
        |panel, answer, tx| match result(answer) {
            Ok(attn_protocol::QuickCaptureResult {
                record: Some(record),
                ..
            }) => accept(panel, record, true, tx),
            Ok(_) => fail_with(
                panel,
                "The daemon did not return a saved quick capture receipt. Your draft is retained."
                    .into(),
            ),
            Err(failure) => fail(panel, failure, tx),
        },
        tx,
    );
}

fn fail(panel: &mut Panel, failure: Failure, tx: &mut Tx) {
    fail_with(panel, failure.say());
    check(panel, tx);
}

fn fail_with(panel: &mut Panel, error: String) {
    panel.busy = Busy::Idle;
    panel.error = Some(error);
}

fn get(capture: String, request_id: String) -> Message {
    Message::Get(QuickCaptureGetMessage {
        request_id: Some(request_id),
        capture_id: capture,
        ..QuickCaptureGetMessage::new()
    })
}

/// The record a `quick_capture_get` found, or none when the daemon never kept the capture.
fn found(answer: Answer) -> Result<Option<QuickCaptureRecord>, Failure> {
    match result(answer) {
        Ok(result) => Ok(result.record),
        Err(Failure::Refused { code, .. }) if code == "quick_capture_not_found" => Ok(None),
        Err(failure) => Err(failure),
    }
}

/// While a send's answer is unknown and the panel is connected, asks whether the daemon kept
/// it: a kept send clears the draft, and one never kept is the person's to send again.
fn check(panel: &mut Panel, tx: &mut Tx) {
    if !(panel.draft.restored
        && panel.draft.uncertain
        && panel.busy == Busy::Idle
        && panel.connected())
    {
        return;
    }
    panel.busy = Busy::Checking;
    let capture = panel.draft.id.clone();
    panel.ask(
        |request_id| get(capture, request_id),
        |panel, answer, tx| {
            panel.busy = Busy::Idle;
            match found(answer) {
                Ok(Some(record)) => accept(panel, record, false, tx),
                Ok(None) => {
                    panel.draft.uncertain = false;
                    if let Err(why) = panel.write() {
                        panel.error = Some(format!("Draft could not be saved: {why}"));
                    } else {
                        panel.error =
                            Some("Not yet saved. You can edit or send this draft again.".into());
                    }
                }
                Err(failure) => panel.error = Some(failure.say()),
            }
        },
        tx,
    );
}

/// The daemon kept the capture: a fresh draft takes its place, and the panel hides when the
/// person sent it.
fn accept(panel: &mut Panel, record: QuickCaptureRecord, hide: bool, tx: &mut Tx) {
    let profile = panel.draft.profile.clone();
    panel.replace_draft(Draft::fresh(&profile));
    panel.busy = Busy::Idle;
    panel.error = None;
    if let Err(why) = panel.write() {
        panel.error = Some(format!("Draft could not be saved: {why}"));
    }
    panel.saved_for = record.mailbox.member_id.clone();
    history::sent(panel, tx);
    if hide && panel.shown {
        panel.hide();
    }
}

/// Starts a new draft in place of this one, and has the daemon forget the files it held.
pub(crate) fn discard(panel: &mut Panel, tx: &mut Tx) {
    if panel.busy != Busy::Idle {
        return;
    }
    if panel.draft.uncertain {
        panel.error = Some(
            "Resolve this submission before discarding it. Use Retry to check whether it was saved."
                .into(),
        );
        return;
    }
    let profile = panel.draft.profile.clone();
    let discarded = panel.replace_draft(Draft::fresh(&profile));
    match panel.write() {
        Ok(()) => {
            panel.error = None;
            attach::forget(panel, &discarded, tx);
        }
        Err(why) => {
            panel.replace_draft(discarded);
            panel.error = Some(format!("Cannot clear draft: {why}"));
        }
    }
}

pub(crate) fn pick(panel: &mut Panel, mailbox: String, tx: &mut Tx) {
    if !panel.editable() {
        return;
    }
    panel.draft.mailbox = mailbox;
    panel.picking = None;
    panel.edited(tx);
    tx.focus(view::MESSAGE, false);
}

pub(crate) fn toggle_picker(panel: &mut Panel) {
    if !panel.editable() {
        return;
    }
    panel.face = Face::Compose;
    panel.picking = match panel.picking {
        Some(_) => None,
        None => {
            let mailbox = panel.mailbox();
            Some(
                panel
                    .roster()
                    .iter()
                    .position(|m| m.id == mailbox)
                    .unwrap_or(0),
            )
        }
    };
}

fn on_key(panel: &mut Panel, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let mods = ev.mods;
    if let (Some(at), false) = (panel.picking, mods.cmd || mods.ctrl || mods.alt) {
        let count = panel.roster().len();
        let step = |by: usize| (at + by) % count.max(1);
        match &ev.key {
            Key::Down => panel.picking = Some(step(1)),
            Key::Up => panel.picking = Some(step(count.max(1) - 1)),
            Key::Home => panel.picking = Some(0),
            Key::End => panel.picking = Some(count.saturating_sub(1)),
            Key::Enter | Key::Text(_)
                if ev.key == Key::Enter || ev.key == Key::Text(" ".into()) =>
            {
                if let Some(m) = panel.roster().get(at) {
                    pick(panel, m.id.clone(), tx);
                }
            }
            Key::Tab => panel.picking = None,
            Key::Escape => panel.picking = None,
            _ => return false,
        }
        return true;
    }
    if shortcut(panel, ev, tx) || text_size(panel, ev, tx) {
        return true;
    }
    if ev.key == Key::Escape {
        match panel.face {
            Face::Recent => history::close(panel, tx),
            Face::Compose => panel.hide(),
        }
        return true;
    }
    if panel.face == Face::Recent && !(mods.cmd || mods.ctrl || mods.alt || mods.shift) {
        match ev.key {
            Key::Up => history::step(panel, -1),
            Key::Down => history::step(panel, 1),
            _ => return false,
        }
        return true;
    }
    false
}

/// The app's text size keys, which size the panel with the app.
fn text_size(panel: &mut Panel, ev: &KeyEvent, tx: &mut Tx) -> bool {
    use crate::action::Action;
    use crate::keymap::{self, Binding, Keymap};
    let setting = panel
        .facts
        .settings
        .get(keymap::SETTING)
        .map(String::as_str);
    let keymap = Keymap::from_setting(setting, rune::KeyPlatform::current());
    let pressed = [
        Action::UiIncreaseFontSize,
        Action::UiDecreaseFontSize,
        Action::UiResetFontSize,
    ]
    .into_iter()
    .find(|action| {
        matches!(keymap.binding(action), Some(Binding::Chord(chord)) if keymap.matches(&chord, ev, tx))
    });
    let Some(action) = pressed else {
        return false;
    };
    if let Some(message) =
        crate::settings::appearance::ui_scale_step(&panel.facts.settings, &action)
    {
        let _ = panel.daemon.send(&message);
    }
    true
}

/// ⌘W hides the panel; ⌘1 to ⌘4 pick the roster's first four mailboxes and ⌘K opens the list, before a field's own
/// keys do.
fn shortcut(panel: &mut Panel, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let only_cmd = ev.mods.cmd && !ev.mods.alt && !ev.mods.shift;
    if let Key::Command(typed) = ev.key
        && only_cmd
    {
        if let Some(digit @ 1..=4) = typed.to_digit(10) {
            if let Some(m) = panel.roster().get(digit as usize - 1) {
                pick(panel, m.id.clone(), tx);
            }
            panel.picking = None;
            return true;
        }
        if typed == 'k' {
            toggle_picker(panel);
            return true;
        }
        if typed == 'w' {
            panel.hide();
            return true;
        }
    }
    false
}

/// The message field's keys: the panel's own first, then Enter sends and Shift+Enter breaks
/// the line.
pub(crate) fn on_message_key(panel: &mut Panel, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if on_key(panel, ev, tx) {
        return true;
    }
    if ev.key == Key::Enter && !ev.mods.shift && !ev.mods.cmd && !ev.mods.ctrl && !ev.mods.alt {
        send(panel, tx);
        return true;
    }
    false
}

/// Runs what waited on each reply the panel was handed.
fn answer(panel: &mut Panel, tx: &mut Tx) -> bool {
    let mut changed = false;
    for reply in panel.requests.inbox.take() {
        if let Some(then) = panel.requests.answered_by(&reply) {
            then(panel, Answer::Reply(reply), tx);
            changed = true;
        }
    }
    changed
}

fn poll(panel: &mut Panel, now: f64, tx: &mut Tx) -> bool {
    let today = crate::calendar::local_day(panel.epoch_ms + (now * 1000.0) as i64);
    let mut changed = std::mem::replace(&mut panel.today, today) != today;
    changed |= answer(panel, tx);
    if panel.save.is_some_and(|due| now >= due.at) {
        panel.flush();
        changed = true;
    }
    if let Some(due) = panel.save {
        tx.wake_at(due.at);
    }
    changed | previews::follow(panel)
}
