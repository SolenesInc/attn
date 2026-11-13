//! A document tile's header controls for its marks, as the Tauri tile header has them: a note on
//! the whole document, the review notes, and sending them: a file's to the session the tile is
//! bound to or to another session on its desktop, a seed's to the session tending it or as a
//! note on the seed.

use attn_protocol::{Desktop, DesktopUpdateTileMessage, MarkdownAnnotation, Session};
use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{Cx, El, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, Menu, MenuItem, Placement};

use super::drafts::{self, Sent, To};
use crate::State;

/// How long "Sent ✓" shows before Send comes back, as the Tauri header shows it.
pub(crate) const SENT_SECONDS: f64 = 4.0;
/// How long "Clear all" waits for its confirmation.
const CLEAR_SECONDS: f64 = 3.0;
const MENU_W: f32 = 260.0;
const NOTES_W: f32 = 360.0;

fn key(tile: &str, part: &str) -> String {
    format!("tile.{tile}.document.{part}")
}

/// The sessions on `desktop` a tile on it can send to, in pane order.
fn desktop_sessions<'a>(desktop: &Desktop, sessions: &'a [Session]) -> Vec<&'a Session> {
    desktop
        .panes
        .iter()
        .filter_map(|p| sessions.iter().find(|s| s.id == p.session_id))
        .collect()
}

/// Where tile `tile` sends: the session picked here until the desktop says it is bound to it,
/// else the session it is bound to, while that session is on the desktop.
fn destination<'a>(
    readings: &super::Readings,
    desktop: &Desktop,
    sessions: &'a [Session],
    tile: &str,
    bound: Option<&str>,
) -> Option<&'a Session> {
    let picked = readings.tiles.get(tile).and_then(|r| r.picked.as_deref());
    let id = picked.or(bound)?;
    desktop_sessions(desktop, sessions)
        .into_iter()
        .find(|s| s.id == id)
}

/// Whose marks a document tile holds, and where they go.
pub(crate) enum Kind {
    /// A markdown file's, sent to the session the tile is bound to.
    File { bound: Option<String> },
    /// A seed's, sent to the session tending it, or as a note on the seed when none does.
    Seed { id: String, tender: Option<String> },
}

impl Kind {
    /// Seed `seed`'s, going to its tender while it is claimed.
    pub(crate) fn seed(seed: &attn_protocol::Seed) -> Kind {
        Kind::Seed {
            id: seed.id.clone(),
            tender: seed
                .claimed
                .then(|| seed.tender.as_ref().and_then(|t| t.session_id.clone()))
                .flatten()
                .filter(|s| !s.is_empty()),
        }
    }

    fn bound(&self) -> Option<&str> {
        match self {
            Kind::File { bound } => bound.as_deref(),
            Kind::Seed { .. } => None,
        }
    }
}

/// The header controls of document tile `tile` on `desktop` for its marks of document `uri`,
/// going where `kind` says; `send_key` names the send key.
#[allow(clippy::too_many_arguments)]
pub(crate) fn controls(
    (drafts, readings): (&super::Drafts, &super::Readings),
    sessions: &[Session],
    desktop: &Desktop,
    tile: &str,
    uri: &str,
    kind: &Kind,
    send_key: Option<&str>,
    t: &Tokens,
) -> El {
    let count = drafts.get(uri).map_or(0, |d| d.marks.len());
    let reading = readings.tiles.get(tile);
    let sent = reading.and_then(|r| r.sent.clone());
    let to = destination(readings, desktop, sessions, tile, kind.bound());
    let name = to.map_or_else(
        || "No session".to_owned(),
        crate::sidebar::model::display_name,
    );
    let any = !desktop_sessions(desktop, sessions).is_empty();
    let noting = matches!(kind, Kind::Seed { tender: None, .. });
    let small = |label: String, face: String| {
        Button::new(label)
            .variant(ButtonVariant::GhostMuted)
            .label_with(move |_, ink| text(face).font(type_::LABEL).color(ink))
    };
    let place = |button: Button, part: &str| {
        button
            .build(attn_ui::kit(t))
            .key(key(tile, part))
            .h(type_::LABEL.line_px() + 6.0)
            .px(space::S1)
            .radius(5.0)
    };
    let (overall_tile, overall_uri) = (tile.to_owned(), uri.to_owned());
    let notes_tile = tile.to_owned();
    let mut parts = vec![
        place(
            small("Overall note".into(), "Overall note".into())
                .help("Add an overall note")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::annotating::compose_overall(state, &overall_tile, &overall_uri, tx)
                }),
            "overall",
        ),
        place(
            small(
                format!("Notes {count}"),
                format!("{} {count}", attn_ui::icon::COMMENT),
            )
            .help("Show review notes")
            .on_press_tx(move |state: &mut State, tx: &mut Tx| open_notes(state, &notes_tile, tx)),
            "notes",
        ),
    ];
    let done = |noted: bool| if noted { "Noted ✓" } else { "Sent ✓" };
    let status = match &sent {
        Some(Sent::Done { kept, noted, .. }) if *kept > 0 => {
            Some(format!("{} — {kept} still here", done(*noted)))
        }
        Some(Sent::Done { noted, .. }) => Some(done(*noted).to_owned()),
        Some(Sent::Skipped) => Some("Target is waiting for approval — not sent".to_owned()),
        Some(Sent::Warning(why) | Sent::Failed(why)) => Some(why.clone()),
        Some(Sent::Sending) | None => None,
    };
    let caret = |label: &str| {
        let tile = tile.to_owned();
        place(
            small(label.to_owned(), attn_ui::icon::CARET_DOWN.into())
                .on_press(move |state: &mut State| toggle_menu(state, &tile)),
            "send.menu",
        )
    };
    let idle = count == 0 && sent.is_none();
    if idle && matches!(kind, Kind::File { .. }) {
        let tile_menu = tile.to_owned();
        parts.push(place(
            small(
                format!("Annotation destination: {name}"),
                format!("To {name} {}", attn_ui::icon::CARET_DOWN),
            )
            .help(format!("Annotations will be sent to {name}"))
            .disabled(!any)
            .on_press(move |state: &mut State| toggle_menu(state, &tile_menu)),
            "send.to",
        ));
    } else if !idle {
        let action = match &sent {
            Some(Sent::Sending) if noting => "Noting…".to_owned(),
            Some(Sent::Sending) => "Sending…".to_owned(),
            Some(Sent::Done { noted, .. }) => done(*noted).to_owned(),
            Some(Sent::Skipped) => "Approval needed".to_owned(),
            Some(Sent::Warning(_)) => "Needs attention".to_owned(),
            Some(Sent::Failed(_)) => "Send failed".to_owned(),
            None if noting => format!("Note on seed {count}"),
            None => format!("Send {count}"),
        };
        let quiet = matches!(sent, Some(Sent::Sending | Sent::Done { .. }));
        let problem = matches!(
            sent,
            Some(Sent::Skipped | Sent::Warning(_) | Sent::Failed(_))
        );
        let label = if quiet || matches!(kind, Kind::Seed { .. }) {
            action
        } else {
            format!("{action} to {name}")
        };
        let label = if problem { format!("! {label}") } else { label };
        let chord = send_key
            .map(|chord| format!(" ({chord})"))
            .unwrap_or_default();
        let title = match (kind, to) {
            _ if problem => status.clone().unwrap_or_default(),
            (
                Kind::Seed {
                    tender: Some(_), ..
                },
                _,
            ) => {
                format!("Send annotations to the tending session{chord}")
            }
            (Kind::Seed { tender: None, .. }, _) => {
                format!("Leave annotations as a note on the seed{chord}")
            }
            (Kind::File { .. }, Some(_)) => format!("Send annotations to {name}{chord}"),
            (Kind::File { .. }, None) => "Choose a session before sending annotations".to_owned(),
        };
        let nowhere = matches!(kind, Kind::File { .. }) && to.is_none();
        let disabled = matches!(sent, Some(Sent::Sending)) || count == 0 || nowhere;
        let send_tile = tile.to_owned();
        let face = label.clone();
        let button = place(
            Button::new(label)
                .variant(ButtonVariant::Outline)
                .label_with(move |_, ink| text(face).font(type_::LABEL).color(ink))
                .help(title)
                .disabled(disabled)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| send(state, &send_tile, tx)),
            "send",
        )
        .live_region(rune::LiveRegion::Polite);
        parts.push(match status {
            Some(status) => button.description(status),
            None => button,
        });
        match kind {
            Kind::File { .. } if any => parts.push(caret("Change annotation destination")),
            Kind::Seed {
                tender: Some(_), ..
            } => parts.push(caret("More annotation destinations")),
            _ => {}
        }
    }
    row()
        .key(key(tile, "controls"))
        .items_center()
        .gap(2.0)
        .children(parts)
}

/// A document tile: the desktop it is on, its document, and where its marks go.
struct Tiled<'a> {
    desktop: &'a Desktop,
    uri: String,
    kind: Kind,
}

/// Document tile `tile`, a markdown file's or a seed's, where a desktop shows it.
fn tile_of<'a>(state: &'a State, tile: &str) -> Option<Tiled<'a>> {
    if let Some((at, content)) = state
        .facts
        .tile_contents
        .tiles
        .iter()
        .find(|(k, c)| k.tile == tile && c.message.tile_kind == "markdown")
    {
        let desktop = state.facts.desktops.iter().find(|d| d.id == at.desktop)?;
        let uri = drafts::Source::File {
            path: content.message.path.clone(),
            host: content.message.host.clone().filter(|h| !h.is_empty()),
        }
        .uri();
        let bound = crate::desktop::tile_session(desktop, tile);
        return Some(Tiled {
            desktop,
            uri,
            kind: Kind::File { bound },
        });
    }
    state.facts.desktops.iter().find_map(|desktop| {
        let id = crate::sidebar::model::leaves(&desktop.tree_json)
            .into_iter()
            .find_map(|leaf| match leaf {
                crate::sidebar::model::Leaf::Tile { id, kind, params }
                    if id == tile && kind == "seed" && !params.is_empty() =>
                {
                    Some(params)
                }
                _ => None,
            })?;
        let seed =
            crate::garden::shown_seed(&state.window.garden, crate::garden::seeds(state), &id)?;
        Some(Tiled {
            desktop,
            uri: drafts::Source::Seed(id).uri(),
            kind: Kind::seed(seed),
        })
    })
}

/// Sends tile `tile`'s marks to where its header says.
pub(crate) fn send(state: &mut State, tile: &str, tx: &mut Tx) {
    let Some(tiled) = tile_of(state, tile) else {
        return;
    };
    let to = match &tiled.kind {
        Kind::File { bound } => destination(
            &state.readings,
            tiled.desktop,
            &state.facts.sessions,
            tile,
            bound.as_deref(),
        )
        .map(|s| To::Session(s.id.clone())),
        Kind::Seed {
            tender: Some(session),
            ..
        } => Some(To::Session(session.clone())),
        Kind::Seed { id, tender: None } => Some(To::Seed(id.clone())),
    };
    if let Some(to) = to {
        let uri = tiled.uri;
        deliver(state, tile, &uri, to, tx);
    }
}

/// Leaves seed tile `tile`'s marks as a note on its seed.
fn note_on_seed(state: &mut State, tile: &str, tx: &mut Tx) {
    close_menu(state, tile);
    let Some(Tiled {
        uri,
        kind: Kind::Seed { id, .. },
        ..
    }) = tile_of(state, tile)
    else {
        return;
    };
    deliver(state, tile, &uri, To::Seed(id), tx);
}

fn deliver(state: &mut State, tile: &str, uri: &str, to: To, tx: &mut Tx) {
    let busy = state
        .readings
        .tiles
        .get(tile)
        .is_some_and(|r| r.sent == Some(Sent::Sending));
    if busy || state.drafts.get(uri).is_none_or(|d| d.marks.is_empty()) {
        return;
    }
    set_sent(state, tile, Some(Sent::Sending));
    let tile = tile.to_owned();
    drafts::submit(
        state,
        uri,
        to,
        move |state, sent, tx| {
            let sent = match sent {
                Sent::Done { noted, kept, .. } => {
                    let until = tx.clock() + SENT_SECONDS;
                    tx.wake_at(until);
                    Sent::Done { noted, kept, until }
                }
                other => other,
            };
            set_sent(state, &tile, Some(sent));
        },
        tx,
    );
}

/// The send key: the marks of the document tile that holds the keyboard go. True when one
/// did.
pub(crate) fn send_key(state: &mut State, tx: &mut Tx) -> bool {
    let Some(tile) = state.window.keyboard.as_ref().map(|h| h.pane.clone()) else {
        return false;
    };
    if tile_of(state, &tile).is_none() {
        return false;
    }
    send(state, &tile, tx);
    true
}

fn set_sent(state: &mut State, tile: &str, sent: Option<Sent>) {
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    reading.sent = sent;
    state.readings.revision += 1;
}

/// Lets "Sent ✓" go when its time is up. True when one went.
pub(crate) fn expire(state: &mut State, now: f64) -> bool {
    let caught_up: Vec<String> = state
        .readings
        .tiles
        .iter()
        .filter(|(tile, r)| {
            r.picked.is_some()
                && tile_of(state, tile).is_some_and(|d| d.kind.bound() == r.picked.as_deref())
        })
        .map(|(tile, _)| tile.clone())
        .collect();
    let mut went = false;
    for (tile, reading) in state.readings.tiles.iter_mut() {
        if caught_up.contains(tile) {
            reading.picked = None;
            went = true;
        }
        if matches!(reading.sent, Some(Sent::Done { until, .. }) if until <= now) {
            reading.sent = None;
            went = true;
        }
        if reading.clearing.is_some_and(|until| until <= now) {
            reading.clearing = None;
            went = true;
        }
    }
    if went {
        state.readings.revision += 1;
    }
    went
}

fn toggle_menu(state: &mut State, tile: &str) {
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    reading.menu = !reading.menu;
    state.readings.revision += 1;
}

fn close_menu(state: &mut State, tile: &str) {
    if let Some(reading) = state.readings.tiles.get_mut(tile) {
        reading.menu = false;
    }
    state.readings.revision += 1;
}

/// Binds tile `tile` to session `session`: sends go there at once, and back to where they went
/// when the desktop refuses.
fn retarget(state: &mut State, tile: &str, session: &str, tx: &mut Tx) {
    let Some(tiled) = tile_of(state, tile) else {
        return;
    };
    let desktop_id = tiled.desktop.id.clone();
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    reading.menu = false;
    reading.sent = None;
    reading.picked = Some(session.to_owned());
    state.readings.revision += 1;
    tx.focus(key(tile, "send"), false);
    let (tile_id, tile, session_id) = (tile.to_owned(), tile.to_owned(), session.to_owned());
    state.ask(
        move |request_id| DesktopUpdateTileMessage {
            request_id,
            desktop_id,
            tile_id,
            tile_session_id: Some(session_id),
            ..DesktopUpdateTileMessage::new()
        },
        move |state, answer, _| {
            let took = answer.outcome().is_ok();
            if !took && let Some(reading) = state.readings.tiles.get_mut(&tile) {
                reading.picked = None;
                state.readings.revision += 1;
            }
        },
        tx,
    );
}

/// The desktop's session list under tile `tile`'s send button, and the review notes over its
/// document, when open.
pub(crate) fn layers(state: &State, cx: &Cx<State>, t: &Tokens) -> Vec<El> {
    let mut out = Vec::new();
    for (tile, reading) in &state.readings.tiles {
        let Some(Tiled { desktop, uri, kind }) = tile_of(state, tile) else {
            continue;
        };
        if reading.menu && cx.layout(&key(tile, "controls")).is_some() {
            let shown = match &kind {
                Kind::File { bound } => {
                    let current = destination(
                        &state.readings,
                        desktop,
                        &state.facts.sessions,
                        tile,
                        bound.as_deref(),
                    )
                    .map(|s| s.id.clone());
                    menu(
                        tile,
                        desktop_sessions(desktop, &state.facts.sessions),
                        current.as_deref(),
                        t,
                    )
                }
                Kind::Seed { .. } => seed_menu(tile, t),
            };
            out.extend(shown);
        }
        if reading.notes
            && let Some(at) = cx.layout(&format!("tile.{tile}"))
        {
            let marks = state
                .drafts
                .get(&uri)
                .map(|d| (d.marks.clone(), d.orphans.clone()))
                .unwrap_or_default();
            out.push(
                notes(tile, marks, reading, t)
                    .absolute((at[0] + at[2] - NOTES_W - 12.0).max(8.0), at[1] + 40.0),
            );
        }
    }
    out
}

/// The desktop's sessions to send tile `tile`'s marks to, beside its header controls, with
/// `current` checked.
fn menu(tile: &str, sessions: Vec<&Session>, current: Option<&str>, t: &Tokens) -> Vec<El> {
    let items = sessions.into_iter().enumerate().map(|(i, s)| {
        let (pressed, session) = (tile.to_owned(), s.id.clone());
        let item = MenuItem::new(crate::sidebar::model::display_name(s))
            .key(key(tile, &format!("send.menu.{i}")))
            .checked(current == Some(s.id.as_str()))
            .on_select_tx(move |state: &mut State, tx: &mut Tx| {
                retarget(state, &pressed, &session, tx)
            });
        if s.state == "pending_approval" {
            item.badge("approval")
        } else {
            item
        }
    });
    destinations(tile, "Send annotations to session", items, t)
}

/// A seed tile's other destination beside its header controls: a note on the seed.
fn seed_menu(tile: &str, t: &Tokens) -> Vec<El> {
    let pressed = tile.to_owned();
    let note = MenuItem::new("Note on seed")
        .key(key(tile, "send.menu.0"))
        .on_select_tx(move |state: &mut State, tx: &mut Tx| note_on_seed(state, &pressed, tx));
    destinations(tile, "More annotation destinations", [note], t)
}

fn destinations(
    tile: &str,
    label: &str,
    items: impl IntoIterator<Item = MenuItem>,
    t: &Tokens,
) -> Vec<El> {
    let closed = tile.to_owned();
    let menu = items.into_iter().fold(
        Menu::new(key(tile, "send.menu.list"))
            .anchor(key(tile, "controls"))
            .placement(Placement::BottomEnd)
            .min_width(MENU_W)
            .label(label)
            .open(true)
            .on_close(move |state: &mut State| close_menu(state, &closed)),
        Menu::item,
    );
    let mut layer = Vec::new();
    menu.build(attn_ui::kit(t), &mut layer);
    layer
}

fn open_notes(state: &mut State, tile: &str, tx: &mut Tx) {
    let reading = state.readings.tiles.entry(tile.to_owned()).or_default();
    reading.notes = !reading.notes;
    state.readings.revision += 1;
    if reading.notes {
        tx.focus(key(tile, "notes.close"), false);
    }
}

/// Escape with the review notes open closes them. True when it took the key.
pub(crate) fn escape(state: &mut State) -> bool {
    let open = state
        .readings
        .tiles
        .iter()
        .find(|(_, r)| r.notes)
        .map(|(t, _)| t.clone());
    let Some(tile) = open else { return false };
    let reading = state.readings.tiles.get_mut(&tile).expect("found above");
    reading.notes = false;
    state.readings.revision += 1;
    true
}

fn notes(
    tile: &str,
    (mut marks, orphans): (Vec<MarkdownAnnotation>, Vec<String>),
    reading: &super::annotating::Reading,
    t: &Tokens,
) -> El {
    super::marks::reading_order(&mut marks);
    let count = marks.len();
    let (closed, overall, cleared) = (tile.to_owned(), tile.to_owned(), tile.to_owned());
    let arming = reading.clearing.is_some();
    let head = row()
        .items_center()
        .gap(6.0)
        .child(
            text("Review notes")
                .font(type_::LABEL)
                .color(t.text_primary)
                .grow(1.0),
        )
        .child(panel_button(
            tile,
            "notes.close",
            ("Close review notes", count.to_string()),
            t.text_secondary,
            t,
            move |state: &mut State, tx: &mut Tx| open_notes(state, &closed, tx),
        ))
        .child(panel_button(
            tile,
            "notes.overall",
            ("+ Overall note", "+ Overall note".to_owned()),
            t.text_secondary,
            t,
            move |state: &mut State, tx: &mut Tx| {
                if let Some(r) = state.readings.tiles.get_mut(&overall) {
                    r.notes = false;
                }
                let uri = state.readings.tiles.get(&overall).map(|r| r.uri.clone());
                if let Some(uri) = uri.or_else(|| tile_of(state, &overall).map(|d| d.uri)) {
                    super::annotating::compose_overall(state, &overall, &uri, tx);
                }
            },
        ))
        .children((count > 0).then(|| {
            let words = if arming { "Confirm?" } else { "Clear all" };
            panel_button(
                tile,
                "notes.clear",
                (words, words.to_owned()),
                t.error,
                t,
                move |state: &mut State, tx: &mut Tx| clear_all(state, &cleared, tx),
            )
        }));
    let body: Vec<El> = if marks.is_empty() {
        vec![
            text("No annotations yet.")
                .font(type_::LABEL)
                .color(t.text_secondary),
            text("Select text in the document to comment, redline, or quick-label it.")
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .wrap(),
        ]
    } else {
        marks
            .iter()
            .map(|m| {
                card(
                    tile,
                    m,
                    orphans.contains(&m.id),
                    reading.selected.as_deref() == Some(&m.id),
                    t,
                )
            })
            .collect()
    };
    col()
        .key(key(tile, "notes.dialog"))
        .role("dialog")
        .label("Review notes")
        .w(NOTES_W)
        .max_h(480.0)
        .gap(8.0)
        .padding(10.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .on_press(|_: &mut State| {})
        .child(head)
        .child(
            col()
                .gap(6.0)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .children(body),
        )
}

/// A text button in the review notes' head: `label` names it, `face` shows in `ink`.
fn panel_button(
    tile: &str,
    part: &str,
    (label, face): (&str, String),
    ink: rune::Color,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(label)
        .variant(ButtonVariant::GhostMuted)
        .label_with(move |_, _| text(face).font(type_::LABEL).color(ink))
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key(tile, part))
        .h(type_::LABEL.line_px() + 6.0)
        .px(6.0)
}

fn card(tile: &str, mark: &MarkdownAnnotation, orphan: bool, selected: bool, t: &Tokens) -> El {
    let label =
        mark.quick_label_id
            .as_deref()
            .map(|id| match crate::annotations::label_by_id(id) {
                Some(l) => format!("{} {}", l.emoji, l.text),
                None => id.to_owned(),
            });
    let badge = label.unwrap_or_else(|| mark.r#type.clone());
    let quote = mark
        .anchor
        .as_ref()
        .map(|a| format!("\u{201c}{}\u{201d}", a.exact));
    let line = mark
        .anchor
        .as_ref()
        .filter(|_| orphan)
        .map(|a| format!("~line {} (moved)", a.start_line));
    let (opened, removed, id, gone) = (
        tile.to_owned(),
        tile.to_owned(),
        mark.id.clone(),
        mark.id.clone(),
    );
    col()
        .key(key(tile, &format!("notes.card.{}", mark.id)))
        .role("button")
        .label(quote.clone().unwrap_or_else(|| badge.clone()))
        .checked(selected)
        .gap(4.0)
        .padding(8.0)
        .radius(6.0)
        .fill(if selected {
            t.accent.alpha(0.12)
        } else {
            t.bg_panel
        })
        .border(1.0, t.border)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            if let Some(r) = state.readings.tiles.get_mut(&opened) {
                r.notes = false;
            }
            super::annotating::focus(state, &opened, &id, tx);
        })
        .child(
            row()
                .items_center()
                .gap(6.0)
                .child(text(badge).font(type_::LABEL).color(t.text_secondary))
                .children(orphan.then(|| text("⚠ moved").font(type_::LABEL).color(t.warning)))
                .child(el().grow(1.0))
                .child(
                    GlyphButton::new(attn_ui::icon::CLOSE, "Remove annotation")
                        .font(type_::LABEL)
                        .side(18.0)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            remove(state, &removed, std::slice::from_ref(&gone), tx)
                        })
                        .build(attn_ui::kit(t))
                        .key(key(tile, &format!("notes.remove.{}", mark.id))),
                ),
        )
        .children(quote.map(|q| {
            text(q)
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .wrap()
                .max_lines(3)
        }))
        .children(line.map(|l| text(l).font(type_::LABEL).color(t.warning)))
        .children(
            mark.text
                .clone()
                .filter(|s| !s.is_empty())
                .map(|c| text(c).font(type_::BODY).color(t.text_primary).wrap()),
        )
}

/// Takes marks `ids` off tile `tile`'s document.
fn remove(state: &mut State, tile: &str, ids: &[String], tx: &mut Tx) {
    let Some(Tiled { uri, .. }) = tile_of(state, tile) else {
        return;
    };
    let now = tx.clock();
    if let Some(draft) = state.drafts.get_mut(&uri) {
        draft.marks.retain(|m| !ids.contains(&m.id));
        draft.changed(now);
        tx.wake_at(now);
    }
}

fn clear_all(state: &mut State, tile: &str, tx: &mut Tx) {
    let arming = state
        .readings
        .tiles
        .get(tile)
        .is_some_and(|r| r.clearing.is_some());
    if !arming {
        let until = tx.clock() + CLEAR_SECONDS;
        if let Some(r) = state.readings.tiles.get_mut(tile) {
            r.clearing = Some(until);
        }
        state.readings.revision += 1;
        tx.wake_at(until);
        return;
    }
    if let Some(r) = state.readings.tiles.get_mut(tile) {
        r.clearing = None;
    }
    let Some(Tiled { uri, .. }) = tile_of(state, tile) else {
        return;
    };
    let ids: Vec<String> = state
        .drafts
        .get(&uri)
        .map(|d| d.marks.iter().map(|m| m.id.clone()).collect())
        .unwrap_or_default();
    remove(state, tile, &ids, tx);
    drafts::save_now(state, &uri, tx);
}
