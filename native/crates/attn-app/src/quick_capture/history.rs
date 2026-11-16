//! Recent messages: the profile's sent captures, a page at a time, each marked Read once its
//! agent retrieved it from its inbox.

use attn_protocol::{QuickCaptureListMessage, QuickCaptureRecord};
use std::collections::HashMap;

use rune::Tx;

use super::previews::{self, Sent};
use super::{Face, Message, Panel, result, view};

/// Captures a page of history lists, as the Tauri app asks for.
const PAGE: i64 = 4;

#[derive(Default)]
pub(crate) struct History {
    /// The captures listed so far, newest first, as the daemon pages them.
    items: Vec<QuickCaptureRecord>,
    pub next_cursor: Option<String>,
    pub loading: bool,
    pub error: Option<String>,
    pub selected: Option<String>,
    /// Moves when the history opens or closes, so an answer to an earlier opening is dropped.
    pub(super) generation: u64,
    /// The history opened and has not asked for its first page yet.
    unread: bool,
    /// A refresh was asked for while one ran.
    again: bool,
    /// The pictures of the images the listed messages sent, by capture and attachment.
    pub images: HashMap<(String, String), Sent>,
}

impl History {
    /// The captures oldest first, as the history shows them.
    pub(crate) fn ordered(&self) -> Vec<&QuickCaptureRecord> {
        let mut items: Vec<_> = self.items.iter().collect();
        items.sort_by(|a, b| {
            a.created_at
                .cmp(&b.created_at)
                .then_with(|| a.id.cmp(&b.id))
        });
        items
    }
}

impl Panel {
    /// When the capture's agent retrieved it, as the daemon last said.
    pub(crate) fn read_at(&self, record: &QuickCaptureRecord) -> Option<String> {
        match self.facts.capture_reads.get(&record.id) {
            Some(read) => read.clone(),
            None => record.read_at.clone(),
        }
    }
}

pub(crate) fn open(panel: &mut Panel, tx: &mut Tx) {
    panel.face = Face::Recent;
    panel.picking = None;
    let generation = panel.history.generation + 1;
    panel.history = History {
        generation,
        unread: true,
        ..History::default()
    };
    follow(panel, tx);
}

pub(crate) fn close(panel: &mut Panel, tx: &mut Tx) {
    panel.face = Face::Compose;
    panel.history.generation += 1;
    panel.history.again = false;
    panel.history.images.clear();
    tx.focus(view::MESSAGE, false);
}

/// Asks for the first page once the open history can.
pub(super) fn follow(panel: &mut Panel, tx: &mut Tx) {
    if panel.face == Face::Recent && panel.history.unread && panel.connected() {
        panel.history.unread = false;
        refresh(panel, None, tx);
    }
}

/// A send was saved: the newest page shows it.
pub(super) fn sent(panel: &mut Panel, tx: &mut Tx) {
    if panel.connected() {
        refresh(panel, None, tx);
    }
}

/// Shows the page before the oldest shown.
pub(crate) fn older(panel: &mut Panel, tx: &mut Tx) {
    if let Some(cursor) = panel.history.next_cursor.clone() {
        refresh(panel, Some(cursor), tx);
    }
}

/// Lists the newest page again, or with `cursor` the page after it. A refresh of the open
/// history pages on until it reaches the oldest capture already shown, so nothing shown drops
/// out.
fn refresh(panel: &mut Panel, cursor: Option<String>, tx: &mut Tx) {
    if panel.history.loading {
        panel.history.again |= cursor.is_none();
        return;
    }
    let oldest = (cursor.is_none() && panel.face == Face::Recent)
        .then(|| panel.history.items.last().map(|r| r.id.clone()))
        .flatten();
    panel.history.loading = true;
    let generation = panel.history.generation;
    page(
        panel,
        generation,
        cursor.clone(),
        cursor.is_some(),
        oldest,
        Vec::new(),
        tx,
    );
}

fn page(
    panel: &mut Panel,
    generation: u64,
    cursor: Option<String>,
    older: bool,
    oldest: Option<String>,
    mut listed: Vec<QuickCaptureRecord>,
    tx: &mut Tx,
) {
    let asked = cursor.clone();
    panel.ask(
        |request_id| {
            Message::List(QuickCaptureListMessage {
                request_id: Some(request_id),
                limit: PAGE,
                cursor: asked,
                ..QuickCaptureListMessage::new()
            })
        },
        move |panel, answer, tx| {
            let current = generation == panel.history.generation;
            let list = match result(answer) {
                Ok(result) => result
                    .list
                    .ok_or_else(|| "The daemon did not return quick capture history.".to_owned()),
                Err(failure) => Err(failure.say()),
            };
            let list = match list {
                Ok(list) => list,
                Err(why) => {
                    panel.history.loading = false;
                    if current {
                        panel.history.error = Some(why);
                    }
                    return;
                }
            };
            let reached = oldest
                .as_ref()
                .is_none_or(|id| listed.iter().chain(&list.items).any(|r| &r.id == id));
            listed.extend(list.items);
            if current
                && !reached
                && let Some(next) = list.next_cursor
            {
                return page(panel, generation, Some(next), older, oldest, listed, tx);
            }
            panel.history.loading = false;
            if current {
                let history = &mut panel.history;
                if older {
                    listed.retain(|r| !history.items.iter().any(|shown| shown.id == r.id));
                    history.items.extend(listed);
                } else {
                    history.items = listed;
                }
                history.next_cursor = list.next_cursor;
                history.error = None;
                if history.selected.is_none() {
                    history.selected = history.ordered().last().map(|r| r.id.clone());
                }
                previews::fetch(panel, tx);
            }
            if std::mem::take(&mut panel.history.again) && panel.face == Face::Recent {
                refresh(panel, None, tx);
            }
        },
        tx,
    );
}

/// Moves the selection `by` rows, staying within the list.
pub(super) fn step(panel: &mut Panel, by: isize) {
    let ordered: Vec<String> = panel
        .history
        .ordered()
        .iter()
        .map(|r| r.id.clone())
        .collect();
    if ordered.is_empty() {
        return;
    }
    let at = panel
        .history
        .selected
        .as_ref()
        .and_then(|id| ordered.iter().position(|o| o == id))
        .unwrap_or(0);
    let next = at.saturating_add_signed(by).min(ordered.len() - 1);
    panel.history.selected = Some(ordered[next].clone());
}
