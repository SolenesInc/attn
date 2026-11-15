//! Settings → Phones: the home's link to Attn Cloud, and for each profile its paired phones,
//! with Revoke, and a pairing code to pair one more. A code shown lasts while the page does:
//! leaving the page cancels it.

use std::collections::BTreeMap;

use attn_client::{Connection, Slices};
use attn_protocol::{
    PhoneLink, PhonePairBeginMessage, PhonePairCancelMessage, PhonePairing, PhonePairingCode,
    PhonePairingRevokeMessage, PhonesGetMessage, Profile,
};
use attn_ui::Tokens;
use qrcodegen::{QrCode, QrCodeEcc, QrSegment, Version};
use rune::tok::{space, type_};
use rune::{El, Pixels, Tx, col, row, text};
use rune_kit::Button;

use super::pages::{CAPTION, block, warning};
use crate::State;
use crate::sidebar::queue::epoch_ms;

const PAIRING_LIFETIME_MINUTES: u32 = 10;

/// Why the last pairing, cancel or revoke failed, by profile.
#[derive(Default)]
pub(crate) struct Phones {
    errors: BTreeMap<String, String>,
    /// Each shown code's QR image, by the text it encodes, drawn once when the code arrives.
    images: BTreeMap<String, Pixels>,
    /// Whether the page was in view at the last frame.
    in_view: bool,
}

/// Modules of light margin around the symbol, as the Tauri app's `uqr` border.
const QR_BORDER: i32 = 2;
/// Image pixels per module, so the image scales down to its box without blurring the modules.
const QR_MODULE_PX: i32 = 16;
const QR_SIZE: f32 = 176.0;

/// `text` as a QR symbol at error correction M, dark modules on white.
fn qr_image(text: &str) -> Option<Pixels> {
    let segments = QrSegment::make_segments(text);
    let qr = QrCode::encode_segments_advanced(
        &segments,
        QrCodeEcc::Medium,
        Version::MIN,
        Version::MAX,
        None,
        false,
    )
    .ok()?;
    let modules = qr.size() + 2 * QR_BORDER;
    let side = (modules * QR_MODULE_PX) as usize;
    let mut rgba = vec![255u8; side * side * 4];
    for y in 0..qr.size() {
        for x in 0..qr.size() {
            if !qr.get_module(x, y) {
                continue;
            }
            let (left, top) = (
                (x + QR_BORDER) * QR_MODULE_PX,
                (y + QR_BORDER) * QR_MODULE_PX,
            );
            for py in top..top + QR_MODULE_PX {
                let row = py as usize * side;
                for px in left..left + QR_MODULE_PX {
                    let at = (row + px as usize) * 4;
                    rgba[at..at + 3].fill(0);
                }
            }
        }
    }
    let pixels = Pixels::new();
    pixels.set(side as u32, side as u32, rgba);
    Some(pixels)
}

/// Draws the QR image of each code the daemon now shows, and lets go of the rest.
fn follow_images(state: &mut State) {
    let codes: Vec<String> = state
        .facts
        .phones
        .as_ref()
        .iter()
        .flat_map(|p| p.pairing_codes.iter().map(|c| c.qr.clone()))
        .collect();
    let images = &mut state.window.settings.phones.images;
    images.retain(|qr, _| codes.contains(qr));
    for qr in codes {
        if !images.contains_key(&qr)
            && let Some(image) = qr_image(&qr)
        {
            images.insert(qr, image);
        }
    }
}

fn shown(state: &State) -> bool {
    super::is_open(state) && state.window.settings.section == super::Section::Phones
}

fn read(state: &mut State) {
    let _ = state.daemon.send(&PhonesGetMessage::new());
}

/// Reads the phones when the page comes into view, starting without the errors it last
/// showed.
pub(super) fn arrive(state: &mut State) {
    if shown(state) {
        state.window.settings.phones.errors.clear();
        read(state);
    }
}

/// Reads the phones again when the connection comes back under the page.
pub(super) fn follow(state: &mut State, changed: Slices) {
    if changed.intersects(Slices::PHONES) {
        follow_images(state);
    }
    if changed.intersects(Slices::CONNECTION)
        && state.facts.connection == Connection::Connected
        && shown(state)
    {
        read(state);
    }
}

/// Cancels each pairing code shown once the page went out of view, however it went: closed,
/// another page, or another layer in its place.
pub(super) fn poll(state: &mut State, tx: &mut Tx) {
    let in_view = shown(state);
    let was = std::mem::replace(&mut state.window.settings.phones.in_view, in_view);
    if !was || in_view {
        return;
    }
    let profiles: Vec<String> = state
        .facts
        .phones
        .as_ref()
        .iter()
        .flat_map(|p| p.pairing_codes.iter().map(|c| c.profile_id.clone()))
        .collect();
    for profile in profiles {
        cancel_quietly(state, profile, tx);
    }
}

fn cancel_quietly(state: &mut State, profile_id: String, tx: &mut Tx) {
    state.ask(
        |request_id| PhonePairCancelMessage {
            profile_id,
            request_id,
            ..PhonePairCancelMessage::new()
        },
        |_, _, _| {},
        tx,
    );
}

/// When the next pairing code shown runs out, in wall-clock milliseconds.
pub(crate) fn next_change(state: &State, now_ms: i64) -> Option<i64> {
    state
        .facts
        .phones
        .as_ref()
        .iter()
        .flat_map(|p| p.pairing_codes.iter())
        .filter_map(|c| epoch_ms(&c.expires_at))
        .filter(|at| *at > now_ms)
        .min()
}

fn failed(state: &mut State, profile_id: &str, why: String) {
    state
        .window
        .settings
        .phones
        .errors
        .insert(profile_id.to_owned(), why);
}

fn pair(state: &mut State, profile_id: &str, tx: &mut Tx) {
    state.window.settings.phones.errors.remove(profile_id);
    let profile = profile_id.to_owned();
    let answered = profile.clone();
    state.ask(
        |request_id| PhonePairBeginMessage {
            profile_id: profile,
            request_id,
            ..PhonePairBeginMessage::new()
        },
        move |state, answer, tx| match answer.outcome() {
            Ok(_) if !shown(state) => cancel_quietly(state, answered, tx),
            Ok(_) => {}
            Err(why) => failed(state, &answered, why),
        },
        tx,
    );
}

fn cancel(state: &mut State, profile_id: &str, tx: &mut Tx) {
    let profile = profile_id.to_owned();
    let answered = profile.clone();
    state.ask(
        |request_id| PhonePairCancelMessage {
            profile_id: profile,
            request_id,
            ..PhonePairCancelMessage::new()
        },
        move |state, answer, _| {
            if let Err(why) = answer.outcome() {
                failed(state, &answered, why);
            }
        },
        tx,
    );
}

/// Revokes a phone, once however often it is pressed before the daemon answers.
fn revoke(state: &mut State, device_id: &str, profile_id: &str, tx: &mut Tx) {
    if state.requests.waits_for(|c| {
        c["cmd"] == "phone_pairing_revoke"
            && c["device_id"] == device_id
            && c["profile_id"] == profile_id
    }) {
        return;
    }
    let (device, profile) = (device_id.to_owned(), profile_id.to_owned());
    let answered = profile.clone();
    state.ask(
        |request_id| PhonePairingRevokeMessage {
            device_id: device,
            profile_id: profile,
            request_id,
            ..PhonePairingRevokeMessage::new()
        },
        move |state, answer, _| {
            if let Err(why) = answer.outcome() {
                failed(state, &answered, why);
            }
        },
        tx,
    );
}

fn link_text(link: &PhoneLink) -> (String, bool) {
    let error = link.error.as_deref().filter(|e| !e.is_empty());
    match (link.state.as_str(), error) {
        ("connected", _) => (
            format!("Connected to Attn Cloud at {}", link.cloud_url),
            false,
        ),
        ("connecting", Some(error)) => (
            format!(
                "Reconnecting to Attn Cloud at {}. Last error: {error}",
                link.cloud_url
            ),
            true,
        ),
        ("connecting", None) => (
            format!("Connecting to Attn Cloud at {}…", link.cloud_url),
            false,
        ),
        (_, Some(error)) => (error.to_owned(), true),
        (_, None) => (
            "Phone access is off, so this home holds no connection to Attn Cloud.".into(),
            false,
        ),
    }
}

fn paired_phone(pairing: &PhonePairing, t: &Tokens) -> El {
    let seen = if pairing.connected {
        "Connected now".to_owned()
    } else {
        let at = epoch_ms(&pairing.last_seen_at)
            .map_or_else(|| pairing.last_seen_at.clone(), crate::calendar::date_time);
        format!("Last seen {at}")
    };
    let key = format!(
        "settings.phone.{}.{}",
        pairing.profile_id, pairing.device_id
    );
    let (device, profile) = (pairing.device_id.clone(), pairing.profile_id.clone());
    row()
        .key(key.clone())
        .role("listitem")
        .label(pairing.name.clone())
        .w_full()
        .gap(space::S3)
        .items_center()
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(space::S1)
                .child(
                    text(pairing.name.clone())
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .child(
                    text(seen.clone())
                        .key(format!("{key}.seen"))
                        .label(seen)
                        .font(CAPTION)
                        .color(t.text_tertiary),
                ),
        )
        .child(
            Button::new("Revoke")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    revoke(state, &device, &profile, tx)
                })
                .build(attn_ui::kit(t))
                .key(format!("{key}.revoke")),
        )
}

fn pairing_code(state: &State, code: &PhonePairingCode, profile: &Profile, t: &Tokens) -> El {
    let key = format!("settings.phones.{}.code", profile.id);
    let expires = epoch_ms(&code.expires_at);
    if expires.is_some_and(|at| at <= state.window.now_ms) {
        let said =
            format!("Pairing code expired after {PAIRING_LIFETIME_MINUTES} min; show a new one.");
        return text(said.clone())
            .key(format!("{key}.expired"))
            .role("status")
            .label(said)
            .font(CAPTION)
            .color(t.warning)
            .wrap();
    }
    let until = expires.map_or_else(|| code.expires_at.clone(), crate::calendar::time_of_day);
    let id = profile.id.clone();
    let image = state
        .window
        .settings
        .phones
        .images
        .get(&code.qr)
        .map(|pixels| {
            rune::el()
                .key(format!("{key}.qr"))
                .role("img")
                .label(format!("Pairing code for {}", profile.name))
                .w(QR_SIZE)
                .h(QR_SIZE)
                .radius(6.0)
                .pixels(pixels)
        });
    col()
        .key(key.clone())
        .gap(space::S2)
        .children(image)
        .child(
            text(format!(
                "Scan with the attn app. The code works once, until {until}."
            ))
            .font(CAPTION)
            .color(t.text_tertiary)
            .wrap(),
        )
        .child(
            row().child(
                Button::new("Cancel")
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| cancel(state, &id, tx))
                    .build(attn_ui::kit(t))
                    .key(format!("{key}.cancel")),
            ),
        )
}

fn profile_block(
    state: &State,
    phones: &attn_protocol::PhonesUpdatedMessage,
    profile: &Profile,
    t: &Tokens,
) -> El {
    let code = phones
        .pairing_codes
        .iter()
        .find(|c| c.profile_id == profile.id);
    let paired: Vec<&PhonePairing> = phones
        .pairings
        .iter()
        .filter(|p| p.profile_id == profile.id)
        .collect();
    let mut body = Vec::new();
    if !paired.is_empty() {
        body.push(
            col()
                .key(format!("settings.phones.{}.list", profile.id))
                .role("list")
                .label(format!("Phones paired with {}", profile.name))
                .w_full()
                .gap(space::S2)
                .children(paired.into_iter().map(|p| paired_phone(p, t))),
        );
    }
    if let Some(code) = code {
        body.push(pairing_code(state, code, profile, t));
    }
    if let Some(error) = state.window.settings.phones.errors.get(&profile.id) {
        body.push(warning(
            format!("settings.phones.{}.error", profile.id),
            error.clone(),
            t,
        ));
    }
    let id = profile.id.clone();
    body.push(
        row().child(
            Button::new(if code.is_some() {
                "Show a new pairing code"
            } else {
                "Pair a phone"
            })
            .disabled(phones.link.state == "off")
            .on_press_tx(move |state: &mut State, tx: &mut Tx| pair(state, &id, tx))
            .build(attn_ui::kit(t))
            .key(format!("settings.phones.{}.pair", profile.id)),
        ),
    );
    block(
        &format!("phones.{}", profile.id),
        &profile.name,
        "A phone you pair here sees this profile's agents and nothing else.",
        t,
        body,
    )
    .label(format!("Phones for {}", profile.name))
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let Some(phones) = state.facts.phones.as_ref() else {
        return vec![
            text("Loading phones…")
                .key("settings.phones.loading")
                .font(type_::BODY)
                .color(t.text_tertiary),
        ];
    };
    let (said, bad) = link_text(&phones.link);
    let link = text(said.clone())
        .key("settings.phones.link")
        .role("status")
        .label(said)
        .font(CAPTION)
        .color(if bad { t.warning } else { t.text_tertiary })
        .wrap();
    let mut shown = vec![block(
        "phones.cloud",
        "Attn Cloud",
        "Phones reach this home through Attn Cloud, which relays end-to-end encrypted traffic it cannot read.",
        t,
        vec![link],
    )];
    shown.extend(
        state
            .facts
            .profiles
            .all
            .iter()
            .map(|profile| profile_block(state, phones, profile, t)),
    );
    shown
}
