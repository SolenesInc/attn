//! Browser tiles: a desktop's in-app browser, whose page an agent opens and drives with
//! `attn browser` and the person watches, browses and signs in on. The page is a native web
//! view over the window ([`host::BrowserHost`]); the window decides which pages exist, where
//! each shows, and answers the daemon's `browser_control_request`s with what the page says.

mod host;
mod view;

#[cfg(target_os = "macos")]
pub mod mac;

use std::collections::HashMap;

use attn_client::{Reply, TileKey};
use attn_protocol::{
    BrowserControlRequestMessage, BrowserControlResultMessage, DesktopUpdateTileMessage,
};
use rune::{TextEdit, Tx};
use serde_json::{Map, Value};

pub use host::{
    Alert, AlertKind, BrowserHost, Control, Heard, PageEdit, Placement, claims_key, hear,
    hosts_pages, page_alert, page_edit, use_host,
};
pub(crate) use view::{address_field, page_area, reload_button};

use crate::State;
use crate::desktop::layout::{LeafKind, Node};
use crate::keymap::PageKey;

pub(crate) const KIND: &str = "browser";

/// The daemon reads at most 32 MiB from the app (`maxTrustedAppWebSocketReadBytes` in
/// `internal/daemon/websocket.go`); a result past this says so instead of ending the connection.
const MAX_RESULT_BYTES: usize = 24 * 1024 * 1024;
/// How often a `wait_for` asks the page again, and how long it waits when the agent gives no
/// timeout, as the Tauri app's host does.
const WAIT_PROBE_SECS: f64 = 0.05;
const WAIT_DEFAULT_MS: f64 = 5_000.0;
const WAIT_LONGEST_MS: f64 = 120_000.0;

/// The window's browser pages, by the tile that shows each.
#[derive(Default)]
pub struct Browsers {
    pages: HashMap<TileKey, Page>,
    /// What each tile's address field holds.
    addresses: HashMap<TileKey, TextEdit>,
    /// The page that holds the keyboard, until a click or a key gives it back to the window.
    focused: Option<TileKey>,
    waits: HashMap<String, Wait>,
    /// Counts changes to what the tiles show of the pages, for the view's memo.
    revision: u64,
}

struct Page {
    /// The address the page loaded or browsed to.
    url: String,
    /// The address the tile stored when the window last looked: the page goes elsewhere only
    /// when this changes, so storing where it browsed loads nothing.
    stored: String,
    shown: Option<Placement>,
    /// The locations this page asked the daemon to store, oldest first, until the daemon's
    /// copy of the tile shows each: the daemon storing one of these is the page's own echo.
    sent: Vec<String>,
}

/// A `wait_for` that asks the page again until its element is ready or its time runs out.
struct Wait {
    page: TileKey,
    params: Map<String, Value>,
    /// Runtime seconds when the wait gives up, and when it asks the page next.
    until: f64,
    next: Option<f64>,
    asked_ms: f64,
    host_ms: f64,
}

impl Browsers {
    pub(crate) fn address(&self, page: &TileKey) -> Option<&TextEdit> {
        self.addresses.get(page)
    }

    pub(crate) fn address_mut(&mut self, page: &TileKey) -> &mut TextEdit {
        self.revision += 1;
        self.addresses.entry(page.clone()).or_default()
    }

    pub(crate) fn revision(&self) -> u64 {
        self.revision
    }
}

/// The browser tiles whose pages this window keeps: those on the current desktop and the one
/// before it, as the Tauri app keeps them, each with the address its tile stores.
fn wanted(state: &State) -> Vec<(TileKey, String)> {
    if state.window.tile.is_some() {
        return Vec::new();
    }
    let facts = &state.facts;
    let current = facts.current_desktop().map(|d| d.id.as_str());
    let previous = state.window.desk.previous_desktop.as_deref();
    facts
        .desktops
        .iter()
        .filter(|d| Some(d.id.as_str()) == current || Some(d.id.as_str()) == previous)
        .flat_map(|desktop| {
            let Some(tree) = Node::parse(&desktop.tree_json) else {
                return Vec::new();
            };
            tree.leaves()
                .into_iter()
                .filter(|leaf| leaf.window.is_none())
                .filter_map(|leaf| match &leaf.kind {
                    LeafKind::Tile { kind, params, .. } if kind == KIND => Some((
                        TileKey {
                            desktop: desktop.id.clone(),
                            tile: leaf.id.clone(),
                        },
                        params.clone().unwrap_or_default(),
                    )),
                    _ => None,
                })
                .collect()
        })
        .filter(|(_, url)| !url.is_empty())
        .collect()
}

/// Opens, points and frees pages to match the tiles, places each where its tile shows, and
/// takes what the pages said. Runs at the start of every frame; true when the view reads
/// something that changed.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) -> bool {
    if !hosts_pages() || state.window.tile.is_some() {
        return false;
    }
    let mut changed = keep_pages(state);
    changed |= heard(state, tx);
    changed |= probe_waits(state, tx);
    place_pages(state, tx);
    hand_keys(state, tx);
    changed
}

/// The browser tile whose address field has the keyboard, so the window's keys act on it.
pub(crate) fn address_tile(focused: Option<&str>) -> Option<&str> {
    focused?.strip_prefix("tile.")?.strip_suffix(".address")
}

fn keep_pages(state: &mut State) -> bool {
    let wanted = wanted(state);
    let browsers = &mut state.browser;
    let mut changed = false;
    let gone: Vec<TileKey> = browsers
        .pages
        .keys()
        .filter(|key| !wanted.iter().any(|(w, _)| w == *key))
        .cloned()
        .collect();
    for key in gone {
        host::with_host(|h| h.close(&key));
        host::forget(&key);
        browsers.pages.remove(&key);
        browsers.addresses.remove(&key);
        browsers.revision += 1;
        browsers.focused.take_if(|f| *f == key);
        changed = true;
    }
    for (key, url) in wanted {
        match browsers.pages.get_mut(&key) {
            None => {
                host::with_host(|h| h.open(&key, &url));
                browsers.pages.insert(
                    key.clone(),
                    Page {
                        url: url.clone(),
                        stored: url.clone(),
                        shown: None,
                        sent: Vec::new(),
                    },
                );
                *browsers.address_mut(&key) = TextEdit::new(&url);
                changed = true;
            }
            Some(page) if page.stored != url => {
                page.stored = url.clone();
                let echo = page.sent.iter().position(|sent| *sent == url);
                match echo {
                    Some(at) => drop(page.sent.drain(..=at)),
                    None => page.sent.clear(),
                }
                if echo.is_none() && page.url != url {
                    host::with_host(|h| h.navigate(&key, &url));
                    page.url = url.clone();
                    *browsers.address_mut(&key) = TextEdit::new(&url);
                    changed = true;
                }
            }
            Some(_) => {}
        }
    }
    changed
}

fn heard(state: &mut State, tx: &mut Tx) -> bool {
    let mut changed = false;
    for heard in host::take_heard() {
        match heard {
            Heard::Located { page, url } => changed |= located(state, page, url, tx),
            Heard::Focused { page } => changed |= page_focused(state, page, tx),
            Heard::Answered { request_id, result } => answered(state, request_id, result, tx),
            Heard::Alerted { page } => alerted(state, &page),
        }
    }
    changed
}

/// The page browsed to `url`: its address field shows it, and the daemon stores it as the
/// tile's address, so the tile comes back there.
fn located(state: &mut State, page: TileKey, url: String, tx: &mut Tx) -> bool {
    let stored = stored_url(state, &page);
    let Some(known) = state.browser.pages.get_mut(&page) else {
        return false;
    };
    known.url = url.clone();
    let already_stored = known.sent.last().or(stored.as_ref()) == Some(&url);
    *state.browser.address_mut(&page) = TextEdit::new(&url);
    if already_stored {
        return true;
    }
    store_location(state, page, url, tx);
    true
}

fn leaf_of(state: &State, page: &TileKey) -> Option<crate::desktop::layout::Leaf> {
    let desktop = state.facts.desktops.iter().find(|d| d.id == page.desktop)?;
    match Node::parse(&desktop.tree_json)?.find_leaf(&page.tile)? {
        Node::Leaf(leaf) => Some(leaf.clone()),
        _ => None,
    }
}

fn stored_url(state: &State, page: &TileKey) -> Option<String> {
    match leaf_of(state, page)?.kind {
        LeafKind::Tile { params, .. } => params,
        _ => None,
    }
}

fn popped_out(state: &State, page: &TileKey) -> bool {
    leaf_of(state, page).is_some_and(|leaf| leaf.window.is_some())
}

fn store_location(state: &mut State, page: TileKey, url: String, tx: &mut Tx) {
    if let Some(known) = state.browser.pages.get_mut(&page) {
        known.sent.push(url.clone());
    }
    let (desktop_id, tile_id) = (page.desktop.clone(), page.tile.clone());
    let sent = url.clone();
    state.ask(
        |request_id| DesktopUpdateTileMessage {
            request_id,
            desktop_id,
            tile_id,
            tile_params: Some(sent),
            ..DesktopUpdateTileMessage::new()
        },
        move |state, answer, _| {
            let Err(why) = answer.outcome() else {
                return;
            };
            eprintln!("attn-app: the browser tile's location was not stored: {why}");
            if let Some(known) = state.browser.pages.get_mut(&page) {
                known.sent.retain(|sent| *sent != url);
            }
        },
        tx,
    );
}

/// A click in the page gave it the keyboard: its tile becomes the one the window's keys act on,
/// and nothing of the window's keeps focus.
fn page_focused(state: &mut State, page: TileKey, tx: &mut Tx) -> bool {
    if !state.browser.pages.contains_key(&page) {
        return false;
    }
    state.browser.focused = Some(page.clone());
    tx.blur();
    crate::desktop::activate(state, &page.desktop, &page.tile, tx);
    true
}

/// The page whose tile holds the window's keyboard has it, while no control of the window's
/// has focus, no sequence waits for its second key, and the page shows; any other page gives it
/// back to the window.
fn hand_keys(state: &mut State, tx: &Tx) {
    let holder = state.window.keyboard.as_ref().map(|h| TileKey {
        desktop: h.desktop.clone(),
        tile: h.pane.clone(),
    });
    let wanted = holder.filter(|page| {
        tx.focused().is_none()
            && state.window.leader.is_none()
            && state
                .browser
                .pages
                .get(page)
                .is_some_and(|p| p.shown.is_some())
    });
    if wanted == state.browser.focused {
        return;
    }
    if let Some(was) = state.browser.focused.take() {
        host::with_host(|h| h.focus(&was, false));
    }
    if let Some(page) = &wanted {
        host::with_host(|h| h.focus(page, true));
    }
    state.browser.focused = wanted;
}

fn place_pages(state: &mut State, tx: &mut Tx) {
    if state.browser.pages.is_empty() {
        return;
    }
    let keys: Vec<TileKey> = state.browser.pages.keys().cloned().collect();
    let elements = keys.iter().map(view::page_key).collect();
    tx.find_elements(elements, move |state: &mut State, found, tx| {
        let covered = crate::shell::covers_tiles(state);
        let dock_left = state.window.width.window - crate::shell::dock_width(state);
        let zoom = crate::settings::text_scale(&state.facts.settings);
        let mut moved = false;
        for (key, info) in keys.into_iter().zip(found) {
            let at = info
                .filter(|e| !covered && !e.inert && !e.exiting && e.opacity > 0.0)
                .map(|mut e| {
                    e.visible[2] = e.visible[2].min(dock_left - e.visible[0]);
                    e
                })
                .filter(|e| e.visible[2] >= 1.0 && e.visible[3] >= 1.0)
                .map(|e| Placement {
                    frame: e.rect.map(|v| v * zoom),
                    visible: e.visible.map(|v| v * zoom),
                });
            let Some(page) = state.browser.pages.get_mut(&key) else {
                continue;
            };
            if page.shown == at {
                continue;
            }
            page.shown = at;
            moved = true;
            host::with_host(|h| h.place(&key, at));
        }
        if moved {
            hand_keys(state, tx);
        } else {
            tx.live_only();
        }
    });
}

/// Answers a `browser_control_request`; false for any other reply.
pub(crate) fn receive(state: &mut State, reply: &Reply, tx: &mut Tx) -> bool {
    if reply.event != "browser_control_request" {
        return false;
    }
    if let Some(request) = reply.parse::<BrowserControlRequestMessage>() {
        control(state, request, tx);
    }
    true
}

fn control(state: &mut State, request: BrowserControlRequestMessage, tx: &mut Tx) {
    let page = TileKey {
        desktop: request.desktop_id.clone(),
        tile: request.tile_id.clone(),
    };
    let id = request.request_id.clone();
    let params = match params(&request) {
        Ok(params) => params,
        Err(why) => return answer(state, id, Err(why)),
    };
    if !state.browser.pages.contains_key(&page) {
        keep_pages(state);
    }
    let Some(known) = state.browser.pages.get_mut(&page) else {
        let why = if popped_out(state, &page) {
            "the browser tile is popped out into its own window, where this app shows no page yet. Ask the user to bring it back to its desktop, then retry.".to_owned()
        } else {
            format!(
                "the browser tile on desktop {} is not loaded: attn loads it only while its desktop is the current or the previous one. Ask the user to open that desktop, then retry.",
                request.desktop_id
            )
        };
        return answer(state, id, Err(why));
    };
    match request.action.as_str() {
        "navigate" => {
            let url = match web_url(&params) {
                Ok(url) => url,
                Err(why) => return answer(state, id, Err(why)),
            };
            if known.url != url {
                known.url = url.clone();
                host::with_host(|h| h.navigate(&page, &url));
                *state.browser.address_mut(&page) = TextEdit::new(&url);
            }
            answer(state, id, Ok(r#"{"success":true}"#.into()));
        }
        "reload" => {
            host::with_host(|h| h.reload(&page));
            answer(state, id, Ok(r#"{"success":true}"#.into()));
        }
        "get_alert_text" => {
            let text = host::alert_text(&page)
                .map(|text| Value::String(text).to_string())
                .ok_or_else(|| "no such alert".to_owned());
            answer(state, id, text);
        }
        "send_alert_text" => {
            let typed = match params.get("text").and_then(Value::as_str) {
                Some(text) if !text.is_empty() => host::type_alert(&page, text),
                _ => return answer(state, id, Err("text must be a non-empty string".into())),
            };
            let result = typed
                .then(|| "null".to_owned())
                .ok_or_else(|| "no prompt alert is open".to_owned());
            answer(state, id, result);
        }
        "accept_alert" | "dismiss_alert" => {
            let closed = host::close_alert(&page, request.action == "accept_alert");
            let result = closed
                .then(|| "null".to_owned())
                .ok_or_else(|| "no such alert".to_owned());
            answer(state, id, result);
        }
        "get_window_handle" => answer(state, id, Ok(Value::String(handle(&page)).to_string())),
        "get_window_handles" => answer(state, id, Ok(Value::from(vec![handle(&page)]).to_string())),
        "wait_for" => {
            let asked_ms = params
                .get("timeout")
                .and_then(Value::as_f64)
                .unwrap_or(WAIT_DEFAULT_MS);
            let host_ms = asked_ms.clamp(0.0, WAIT_LONGEST_MS);
            state.browser.waits.insert(
                id.clone(),
                Wait {
                    page,
                    params,
                    until: tx.clock() + host_ms / 1000.0,
                    next: None,
                    asked_ms,
                    host_ms,
                },
            );
            probe(state, &id);
        }
        action => {
            let request = Control {
                request_id: id,
                action: action.to_owned(),
                params,
            };
            host::run(&page, request);
        }
    }
}

fn params(request: &BrowserControlRequestMessage) -> Result<Map<String, Value>, String> {
    let mut params = match request.params.as_deref() {
        Some(raw) => match serde_json::from_str::<Value>(raw) {
            Ok(Value::Object(object)) => object,
            Ok(_) => return Err("browser params must be a JSON object".into()),
            Err(why) => return Err(format!("decode browser params: {why}")),
        },
        None => Map::new(),
    };
    if let Some(selector) = &request.selector {
        params
            .entry("selector")
            .or_insert_with(|| Value::String(selector.clone()));
    }
    if let Some(text) = &request.text {
        params
            .entry("text")
            .or_insert_with(|| Value::String(text.clone()));
    }
    Ok(params)
}

/// The address a `navigate` names: its `url`, or else its `text`, if it is http or https.
fn web_url(params: &Map<String, Value>) -> Result<String, String> {
    let raw = ["url", "text"]
        .iter()
        .find_map(|key| {
            params
                .get(*key)
                .and_then(Value::as_str)
                .filter(|s| !s.is_empty())
        })
        .ok_or("url must be a non-empty string")?;
    let scheme = raw.split_once("://").map(|(s, _)| s.to_ascii_lowercase());
    match scheme.as_deref() {
        Some("http" | "https") => Ok(raw.to_owned()),
        _ => Err("only http and https browser URLs are supported".into()),
    }
}

/// Asks the page again for each `wait_for` whose turn came.
fn probe_waits(state: &mut State, tx: &mut Tx) -> bool {
    let now = tx.clock();
    let due: Vec<String> = state
        .browser
        .waits
        .iter()
        .filter_map(|(id, wait)| {
            let next = wait.next?;
            if now < next {
                tx.wake_at(next);
                return None;
            }
            Some(id.clone())
        })
        .collect();
    for id in due {
        probe(state, &id);
    }
    false
}

/// Asks the page whether the element `wait_id` waits for is ready, under the wait's own id.
fn probe(state: &mut State, wait_id: &str) {
    let Some(wait) = state.browser.waits.get_mut(wait_id) else {
        return;
    };
    wait.next = None;
    let request = Control {
        request_id: wait_id.to_owned(),
        action: "wait_for".into(),
        params: wait.params.clone(),
    };
    host::run(&wait.page.clone(), request);
}

fn answered(state: &mut State, request_id: String, result: Result<String, String>, tx: &mut Tx) {
    if !host::settle(&request_id) {
        return;
    }
    if !state.browser.waits.contains_key(&request_id) {
        return answer(state, request_id, result);
    }
    let ready = result.map(|raw| {
        let probe: Value = serde_json::from_str(&raw).unwrap_or(Value::Null);
        probe
            .get("ready")
            .and_then(Value::as_bool)
            .unwrap_or(false)
            .then(|| {
                probe
                    .get("value")
                    .cloned()
                    .unwrap_or(Value::Null)
                    .to_string()
            })
    });
    let now = tx.clock();
    let wait = state
        .browser
        .waits
        .get_mut(&request_id)
        .expect("a probed wait");
    match ready {
        Ok(None) if now < wait.until => {
            let next = (now + WAIT_PROBE_SECS).min(wait.until);
            wait.next = Some(next);
            tx.wake_at(next);
        }
        Ok(None) => {
            let wait = state
                .browser
                .waits
                .remove(&request_id)
                .expect("a probed wait");
            let state_name = wait
                .params
                .get("state")
                .and_then(Value::as_str)
                .unwrap_or("attached")
                .to_owned();
            answer(
                state,
                request_id,
                Err(format!(
                    "wait_for timed out waiting for {state_name} (timeoutMs={}, hostTimeoutMs={})",
                    wait.asked_ms, wait.host_ms
                )),
            );
        }
        done => {
            state.browser.waits.remove(&request_id);
            answer(state, request_id, done.map(Option::unwrap_or_default));
        }
    }
}

/// A dialog ended the agents' actions on `page`: each answers null, as WebDriver's do, and the
/// agent goes on to read or close the dialog.
fn alerted(state: &mut State, page: &TileKey) {
    for request_id in host::interrupt(page) {
        state.browser.waits.remove(&request_id);
        answer(state, request_id, Ok("null".into()));
    }
}

/// The name WebDriver's window handles give the page, as the Tauri app named it.
fn handle(page: &TileKey) -> String {
    let safe = |part: &str| {
        part.chars()
            .map(|c| {
                if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                    c
                } else {
                    '-'
                }
            })
            .collect::<String>()
    };
    format!("browser-{}-{}", safe(&page.desktop), safe(&page.tile))
}

/// Tells the pages which keys the window takes from them.
pub(crate) fn claim_keys(keymap: &crate::keymap::Keymap) {
    host::claim_keys(keymap.claimed_from_pages().collect());
}

/// Whether a page holds the window's keyboard.
pub(crate) fn holds_keys(state: &State) -> bool {
    state.browser.focused.is_some()
}

/// Runs `action`'s browser key on the page that holds the keyboard: false when no page holds
/// it or `action` is no browser key.
pub(crate) fn press_page_key(state: &State, action: &crate::action::Action) -> bool {
    let (Some(page), Some(key)) = (&state.browser.focused, crate::keymap::page_key(action)) else {
        return false;
    };
    host::with_host(|h| match key {
        PageKey::Reload => h.reload(page),
        PageKey::Back => h.back(page),
        PageKey::Forward => h.forward(page),
    });
    true
}

fn answer(state: &State, request_id: String, result: Result<String, String>) {
    let result = result.and_then(|data| {
        if data.len() > MAX_RESULT_BYTES {
            Err(format!(
                "serialized browser control result is {} bytes; the maximum supported result is {MAX_RESULT_BYTES} bytes",
                data.len()
            ))
        } else {
            Ok(data)
        }
    });
    let (success, data, error) = match result {
        Ok(data) => (true, Some(data), None),
        Err(error) => (false, None, Some(error)),
    };
    let _ = state.daemon.send(&BrowserControlResultMessage {
        request_id,
        success,
        data,
        error,
        ..BrowserControlResultMessage::new()
    });
}

/// The person pressed Enter in the address field: the page goes to what it says, with a scheme
/// added when it names none.
pub(crate) fn go_to_address(state: &mut State, page: &TileKey) {
    let typed = state
        .browser
        .address(page)
        .map(|a| a.text.trim().to_owned())
        .unwrap_or_default();
    if typed.is_empty() {
        return;
    }
    let url = normalize_address(&typed);
    *state.browser.address_mut(page) = TextEdit::new(&url);
    if let Some(known) = state.browser.pages.get_mut(page) {
        known.url = url.clone();
        host::with_host(|h| h.navigate(page, &url));
    }
}

/// The person pressed the tile's Reload button.
pub(crate) fn reload(page: &TileKey) {
    host::with_host(|h| h.reload(page));
}

/// What the address field navigates to for `typed`: as typed when it names a scheme, else
/// over http for this machine and https for any other host.
pub fn normalize_address(typed: &str) -> String {
    let typed = typed.trim();
    let has_scheme = typed.split_once("://").is_some_and(|(scheme, _)| {
        let mut chars = scheme.chars();
        chars.next().is_some_and(|c| c.is_ascii_alphabetic())
            && chars.all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '.' | '-'))
    });
    if has_scheme {
        return typed.to_owned();
    }
    let host_end = typed.find(['/', '?', '#']).unwrap_or(typed.len());
    let host = &typed[..host_end];
    let name = match host.strip_prefix('[') {
        Some(rest) => rest.split_once(']').map_or(host, |(inside, _)| inside),
        None => host.rsplit_once(':').map_or(host, |(name, port)| {
            if port.chars().all(|c| c.is_ascii_digit()) {
                name
            } else {
                host
            }
        }),
    };
    let local = name.eq_ignore_ascii_case("localhost") || name == "::1" || is_loopback_v4(name);
    format!("{}://{typed}", if local { "http" } else { "https" })
}

fn is_loopback_v4(name: &str) -> bool {
    let parts: Vec<&str> = name.split('.').collect();
    parts.len() == 4
        && parts[0] == "127"
        && parts[1..]
            .iter()
            .all(|p| (1..=3).contains(&p.len()) && p.chars().all(|c| c.is_ascii_digit()))
}
