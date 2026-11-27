use std::collections::BTreeMap;
use std::sync::{Arc, Mutex, Weak};

use crate::layout::{Leaf, Node};
use attn_protocol::{
    Ask, AuthorState, CrewMember, DaemonWarning, DesktopTileContentMessage, GardenBed,
    GardenClosedCounts, GitOperation, Harness, InitialStateMessage, OutpostInfo, PR, PairedSender,
    PhonesUpdatedMessage, PluginInfo, PluginIssue, Profile, RepoState, Seed, SenderPairingRequest,
    SendersUpdatedMessage, Session, SessionLedgerEntry, SharedCodexState, TerminalBinding,
    UpdateRun, Vent, Worktree, WorktreeSweepEntry,
};

/// A desktop as the daemon sent it, with its layout tree read once on arrival.
#[derive(Clone, Debug, Default)]
pub struct Desktop {
    sent: attn_protocol::Desktop,
    tree: Option<Node>,
}

impl Desktop {
    /// The desktop's tiles; None for an empty desktop or a tree this app cannot read.
    pub fn tree(&self) -> Option<&Node> {
        self.tree.as_ref()
    }

    /// The desktop's leaves in reading order: first child before second.
    pub fn leaves(&self) -> Vec<&Leaf> {
        self.tree().map(Node::leaves).unwrap_or_default()
    }
}

impl From<attn_protocol::Desktop> for Desktop {
    fn from(sent: attn_protocol::Desktop) -> Desktop {
        let tree = Node::parse(&sent.tree_json);
        Desktop { sent, tree }
    }
}

impl serde::Serialize for Desktop {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        self.sent.serialize(serializer)
    }
}

impl std::ops::Deref for Desktop {
    type Target = attn_protocol::Desktop;

    fn deref(&self) -> &attn_protocol::Desktop {
        &self.sent
    }
}

fn arrived(desktops: Vec<attn_protocol::Desktop>) -> Arc<Vec<Desktop>> {
    Arc::new(desktops.into_iter().map(Desktop::from).collect())
}

/// The shared Codex app server's state, as the daemon said it of `profile_id`.
#[derive(Clone, Debug, Default)]
pub struct SharedCodex {
    pub profile_id: String,
    pub state: Option<SharedCodexState>,
}

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
    /// Every harness the home can run a session on, offered or not, as the daemon last sent it.
    pub harnesses: Arc<Vec<Harness>>,
    /// Whether the daemon has pushed its harness catalog on this connection.
    pub harnesses_loaded: bool,
    /// The machines that host remote sessions, by the id a session's `host` names.
    pub outposts: Arc<Vec<OutpostInfo>>,
    /// The name of the home's own machine, which an address names it by as well as `home`.
    pub home_name: String,
    /// The machine this app runs on, as its own daemon's `/health` last said.
    pub machine: AppMachine,
    /// The build the home daemon runs, which an outpost on another build is offered.
    pub home_build: String,
    /// The Codex app server a profile's Codex tiles share, by the profile the daemon last said
    /// it of.
    pub shared_codex: Arc<Option<SharedCodex>>,
    /// The phones' link to Attn Cloud, their pairings and the pairing codes shown; `None` until
    /// the daemon first lists them.
    pub phones: Arc<Option<PhonesUpdatedMessage>>,
    /// The programs paired to send as the selected profile; `None` until the daemon first
    /// lists them.
    pub senders: Arc<Option<Vec<PairedSender>>>,
    /// The program waiting for the user to allow or deny it as a sender; the daemon asks about
    /// one at a time.
    pub sender_pairing: Arc<Option<SenderPairingRequest>>,
    /// Every crew member of every profile, in session or not.
    pub crew: Arc<Vec<CrewMember>>,
    /// When the daemon last said an agent retrieved a quick capture from its inbox, by capture
    /// id; `None` once it said the capture is unread again.
    pub capture_reads: Arc<BTreeMap<String, Option<String>>>,
    /// What each document tile holds, as the daemon last sent it.
    pub tile_contents: Arc<TileContents>,
    pub garden: Arc<Garden>,
    /// The selected profile's open asks, then the ones answered in the last day.
    pub asks: Arc<Vec<Ask>>,
    /// What the daemon warned of at the last handshake, such as terminals it could not
    /// recover.
    pub warnings: Arc<Vec<DaemonWarning>>,
    /// The selected profile's vents: what its agents said got in their way.
    pub vents: Arc<Vec<Vent>>,
    pub closes: Arc<Closes>,
    /// Rises each time the daemon says attn's kept conversations changed.
    pub kept_conversations: u64,
    /// Rises for a root, the resolved absolute folder, each time the daemon says files under it
    /// changed.
    pub file_changes: Arc<BTreeMap<String, u64>>,
    /// Rises each time the daemon says its background tasks changed.
    pub tasks_changed: u64,
    /// Rises each time the daemon says the delegation preferences changed.
    pub delegation_changed: u64,
    /// Rises each time the daemon says automation definitions or their runs changed.
    pub automations_changed: u64,
    pub github: Arc<GitHub>,
    pub worktrees: Arc<WorktreePushes>,
    /// The paths git operations run on now, by operation id.
    pub git_running: Arc<BTreeMap<String, String>>,
    pub notifications: Notifications,
    /// The latest update run, open or finished, when the home has had one.
    pub update_run: Arc<Option<UpdateRun>>,
    pub pull_requests: Arc<PullRequests>,
    /// The installed plugins and the plugin folders that failed to load, as the daemon last
    /// listed them; empty until the first list.
    pub plugins: Arc<Plugins>,
}

#[derive(Clone, Debug, Default)]
pub struct Plugins {
    pub plugins: Vec<PluginInfo>,
    pub issues: Vec<PluginIssue>,
}

/// The user's pull requests and review requests the daemon polled from GitHub, with the
/// repositories and authors the user muted.
#[derive(Clone, Debug, Default)]
pub struct PullRequests {
    pub prs: Vec<PR>,
    pub repos: Vec<RepoState>,
    pub authors: Vec<AuthorState>,
    /// When GitHub lets the daemon poll again, as the daemon's last `rate_limited` said.
    pub rate_limited_until: Option<String>,
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

/// The sweeps kept from pushes; a reader that missed more reads again.
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
    pub beds: Vec<GardenBed>,
    pub closed_counts: Option<GardenClosedCounts>,
    pub revision: u64,
    pub full_push: u64,
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

    /// The curated beds of the profile `selected` names; none for another's.
    pub fn beds_of(&self, selected: &str) -> &[GardenBed] {
        if self.profile == selected {
            &self.beds
        } else {
            &[]
        }
    }

    /// Exact root-level closed counts of the profile `selected` names, when the daemon sent them.
    pub fn closed_counts_of(&self, selected: &str) -> Option<&GardenClosedCounts> {
        if self.profile == selected {
            self.closed_counts.as_ref()
        } else {
            None
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
    /// The daemon on the app's port passes the app to its home, and cannot reach it now. That
    /// daemon closes the connection once its dial to the home changes, and the link connects
    /// again; the screen stays until the home answers.
    HomeUnreachable(HomeUnreachable),
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct HomeUnreachable {
    pub home: String,
    /// How the daemon's dial to the home stands: `dialing`, `refused`, or `off`.
    pub dial: String,
    pub reason: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Refusal {
    ProtocolMismatch {
        app: String,
        daemon: String,
    },
    InstanceMismatch {
        app: String,
        daemon: String,
    },
    Unauthorized(String),
    /// Starting the daemon failed, and it left a marker saying a data migration failed: the
    /// user takes the details to someone who can repair the data.
    MigrationFailed(MigrationMarker),
}

/// The daemon's `migration-failure.json`, as the app found it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct MigrationMarker {
    pub path: String,
    /// The file's text, or why it could not be read.
    pub contents: Result<String, String>,
}

impl MigrationMarker {
    /// The marker at `path`, if the daemon left one there.
    pub fn read(path: &std::path::Path) -> Option<MigrationMarker> {
        let contents = match std::fs::read_to_string(path) {
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return None,
            read => read.map_err(|error| error.to_string()),
        };
        Some(MigrationMarker {
            path: path.display().to_string(),
            contents,
        })
    }
}

/// The machine this app runs on: the id its home knows it by, and the home's name when the home
/// runs on another machine. The terminals that run here then go over the app's own link to this
/// machine's daemon, so they keep echoing at local speed, and keep working while the home is
/// away.
#[derive(Clone, Debug, Default)]
pub struct AppMachine {
    pub id: String,
    pub home: Option<String>,
    /// Whether the link to this machine's own daemon has its state.
    pub connected: bool,
    /// The daemon process that link reached; a new one starts its terminals' streams over.
    pub daemon_instance: String,
    /// The sessions that run here, as this machine's daemon last listed them.
    pub sessions: Arc<Vec<Session>>,
    /// The terminal each of those sessions runs in.
    pub terminals: Arc<Vec<TerminalBinding>>,
    /// This machine's settings, as its own daemon last listed them.
    pub settings: Arc<BTreeMap<String, String>>,
    /// This machine's own latest update run, such as its update of its home, which the home
    /// cannot show while it refuses this machine.
    pub update_run: Option<Arc<UpdateRun>>,
}

impl PartialEq for AppMachine {
    fn eq(&self, other: &AppMachine) -> bool {
        self.id == other.id
            && self.home == other.home
            && self.connected == other.connected
            && self.daemon_instance == other.daemon_instance
            && Arc::ptr_eq(&self.sessions, &other.sessions)
            && Arc::ptr_eq(&self.terminals, &other.terminals)
            && Arc::ptr_eq(&self.settings, &other.settings)
            && match (&self.update_run, &other.update_run) {
                (Some(ours), Some(theirs)) => Arc::ptr_eq(ours, theirs),
                (None, None) => true,
                _ => false,
            }
    }
}

impl AppMachine {
    /// Whether the session `host` names runs on this machine while its home runs on another,
    /// so this machine's own daemon serves it.
    pub fn runs(&self, host: Option<&str>) -> bool {
        self.home.is_some() && !self.id.is_empty() && host == Some(self.id.as_str())
    }
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
    pub const PLUGINS: Slices = Slices(1 << 19);
    pub const DELEGATION: Slices = Slices(1 << 20);
    pub const PHONES: Slices = Slices(1 << 21);
    pub const SENDERS: Slices = Slices(1 << 22);
    pub const HARNESSES: Slices = Slices(1 << 23);
    pub const FILES: Slices = Slices(1 << 24);
    pub const CREW: Slices = Slices(1 << 25);
    pub const CAPTURE_READS: Slices = Slices(1 << 26);
    pub const SHARED_CODEX: Slices = Slices(1 << 27);
    pub const AUTOMATIONS: Slices = Slices(1 << 28);
    pub const MACHINE: Slices = Slices(1 << 29);
    pub const ALL: Slices = Slices((1 << 30) - 1);

    pub const fn union(self, other: Slices) -> Slices {
        Slices(self.0 | other.0)
    }

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

    pub(crate) fn set_machine_connected(&self, connected: bool) {
        self.change_machine(|machine| machine.connected = connected);
    }

    pub(crate) fn machine_greeted(
        &self,
        daemon_instance: String,
        sessions: Vec<Session>,
        terminals: Vec<TerminalBinding>,
        settings: BTreeMap<String, String>,
        update_run: Option<UpdateRun>,
    ) {
        self.change_machine(|machine| {
            machine.connected = true;
            machine.daemon_instance = daemon_instance;
            machine.sessions = Arc::new(sessions);
            machine.terminals = Arc::new(terminals);
            machine.settings = Arc::new(settings);
            machine.update_run = update_run.map(Arc::new);
        });
    }

    pub(crate) fn set_machine_settings(&self, settings: BTreeMap<String, String>) {
        self.change_machine(|machine| machine.settings = Arc::new(settings));
    }

    pub(crate) fn set_machine_update_run(&self, run: UpdateRun) {
        self.change_machine(|machine| machine.update_run = Some(Arc::new(run)));
    }

    pub(crate) fn set_machine_terminals(&self, terminals: Vec<TerminalBinding>) {
        self.change_machine(|machine| machine.terminals = Arc::new(terminals));
    }

    pub(crate) fn change_machine_sessions(&self, change: impl FnOnce(&mut Vec<Session>)) {
        self.change_machine(|machine| {
            let mut sessions = (*machine.sessions).clone();
            change(&mut sessions);
            machine.sessions = Arc::new(sessions);
        });
    }

    fn change_machine(&self, change: impl FnOnce(&mut AppMachine)) {
        self.change(|facts| {
            let mut machine = facts.machine.clone();
            change(&mut machine);
            if machine == facts.machine {
                return Slices::NONE;
            }
            facts.machine = machine;
            Slices::MACHINE
        });
    }

    pub(crate) fn apply_initial_state(&self, state: InitialStateMessage) {
        self.change(|facts| {
            facts.connection = Connection::Connected;
            facts.machine.update_run = None;
            facts.daemon_instance = state.daemon_instance_id.unwrap_or_default();
            facts.sessions = Arc::new(state.sessions.unwrap_or_default());
            facts.shared_codex = Arc::new(Some(SharedCodex {
                profile_id: state.selected_profile_id.clone().unwrap_or_default(),
                state: state.shared_codex,
            }));
            facts.profiles = Arc::new(Profiles {
                all: state.profiles.unwrap_or_default(),
                selected: state.selected_profile_id.unwrap_or_default(),
            });
            facts.desktops = arrived(state.desktops.unwrap_or_default());
            if let Some(kept) = facts.tile_contents.held_by(&facts.desktops) {
                facts.tile_contents = Arc::new(kept);
            }
            facts.settings = Arc::new(state.settings.unwrap_or_default());
            facts.outposts = Arc::new(state.outposts.unwrap_or_default());
            facts.home_name = state.home_name.unwrap_or_default();
            facts.home_build = state.home_build.unwrap_or_default();
            facts.crew = Arc::new(state.crew.unwrap_or_default());
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
                beds: state.garden_beds.unwrap_or_default(),
                closed_counts: state.garden_closed_counts,
                revision: facts.garden.revision + 1,
                full_push: facts.garden.full_push + 1,
            });
            facts.asks = Arc::new(state.asks.unwrap_or_default());
            facts.warnings = Arc::new(state.warnings.unwrap_or_default());
            facts.vents = Arc::new(state.vents.unwrap_or_default());
            facts.update_run = Arc::new(state.update_run);
            facts.pull_requests = Arc::new(PullRequests {
                prs: state.prs.unwrap_or_default(),
                repos: state.repos.unwrap_or_default(),
                authors: state.authors.unwrap_or_default(),
                rate_limited_until: facts.pull_requests.rate_limited_until.clone(),
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

    pub(crate) fn set_plugins(&self, plugins: Plugins) {
        self.change(|facts| {
            facts.plugins = Arc::new(plugins);
            Slices::PLUGINS
        });
    }

    pub(crate) fn set_shared_codex(&self, profile_id: String, state: Option<SharedCodexState>) {
        self.change(|facts| {
            facts.shared_codex = Arc::new(Some(SharedCodex { profile_id, state }));
            Slices::SHARED_CODEX
        });
    }

    pub(crate) fn set_harnesses(&self, harnesses: Vec<Harness>) {
        self.change(|facts| {
            facts.harnesses = Arc::new(harnesses);
            facts.harnesses_loaded = true;
            Slices::HARNESSES
        });
    }

    pub(crate) fn set_machine(&self, id: String, home: Option<String>) {
        self.change(|facts| {
            if facts.machine.id == id && facts.machine.home == home {
                return Slices::NONE;
            }
            facts.machine = match home {
                Some(_) if facts.machine.id == id => AppMachine {
                    home,
                    ..facts.machine.clone()
                },
                _ => AppMachine {
                    id,
                    home,
                    ..AppMachine::default()
                },
            };
            Slices::CONNECTION | Slices::OUTPOSTS | Slices::MACHINE
        });
    }

    pub(crate) fn set_outposts(&self, outposts: Vec<OutpostInfo>, home_name: Option<String>) {
        self.change(|facts| {
            facts.outposts = Arc::new(outposts);
            facts.home_name = home_name.unwrap_or_default();
            Slices::OUTPOSTS
        });
    }

    /// The selected profile's whole garden window, as the daemon sends it on connect and profile
    /// switch. A window for another profile arrived late and is dropped.
    pub(crate) fn set_garden(
        &self,
        profile: String,
        seeds: Vec<Seed>,
        total: i64,
        beds: Vec<GardenBed>,
        closed_counts: Option<GardenClosedCounts>,
    ) {
        self.change(|facts| {
            if facts.profiles.selected != profile {
                return Slices::NONE;
            }
            facts.garden = Arc::new(Garden {
                profile,
                seeds: seeds.into_iter().map(Arc::new).collect(),
                total,
                beds,
                closed_counts,
                revision: facts.garden.revision + 1,
                full_push: facts.garden.full_push + 1,
            });
            Slices::GARDEN
        });
    }

    /// The selected profile's curated beds, as the daemon pushes them on bed changes.
    pub(crate) fn set_garden_beds(&self, profile: String, beds: Vec<GardenBed>) {
        self.change(|facts| {
            if facts.profiles.selected != profile {
                return Slices::NONE;
            }
            let (seeds, total, closed_counts) = if facts.garden.profile == profile {
                (
                    facts.garden.seeds.clone(),
                    facts.garden.total,
                    facts.garden.closed_counts.clone(),
                )
            } else {
                (Vec::new(), 0, None)
            };
            facts.garden = Arc::new(Garden {
                profile,
                seeds,
                total,
                beds,
                closed_counts,
                revision: facts.garden.revision + 1,
                full_push: facts.garden.full_push,
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
        closed_counts: Option<GardenClosedCounts>,
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
                beds: facts.garden.beds.clone(),
                closed_counts: closed_counts.or_else(|| facts.garden.closed_counts.clone()),
                revision: facts.garden.revision + 1,
                full_push: facts.garden.full_push,
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

    pub(crate) fn set_phones(&self, phones: PhonesUpdatedMessage) {
        self.change(|facts| {
            facts.phones = Arc::new(Some(phones));
            Slices::PHONES
        });
    }

    pub(crate) fn set_senders(&self, message: SendersUpdatedMessage) {
        self.change(|facts| {
            facts.senders = Arc::new(Some(message.senders));
            facts.sender_pairing = Arc::new(message.request);
            Slices::SENDERS
        });
    }

    pub(crate) fn set_crew(&self, crew: Vec<CrewMember>) {
        self.change(|facts| {
            facts.crew = Arc::new(crew);
            Slices::CREW
        });
    }

    pub(crate) fn set_capture_read(&self, capture: String, read_at: Option<String>) {
        self.change(|facts| {
            Arc::make_mut(&mut facts.capture_reads).insert(capture, read_at);
            Slices::CAPTURE_READS
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

    pub(crate) fn delegation_changed(&self) {
        self.change(|facts| {
            facts.delegation_changed += 1;
            Slices::DELEGATION
        });
    }

    pub(crate) fn automations_changed(&self) {
        self.change(|facts| {
            facts.automations_changed += 1;
            Slices::AUTOMATIONS
        });
    }

    pub(crate) fn kept_conversations_changed(&self) {
        self.change(|facts| {
            facts.kept_conversations += 1;
            Slices::KEPT_CONVERSATIONS
        });
    }

    pub(crate) fn files_changed(&self, root: String) {
        self.change(|facts| {
            *Arc::make_mut(&mut facts.file_changes)
                .entry(root)
                .or_default() += 1;
            Slices::FILES
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
    pub(crate) fn apply_arrangement(
        &self,
        profile: Profile,
        desktops: Vec<attn_protocol::Desktop>,
    ) {
        let desktops = arrived(desktops);
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
            facts.desktops = desktops;
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
    desktops
        .iter()
        .find(|d| d.id == key.desktop)
        .and_then(Desktop::tree)
        .is_some_and(|tree| tree.has_leaf(&key.tile))
}
