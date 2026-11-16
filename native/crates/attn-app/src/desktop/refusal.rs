//! What a tile shows when the daemon refuses input sent to its terminal, as the Tauri app's
//! `desktop-pane-refusal` flash and the keys its offline strip counts.

use std::collections::HashMap;

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Beat, Easing, El, Sequence, Tx, el, row, text};

use crate::State;

const FLASH_SECONDS: f64 = 0.55;
const FLASH_ALPHA: f32 = 0.22;
const FLASH_BORDER_PX: f32 = 2.0;
const FADE: &[Beat] = &[
    Beat::new(0.0).opacity(1.0),
    Beat::new(FLASH_SECONDS)
        .opacity(0.0)
        .easing(Easing::EASE_OUT),
];

#[derive(Clone, Copy, Debug, Default, PartialEq)]
struct Refusal {
    keys_not_sent: u32,
    flashing_until: Option<f64>,
}

impl Refusal {
    fn settled(&self) -> bool {
        self.keys_not_sent == 0 && self.flashing_until.is_none()
    }
}

/// Each terminal's refused input, by runtime id, until its flash ends and a typed key goes
/// through.
#[derive(Default)]
pub(crate) struct Refusals(HashMap<String, Refusal>);

impl Refusals {
    /// Typed input reached the daemon for `terminal`: the keys it refused before are no longer
    /// the last word.
    pub(crate) fn sent(&mut self, terminal: &str) {
        if let Some(refusal) = self.0.get_mut(terminal) {
            refusal.keys_not_sent = 0;
            if refusal.settled() {
                self.0.remove(terminal);
            }
        }
    }

    fn expire(&mut self, now: f64) -> bool {
        let mut changed = false;
        for refusal in self.0.values_mut() {
            if refusal.flashing_until.is_some_and(|until| until <= now) {
                refusal.flashing_until = None;
                changed = true;
            }
        }
        self.0.retain(|_, r| !r.settled());
        changed
    }

    /// What the tiles show of the refusals, for the tile area's memo.
    pub(crate) fn keys(&self) -> Vec<(String, u32, Option<u64>)> {
        let mut keys: Vec<_> = self
            .0
            .iter()
            .map(|(terminal, r)| {
                (
                    terminal.clone(),
                    r.keys_not_sent,
                    r.flashing_until.map(f64::to_bits),
                )
            })
            .collect();
        keys.sort();
        keys
    }

    /// The flash over `terminal`'s slot while it plays, keyed by when it ends so a refusal
    /// during a flash plays it again from the start.
    pub(crate) fn flash(&self, terminal: &str, t: &Tokens) -> Option<El> {
        let until = self.0.get(terminal)?.flashing_until?;
        Some(
            el().key(format!("tile.refused.{terminal}.flash.{}", until.to_bits()))
                .pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
                .paint_order(2)
                .inert()
                .fill(t.error.alpha(FLASH_ALPHA))
                .border(FLASH_BORDER_PX, t.error)
                .transition(rune::Transition::none())
                .sequence(Sequence::new(FADE)),
        )
    }

    /// The strip under `terminal` counting the keys the daemon refused, while there are any.
    pub(crate) fn strip(&self, terminal: &str, t: &Tokens) -> Option<El> {
        let keys = self.0.get(terminal)?.keys_not_sent;
        let said = match keys {
            0 => return None,
            1 => "1 key may not have been sent".to_owned(),
            n => format!("{n} keys may not have been sent"),
        };
        Some(
            row()
                .key(format!("tile.refused.{terminal}"))
                .role("status")
                .label(said.clone())
                .w_full()
                .px(space::S2)
                .py(space::S1)
                .fill(t.bg_panel)
                .child(
                    text(said)
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .single_line()
                        .truncate(),
                ),
        )
    }
}

/// A `command_error` for input or a resize sent to a terminal this window shows: its tile
/// flashes, and refused keys count under it. Whether the reply was one.
pub(crate) fn receive(state: &mut State, reply: &attn_client::Reply, tx: &mut Tx) -> bool {
    if reply.event != "command_error" {
        return false;
    }
    let keys = match reply.body["cmd"].as_str() {
        Some("pty_input") => 1,
        Some("pty_resize") => 0,
        _ => return false,
    };
    let Some(terminal) = reply.body["terminal_id"].as_str() else {
        return false;
    };
    if !state.terminals.shows(terminal) {
        return false;
    }
    let until = tx.clock() + FLASH_SECONDS;
    let refusal = state
        .window
        .desk
        .refusals
        .0
        .entry(terminal.to_owned())
        .or_default();
    refusal.keys_not_sent += keys;
    refusal.flashing_until = Some(until);
    tx.wake_at(until);
    true
}

pub(super) fn expire(state: &mut State, now: f64) -> bool {
    state.window.desk.refusals.expire(now)
}
