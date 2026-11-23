//! Content-hash cache for harness logos sent by the daemon over `harness_logo`.
//!
//! `shell` and the generic fallback mark stay static; every harness's own logo arrives by hash
//! and is cached in memory and on disk off the UI thread.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use attn_protocol::{HarnessLogo, HarnessLogoMessage, HarnessLogoResultMessage};
use rune::{Icon, IconSource, OwnedIcon, Tx, Waker};

use super::catalog;
use crate::State;

const MAX_LOGO_BODY_BYTES: usize = 64 * 1024;

static SHELL: Icon = Icon {
    name: "harness-shell",
    view: 24.0,
    body: r#"<g fill="none" stroke="currentColor" stroke-width="1.5"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="m7 9 3 3-3 3m6 0h4"/></g>"#,
};

static OTHER: Icon = Icon {
    name: "harness-other",
    view: 24.0,
    body: r#"<g fill="none" stroke="currentColor" stroke-width="1.5"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="M8 10v2m8-2v2m-7 4h6"/></g>"#,
};

enum Entry {
    LoadingDisk,
    Fetching,
    Loaded(OwnedIcon),
    Missing,
}

type DiskInbox = Arc<Mutex<Vec<(String, Option<OwnedIcon>)>>>;

#[derive(Default)]
pub struct Logos {
    cache_dir: Option<PathBuf>,
    entries: HashMap<String, Entry>,
    catalog_logos: Vec<(String, String)>,
    disk_inbox: DiskInbox,
    pending_writes: Arc<AtomicUsize>,
    wake: Option<Waker>,
    revision: u64,
}

impl Logos {
    pub(crate) fn start(&mut self, wake: Waker) {
        self.wake = Some(wake);
    }

    pub fn cache_in(&mut self, dir: PathBuf) {
        self.cache_dir = Some(dir);
    }

    pub fn loading(&self) -> bool {
        self.entries
            .values()
            .any(|e| matches!(e, Entry::LoadingDisk))
            || !self.disk_inbox.lock().unwrap().is_empty()
            || self.pending_writes.load(Ordering::Acquire) > 0
    }

    pub(crate) fn revision(&self) -> u64 {
        self.revision
    }

    fn insert_loaded(&mut self, hash: String, icon: OwnedIcon) -> bool {
        self.entries.insert(hash, Entry::Loaded(icon));
        self.revision += 1;
        true
    }
}

fn build_icon(hash: &str, logo: &HarnessLogo) -> Option<OwnedIcon> {
    if !valid_logo(logo) {
        return None;
    }
    OwnedIcon::try_new(
        format!("harness-logo-{hash}"),
        logo.view as f32,
        logo.body.clone(),
    )
}

fn valid_hash(hash: &str) -> bool {
    !hash.is_empty() && hash.len() <= 128 && hash.bytes().all(|b| b.is_ascii_hexdigit())
}

fn valid_logo(logo: &HarnessLogo) -> bool {
    logo.view.is_finite()
        && logo.view > 0.0
        && !logo.body.trim().is_empty()
        && logo.body.len() <= MAX_LOGO_BODY_BYTES
}

fn disk_path(dir: &Path, hash: &str) -> PathBuf {
    dir.join(format!("{hash}.json"))
}

fn read_disk_logo(dir: &Path, hash: &str) -> Option<OwnedIcon> {
    let bytes = std::fs::read(disk_path(dir, hash)).ok()?;
    let logo: HarnessLogo = serde_json::from_slice(&bytes).ok()?;
    build_icon(hash, &logo)
}

static NEXT_TMP: AtomicU64 = AtomicU64::new(1);

fn write_disk_logo(
    dir: PathBuf,
    hash: String,
    logo: HarnessLogo,
    pending_writes: Arc<AtomicUsize>,
    wake: Option<Waker>,
) {
    pending_writes.fetch_add(1, Ordering::AcqRel);
    std::thread::spawn(move || {
        if std::fs::create_dir_all(&dir).is_ok()
            && let Ok(bytes) = serde_json::to_vec(&logo)
        {
            let target = disk_path(&dir, &hash);
            let seq = NEXT_TMP.fetch_add(1, Ordering::Relaxed);
            let tmp = dir.join(format!(".{hash}.{}.{seq}.tmp", std::process::id()));
            if std::fs::write(&tmp, &bytes).is_ok() {
                let _ = std::fs::rename(&tmp, &target);
            }
        }
        pending_writes.fetch_sub(1, Ordering::AcqRel);
        if let Some(wake) = wake {
            wake();
        }
    });
}

fn drain_disk(state: &mut State, tx: &mut Tx) -> bool {
    let arrived: Vec<(String, Option<OwnedIcon>)> = {
        let mut inbox = state.logos.disk_inbox.lock().unwrap();
        std::mem::take(&mut *inbox)
    };
    if arrived.is_empty() {
        return false;
    }
    let connected = state.facts.connection == attn_client::Connection::Connected;
    let mut changed = false;
    for (hash, found) in arrived {
        if let Some(icon) = found {
            changed |= state.logos.insert_loaded(hash, icon);
        } else if connected {
            fetch(state, hash, tx);
        } else {
            state.logos.entries.remove(&hash);
        }
    }
    changed
}

fn fetch(state: &mut State, hash: String, tx: &mut Tx) {
    state.logos.entries.insert(hash.clone(), Entry::Fetching);
    let asked = hash.clone();
    state.ask(
        |request_id| HarnessLogoMessage {
            request_id,
            hash: asked,
            ..HarnessLogoMessage::new()
        },
        move |state, answer, _| {
            let parsed = answer
                .outcome()
                .ok()
                .and_then(|reply| reply.parse::<HarnessLogoResultMessage>())
                .filter(|r| r.success)
                .and_then(|r| r.logo);
            match parsed.and_then(|logo| build_icon(&hash, &logo).map(|icon| (logo, icon))) {
                Some((logo, icon)) => {
                    if let Some(dir) = state.logos.cache_dir.clone()
                        && valid_hash(&hash)
                    {
                        write_disk_logo(
                            dir,
                            hash.clone(),
                            logo,
                            state.logos.pending_writes.clone(),
                            state.logos.wake.clone(),
                        );
                    }
                    state.logos.insert_loaded(hash, icon);
                }
                None => {
                    state.logos.entries.insert(hash, Entry::Missing);
                }
            }
        },
        tx,
    );
}

pub(crate) fn disconnected(state: &mut State) {
    state
        .logos
        .entries
        .retain(|_, entry| matches!(entry, Entry::Loaded(_) | Entry::LoadingDisk));
}

pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    if state.window.tile.is_some() {
        return;
    }
    let _ = drain_disk(state, tx);
    let mapping: Vec<(String, String)> = state
        .facts
        .harnesses
        .iter()
        .map(|h| {
            (
                catalog::normalize(&h.id),
                h.logo.as_deref().map(str::trim).unwrap_or("").to_owned(),
            )
        })
        .collect();
    if state.logos.catalog_logos != mapping {
        state.logos.catalog_logos = mapping;
        state.logos.revision += 1;
    }
    let connected = state.facts.connection == attn_client::Connection::Connected;
    let hashes: Vec<String> = state
        .facts
        .harnesses
        .iter()
        .filter_map(|h| h.logo.as_deref().map(str::trim))
        .filter(|h| !h.is_empty())
        .map(str::to_owned)
        .collect();
    state
        .logos
        .entries
        .retain(|h, e| !matches!(e, Entry::Missing) || hashes.iter().any(|cur| cur == h));
    for hash in hashes {
        if state.logos.entries.contains_key(&hash) {
            continue;
        }
        if let Some(dir) = state.logos.cache_dir.clone()
            && valid_hash(&hash)
        {
            state.logos.entries.insert(hash.clone(), Entry::LoadingDisk);
            let inbox = state.logos.disk_inbox.clone();
            let wake = state.logos.wake.clone();
            std::thread::spawn(move || {
                let logo = read_disk_logo(&dir, &hash);
                inbox.lock().unwrap().push((hash, logo));
                if let Some(wake) = wake {
                    wake();
                }
            });
        } else if connected {
            fetch(state, hash, tx);
        }
    }
}

pub(crate) fn poll(state: &mut State, tx: &mut Tx) -> bool {
    drain_disk(state, tx)
}

/// The icon to draw for `harness_id`: `shell`, the cached logo for the harness's catalog hash,
/// or the generic mark while loading or absent.
pub(crate) fn of(state: &State, harness_id: &str) -> IconSource {
    if catalog::normalize(harness_id) == catalog::TERMINAL {
        return IconSource::Static(&SHELL);
    }
    if let Some(hash) = catalog::by_id(&state.facts.harnesses, harness_id)
        .and_then(|h| h.logo.as_deref())
        .map(str::trim)
        .filter(|h| !h.is_empty())
        && let Some(Entry::Loaded(icon)) = state.logos.entries.get(hash)
    {
        return IconSource::Owned(icon.clone());
    }
    IconSource::Static(&OTHER)
}
