use std::collections::BTreeMap;
use std::sync::{Arc, Mutex, Weak};

use attn_protocol::{
    Ask, AuthorState, Desktop, DesktopTileContentMessage, GitOperation, InitialStateMessage,
    OutpostInfo, PR, Profile, RepoState, Seed, Session, SessionLedgerEntry, UpdateRun, Vent,
    Worktree, WorktreeSweepEntry,
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
    pub garden: Arc<Garden>,
    /// The selected profile's open asks, then the ones answered in the last day.
    pub asks: Arc<Vec<Ask>>,
    /// The selected profile's vents: what its agents said got in their way.
    pub vents: Arc<Vec<Vent>>,
    pub closes: Arc<Closes>,
    /// Rises each time the daemon says attn's kept conversations changed.
    pub kept_conversations: u64,
    /// Rises each time the daemon says its background tasks changed.
    pub tasks_changed: u64,
    pub github: Arc<GitHub>,
    pub worktrees: Arc<WorktreePushes>,
    /// The paths git operations run on now, by operation id.
    pub git_running: Arc<BTreeMap<String, String>>,
    pub notifications: Notifications,
    /// The latest update run, open or finished, when the home has had one.
    pub update_run: Arc<Option<UpdateRun>>,
    pub pull_requests: Arc<PullRequests>,
}

/// The user's pull requests and review requests the daemon polled from GitHub, with the
/// repositories and authors the user muted.
#[derive(Clone, Debug, Default)]
pub struct PullRequests {
    pub prs: Vec<PR>,
    pub repos: Vec<RepoState>,
    pub authors: Vec<AuthorState>,
}

/// The selected profile's unread notifications, as counted by the daemon's last list answer
/// or broadcast.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Notifications {
    pub unread: u32,
    pub unread_critical: u32,
    /// The newest unread critical notification's title, while one is unread.
    pub critical_title: Option<String>,
    /// Rises with each broadcast that the feed changed, so an open feed reads it again.
    pub changes: u64,
}

/// The GitHub hosts the daemon found signed in, or why it does not look for pull requests.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct GitHub {
    pub hosts: Vec<String>,
    pub polling_off_reason: Option<String>,
}

/// What the daemon pushed about worktrees, numbered in arrival order, so a list read earlier
/// takes in what changed since.
#[derive(Clone, Default)]
pub struct WorktreePushes {
    /// How many pushes this process has heard of.
    pub seq: u64,
    /// Each worktree's latest pushed state, and its push number.
    pub observed: BTreeMap<String, (u64, Worktree)>,
    /// The last [`SWEEPS_KEPT`] sweeps, newest last, with their push numbers.
    pub swept: Vec<(u64, WorktreeSweepEntry)>,
}

/// One worktree push.
pub enum WorktreePush<'a> {
    Observed(&'a Worktree),
    Swept(&'a WorktreeSweepEntry),
}

/// As many sweeps as the Tauri app keeps from pushes; a reader that missed more reads again.
pub const SWEEPS_KEPT: usize = 200;

impl WorktreePushes {
    /// The pushes after push number `seen`, oldest first, or `None` when some sweep among them
    /// is no longer kept.
    pub fn since(&self, seen: u64) -> Option<Vec<WorktreePush<'_>>> {
        if self.swept.len() == SWEEPS_KEPT && self.swept[0].0 > seen + 1 {
            return None;
        }
        let mut news: Vec<(u64, WorktreePush<'_>)> = self
            .observed
            .values()
            .filter(|(at, _)| *at > seen)
            .map(|(at, w)| (*at, WorktreePush::Observed(w)))
            .chain(
                self.swept
                    .iter()
                    .filter(|(at, _)| *at > seen)
                    .map(|(at, e)| (*at, WorktreePush::Swept(e))),
            )
            .collect();
        news.sort_by_key(|(at, _)| *at);
        Some(news.into_iter().map(|(_, push)| push).collect())
    }
}

/// The sessions the daemon said closed, newest last, as their ledger rows.
#[derive(Clone, Default)]
pub struct Closes {
    /// How many closes this process has heard of; the last of `recent` is close number `seq`.
    pub seq: u64,
    /// The last [`CLOSES_KEPT`] closes. A reader that missed more than these reads the ledger
    /// again.
    pub recent: Vec<SessionLedgerEntry>,
}

/// Far past the closes that arrive between two frames, even when a desktop of agents closes at
/// once; a reader that falls further behind reads its page again rather than losing one.
pub const CLOSES_KEPT: usize = 64;

impl Closes {
    /// The closes after close number `seen`, oldest first, or `None` when some of them are no
    /// longer kept.
    pub fn since(&self, seen: u64) -> Option<&[SessionLedgerEntry]> {
        let missed = usize::try_from(self.seq.saturating_sub(seen)).ok()?;
        self.recent
            .len()
            .checked_sub(missed)
            .map(|from| &self.recent[from..])
    }
}

/// Document tiles' contents by desktop and tile.
#[derive(Clone, Default)]
pub struct TileContents {
    /// Rises with every change to the tiles, so a view can key a `memo` on it.
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
        kept.revision += 1;
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

/// One profile's garden as the daemon pushes it: its newest seeds, newest first, and how many it
/// holds in all. A seed the daemon did not change keeps its `Arc`, so a row built from it is
/// reused.
#[derive(Clone, Default)]
pub struct Garden {
    pub profile: String,
    pub seeds: Vec<Arc<Seed>>,
    pub total: i64,
}

impl Garden {
    /// The seeds, when they are the profile's that `selected` names; none for another's.
    pub fn of(&self, selected: &str) -> &[Arc<Seed>] {
        if self.profile == selected {
            &self.seeds
        } else {
            &[]
        }
    }

    /// How many seeds the garden of the profile `selected` names holds.
    pub fn total_of(&self, selected: &str) -> i64 {
        if self.profile == selected {
            self.total
        } else {
            0
        }
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
    /// The link lost the daemon, or never reached it, and retries on its own. `start_failed`
    /// says why the last try to start the daemon failed.
    Reconnecting {
        start_failed: Option<String>,
    },
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
    pub const GARDEN: Slices = Slices(1 << 7);
    pub const ASKS: Slices = Slices(1 << 8);
    pub const CLOSES: Slices = Slices(1 << 9);
    pub const KEPT_CONVERSATIONS: Slices = Slices(1 << 10);
    pub const GITHUB: Slices = Slices(1 << 11);
    pub const WORKTREES: Slices = Slices(1 << 12);
    pub const GIT_OPERATIONS: Slices = Slices(1 << 13);
    pub const TASKS: Slices = Slices(1 << 14);
    pub const NOTIFICATIONS: Slices = Slices(1 << 15);
    pub const VENTS: Slices = Slices(1 << 16);
    pub const UPDATE_RUN: Slices = Slices(1 << 17);
    pub const PULL_REQUESTS: Slices = Slices(1 << 18);
    pub const ALL: Slices = Slices((1 << 19) - 1);

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
            let seeds: Vec<Arc<Seed>> = state
                .seeds
                .unwrap_or_default()
                .into_iter()
                .map(Arc::new)
                .collect();
            facts.garden = Arc::new(Garden {
                profile: facts.profiles.selected.clone(),
                total: state.seeds_total.unwrap_or(seeds.len() as i64),
                seeds,
            });
            facts.asks = Arc::new(state.asks.unwrap_or_default());
            facts.vents = Arc::new(state.vents.unwrap_or_default());
            facts.update_run = Arc::new(state.update_run);
            facts.pull_requests = Arc::new(PullRequests {
                prs: state.prs.unwrap_or_default(),
                repos: state.repos.unwrap_or_default(),
                authors: state.authors.unwrap_or_default(),
            });
            facts.github = Arc::new(GitHub {
                hosts: state.github_hosts.unwrap_or_default(),
                polling_off_reason: state.github_polling_off_reason.filter(|r| !r.is_empty()),
            });
            facts.git_running = Arc::default();
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

    pub(crate) fn set_github(&self, github: GitHub) {
        self.change(|facts| {
            if *facts.github == github {
                return Slices::NONE;
            }
            facts.github = Arc::new(github);
            Slices::GITHUB
        });
    }

    /// The feed's counts; `changed` says the feed itself changed, as a broadcast does and a
    /// list answer does not.
    pub(crate) fn count_notifications(
        &self,
        unread: u32,
        unread_critical: u32,
        critical_title: Option<String>,
        changed: bool,
    ) {
        self.change(|facts| {
            let was = facts.notifications.clone();
            let n = &mut facts.notifications;
            n.unread = unread;
            n.unread_critical = unread_critical;
            n.critical_title = critical_title.filter(|_| unread_critical > 0);
            n.changes += u64::from(changed);
            if *n == was {
                return Slices::NONE;
            }
            Slices::NOTIFICATIONS
        });
    }

    pub(crate) fn set_outposts(&self, outposts: Vec<OutpostInfo>) {
        self.change(|facts| {
            facts.outposts = Arc::new(outposts);
            Slices::OUTPOSTS
        });
    }

    /// The selected profile's whole garden window, as the daemon sends it on connect and profile
    /// switch. A window for another profile arrived late and is dropped.
    pub(crate) fn set_garden(&self, profile: String, seeds: Vec<Seed>, total: i64) {
        self.change(|facts| {
            if facts.profiles.selected != profile {
                return Slices::NONE;
            }
            facts.garden = Arc::new(Garden {
                profile,
                seeds: seeds.into_iter().map(Arc::new).collect(),
                total,
            });
            Slices::GARDEN
        });
    }

    /// The garden window in the daemon's `order`, from the seeds that changed and the ones this
    /// store already holds. A change for another garden than the selected profile's one held is
    /// dropped: the whole window of the new profile comes first.
    pub(crate) fn change_garden(
        &self,
        profile: String,
        order: Vec<String>,
        changed: Vec<Seed>,
        total: i64,
    ) {
        self.change(|facts| {
            if facts.garden.profile != profile || facts.profiles.selected != profile {
                return Slices::NONE;
            }
            let mut known: std::collections::HashMap<&str, &Arc<Seed>> = facts
                .garden
                .seeds
                .iter()
                .map(|s| (s.id.as_str(), s))
                .collect();
            let changed: Vec<Arc<Seed>> = changed.into_iter().map(Arc::new).collect();
            for seed in &changed {
                known.insert(seed.id.as_str(), seed);
            }
            let mut missing = Vec::new();
            let seeds = order
                .iter()
                .filter_map(|id| {
                    let seed = known.get(id.as_str()).map(|s| Arc::clone(s));
                    if seed.is_none() {
                        missing.push(id.as_str());
                    }
                    seed
                })
                .collect();
            if !missing.is_empty() {
                eprintln!(
                    "attn-app: a garden change lists seeds this app never received: {}",
                    missing.join(", ")
                );
            }
            facts.garden = Arc::new(Garden {
                profile,
                seeds,
                total,
            });
            Slices::GARDEN
        });
    }

    /// A profile's asks; another profile's change nothing here.
    pub(crate) fn set_asks(&self, profile: &str, asks: Vec<Ask>) {
        self.change(|facts| {
            if profile != facts.profiles.selected {
                return Slices::NONE;
            }
            facts.asks = Arc::new(asks);
            Slices::ASKS
        });
    }

    /// A profile's vents; another profile's change nothing here.
    pub(crate) fn set_vents(&self, profile: &str, vents: Vec<Vent>) {
        self.change(|facts| {
            if profile != facts.profiles.selected {
                return Slices::NONE;
            }
            facts.vents = Arc::new(vents);
            Slices::VENTS
        });
    }

    pub(crate) fn change_pull_requests(&self, change: impl FnOnce(&mut PullRequests)) {
        self.change(|facts| {
            change(Arc::make_mut(&mut facts.pull_requests));
            Slices::PULL_REQUESTS
        });
    }

    pub(crate) fn set_update_run(&self, run: UpdateRun) {
        self.change(|facts| {
            facts.update_run = Arc::new(Some(run));
            Slices::UPDATE_RUN
        });
    }

    pub(crate) fn tasks_changed(&self) {
        self.change(|facts| {
            facts.tasks_changed += 1;
            Slices::TASKS
        });
    }

    pub(crate) fn kept_conversations_changed(&self) {
        self.change(|facts| {
            facts.kept_conversations += 1;
            Slices::KEPT_CONVERSATIONS
        });
    }

    pub(crate) fn observe_worktrees(&self, worktrees: Vec<Worktree>) {
        self.change(|facts| {
            let pushes = Arc::make_mut(&mut facts.worktrees);
            for worktree in worktrees {
                pushes.seq += 1;
                pushes
                    .observed
                    .insert(worktree.path.clone(), (pushes.seq, worktree));
            }
            Slices::WORKTREES
        });
    }

    pub(crate) fn sweep_worktree(&self, entry: WorktreeSweepEntry) {
        self.change(|facts| {
            let pushes = Arc::make_mut(&mut facts.worktrees);
            pushes.seq += 1;
            pushes.observed.remove(&entry.path);
            pushes.swept.push((pushes.seq, entry));
            let over = pushes.swept.len().saturating_sub(SWEEPS_KEPT);
            pushes.swept.drain(..over);
            Slices::WORKTREES
        });
    }

    pub(crate) fn git_operation(&self, operation: GitOperation) {
        self.change(|facts| {
            let running = Arc::make_mut(&mut facts.git_running);
            match (operation.status.as_str(), operation.path) {
                ("running", Some(path)) => {
                    running.insert(operation.id, path);
                }
                _ => {
                    if running.remove(&operation.id).is_none() {
                        return Slices::NONE;
                    }
                }
            }
            Slices::GIT_OPERATIONS
        });
    }

    pub(crate) fn add_close(&self, entry: SessionLedgerEntry) {
        self.change(|facts| {
            let mut closes = (*facts.closes).clone();
            closes.seq += 1;
            closes.recent.push(entry);
            let over = closes.recent.len().saturating_sub(CLOSES_KEPT);
            closes.recent.drain(..over);
            facts.closes = Arc::new(closes);
            Slices::CLOSES
        });
    }

    /// A profile's desktops and its current desktop, as one arrangement. The daemon sends a
    /// connection only the arrangement of the profile it scoped the connection to, so the
    /// arrangement's profile becomes the selected one, as after a switch or after the daemon
    /// deleted the profile this window showed. The previous profile's asks and vents go with it.
    pub(crate) fn apply_arrangement(&self, profile: Profile, desktops: Vec<Desktop>) {
        self.change(|facts| {
            let mut changed = Slices::PROFILES | Slices::DESKTOPS;
            let mut profiles = (*facts.profiles).clone();
            if profiles.selected != profile.id {
                profiles.selected = profile.id.clone();
                facts.asks = Arc::default();
                facts.vents = Arc::default();
                changed |= Slices::ASKS | Slices::VENTS;
            }
            match profiles.all.iter_mut().find(|p| p.id == profile.id) {
                Some(known) => *known = profile,
                None => profiles.all.push(profile),
            }
            facts.profiles = Arc::new(profiles);
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
