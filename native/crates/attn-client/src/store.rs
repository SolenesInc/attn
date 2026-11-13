use std::collections::BTreeMap;
use std::sync::{Arc, Mutex, Weak};

use attn_protocol::{
    Desktop, DesktopTileContentMessage, InitialStateMessage, OutpostInfo, Profile, Session,
};

/// The daemon's facts as this process last heard them. Each slice sits behind its own `Arc`,
/// so a slice that did not change keeps its pointer and a view's `memo` can key on it.
#[derive(Clone, Default)]
pub struct Facts {
    pub connection: Connection,
    /// The daemon process the connection reached; a new one started every terminal's stream
    /// over.
    pub daemon_instance: String,
    pub sessions: Arc<Vec<Session>>,
    pub profiles: Arc<Profiles>,
    pub desktops: Arc<Vec<Desktop>>,
    pub settings: Arc<BTreeMap<String, String>>,
    /// The machines that host remote sessions, by the id a session's `host` names.
    pub outposts: Arc<Vec<OutpostInfo>>,
    /// What each document tile holds, as the daemon last sent it.
    pub tile_contents: Arc<TileContents>,
}

/// Document tiles' contents by desktop and tile.
#[derive(Clone, Default)]
pub struct TileContents {
    /// Rises with every content the daemon sends, so a view can key a `memo` on it.
    pub revision: u64,
    pub tiles: BTreeMap<TileKey, TileContent>,
}

#[derive(Clone)]
pub struct TileContent {
    /// The contents' revision when this arrived.
    pub revision: u64,
    pub message: Arc<DesktopTileContentMessage>,
}

impl TileContents {
    pub fn get(&self, key: &TileKey) -> Option<&TileContent> {
        self.tiles.get(key)
    }

    fn held_by(&self, desktops: &[Desktop]) -> Option<TileContents> {
        if self.tiles.keys().all(|k| holds_tile(desktops, k)) {
            return None;
        }
        let mut kept = self.clone();
        kept.tiles.retain(|k, _| holds_tile(desktops, k));
        Some(kept)
    }
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct TileKey {
    pub desktop: String,
    pub tile: String,
}

impl Facts {
    /// The desktop the selected profile shows.
    pub fn current_desktop(&self) -> Option<&Desktop> {
        let profile = self
            .profiles
            .all
            .iter()
            .find(|p| p.id == self.profiles.selected)?;
        self.desktops
            .iter()
            .find(|d| d.id == profile.current_desktop_id)
    }
}

#[derive(Clone, Default)]
pub struct Profiles {
    pub all: Vec<Profile>,
    pub selected: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub enum Connection {
    #[default]
    Connecting,
    Connected,
    /// The link lost the daemon, or never reached it, and retries on its own.
    Reconnecting,
    /// The link stopped and will not retry: the user has to act.
    Refused(Refusal),
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Refusal {
    ProtocolMismatch { app: String, daemon: String },
    InstanceMismatch { app: String, daemon: String },
    Unauthorized(String),
}

/// Which slices of `Facts` a change touched, and which a window reads.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct Slices(u32);

impl Slices {
    pub const NONE: Slices = Slices(0);
    pub const CONNECTION: Slices = Slices(1);
    pub const SESSIONS: Slices = Slices(1 << 1);
    pub const PROFILES: Slices = Slices(1 << 2);
    pub const DESKTOPS: Slices = Slices(1 << 3);
    pub const SETTINGS: Slices = Slices(1 << 4);
    pub const OUTPOSTS: Slices = Slices(1 << 5);
    pub const TILE_CONTENTS: Slices = Slices(1 << 6);
    pub const ALL: Slices = Slices((1 << 7) - 1);

    pub fn intersects(self, other: Slices) -> bool {
        self.0 & other.0 != 0
    }

    pub fn is_empty(self) -> bool {
        self.0 == 0
    }
}

impl std::ops::BitOr for Slices {
    type Output = Slices;
    fn bitor(self, other: Slices) -> Slices {
        Slices(self.0 | other.0)
    }
}

impl std::ops::BitOrAssign for Slices {
    fn bitor_assign(&mut self, other: Slices) {
        self.0 |= other.0;
    }
}

type Notify = dyn Fn(&Facts, Slices) + Send + Sync;

struct Subscriber {
    id: u64,
    reads: Slices,
    notify: Box<Notify>,
}

#[derive(Default)]
struct Inner {
    facts: Facts,
    subscribers: Vec<Subscriber>,
    next_id: u64,
}

/// One per process. The link writes it from its own thread; each window subscribes to the
/// slices it reads and hears only about changes to those.
#[derive(Clone, Default)]
pub struct Store(Arc<Mutex<Inner>>);

/// Ends its subscription when dropped.
pub struct Subscription {
    store: Weak<Mutex<Inner>>,
    id: u64,
}

impl Drop for Subscription {
    fn drop(&mut self) {
        if let Some(store) = self.store.upgrade() {
            store
                .lock()
                .unwrap()
                .subscribers
                .retain(|s| s.id != self.id);
        }
    }
}

impl Store {
    pub fn facts(&self) -> Facts {
        self.0.lock().unwrap().facts.clone()
    }

    /// Calls `notify` now with every slice, then on the link's thread after each change to a
    /// slice in `reads`, in the order the changes happen. `notify` runs while the store is
    /// locked, so it hands the facts on and must not call the store.
    pub fn subscribe(
        &self,
        reads: Slices,
        notify: impl Fn(&Facts, Slices) + Send + Sync + 'static,
    ) -> Subscription {
        let mut inner = self.0.lock().unwrap();
        notify(&inner.facts, Slices::ALL);
        let id = inner.next_id;
        inner.next_id += 1;
        inner.subscribers.push(Subscriber {
            id,
            reads,
            notify: Box::new(notify),
        });
        Subscription {
            store: Arc::downgrade(&self.0),
            id,
        }
    }

    pub(crate) fn set_connection(&self, connection: Connection) {
        self.change(|facts| {
            if facts.connection == connection {
                return Slices::NONE;
            }
            facts.connection = connection;
            Slices::CONNECTION
        });
    }

    pub(crate) fn apply_initial_state(&self, state: InitialStateMessage) {
        self.change(|facts| {
            facts.connection = Connection::Connected;
            facts.daemon_instance = state.daemon_instance_id.unwrap_or_default();
            facts.sessions = Arc::new(state.sessions.unwrap_or_default());
            facts.profiles = Arc::new(Profiles {
                all: state.profiles.unwrap_or_default(),
                selected: state.selected_profile_id.unwrap_or_default(),
            });
            facts.desktops = Arc::new(state.desktops.unwrap_or_default());
            if let Some(kept) = facts.tile_contents.held_by(&facts.desktops) {
                facts.tile_contents = Arc::new(kept);
            }
            facts.settings = Arc::new(state.settings.unwrap_or_default());
            facts.outposts = Arc::new(state.outposts.unwrap_or_default());
            Slices::ALL
        });
    }

    pub(crate) fn set_settings(&self, settings: BTreeMap<String, String>) {
        self.change(|facts| {
            if *facts.settings == settings {
                return Slices::NONE;
            }
            facts.settings = Arc::new(settings);
            Slices::SETTINGS
        });
    }

    pub(crate) fn set_outposts(&self, outposts: Vec<OutpostInfo>) {
        self.change(|facts| {
            facts.outposts = Arc::new(outposts);
            Slices::OUTPOSTS
        });
    }

    /// A profile's desktops and its current desktop, as one arrangement. Another profile's
    /// arrangement changes only its record.
    pub(crate) fn apply_arrangement(&self, profile: Profile, desktops: Vec<Desktop>) {
        self.change(|facts| {
            let shown = profile.id == facts.profiles.selected;
            let mut profiles = (*facts.profiles).clone();
            match profiles.all.iter_mut().find(|p| p.id == profile.id) {
                Some(known) => *known = profile,
                None => profiles.all.push(profile),
            }
            facts.profiles = Arc::new(profiles);
            if !shown {
                return Slices::PROFILES;
            }
            let mut changed = Slices::PROFILES | Slices::DESKTOPS;
            if let Some(kept) = facts.tile_contents.held_by(&desktops) {
                facts.tile_contents = Arc::new(kept);
                changed |= Slices::TILE_CONTENTS;
            }
            facts.desktops = Arc::new(desktops);
            changed
        });
    }

    pub(crate) fn set_tile_content(&self, content: DesktopTileContentMessage) {
        self.change(|facts| {
            let key = TileKey {
                desktop: content.desktop_id.clone(),
                tile: content.tile_id.clone(),
            };
            let mut contents = (*facts.tile_contents).clone();
            contents.revision += 1;
            let revision = contents.revision;
            contents.tiles.insert(
                key,
                TileContent {
                    revision,
                    message: Arc::new(content),
                },
            );
            facts.tile_contents = Arc::new(contents);
            Slices::TILE_CONTENTS
        });
    }

    /// A session the daemon registered or changed takes its place, or joins at the end.
    pub(crate) fn upsert_session(&self, session: Session) {
        self.change(|facts| {
            let mut sessions = (*facts.sessions).clone();
            match sessions.iter_mut().find(|s| s.id == session.id) {
                Some(known) => *known = session,
                None => sessions.push(session),
            }
            facts.sessions = Arc::new(sessions);
            Slices::SESSIONS
        });
    }

    pub(crate) fn remove_session(&self, id: &str) {
        self.change(|facts| {
            if !facts.sessions.iter().any(|s| s.id == id) {
                return Slices::NONE;
            }
            facts.sessions = Arc::new(
                facts
                    .sessions
                    .iter()
                    .filter(|s| s.id != id)
                    .cloned()
                    .collect(),
            );
            Slices::SESSIONS
        });
    }

    /// The daemon's whole list, each session once.
    pub(crate) fn set_sessions(&self, sessions: Vec<Session>) {
        self.change(|facts| {
            let mut seen = std::collections::HashSet::new();
            let sessions = sessions
                .into_iter()
                .filter(|s| seen.insert(s.id.clone()))
                .collect();
            facts.sessions = Arc::new(sessions);
            Slices::SESSIONS
        });
    }

    pub(crate) fn set_profiles(&self, all: Vec<Profile>) {
        self.change(|facts| {
            let selected = facts.profiles.selected.clone();
            facts.profiles = Arc::new(Profiles { all, selected });
            Slices::PROFILES
        });
    }

    fn change(&self, f: impl FnOnce(&mut Facts) -> Slices) {
        let mut inner = self.0.lock().unwrap();
        let changed = f(&mut inner.facts);
        if changed.is_empty() {
            return;
        }
        for subscriber in inner
            .subscribers
            .iter()
            .filter(|s| s.reads.intersects(changed))
        {
            (subscriber.notify)(&inner.facts, changed);
        }
    }
}

/// Whether a desktop in `desktops` still holds the tile `key` names.
fn holds_tile(desktops: &[Desktop], key: &TileKey) -> bool {
    fn within(node: &serde_json::Value, tile: &str) -> bool {
        node.get("tile_id").and_then(serde_json::Value::as_str) == Some(tile)
            || node
                .get("children")
                .and_then(serde_json::Value::as_array)
                .is_some_and(|children| children.iter().any(|c| within(c, tile)))
    }
    desktops
        .iter()
        .find(|d| d.id == key.desktop)
        .and_then(|d| serde_json::from_str(&d.tree_json).ok())
        .is_some_and(|tree| within(&tree, &key.tile))
}
