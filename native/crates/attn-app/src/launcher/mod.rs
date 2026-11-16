//! Launcher 2.0, the new-session launcher the `new_session_launcher_enabled` setting turns on:
//! one screen that searches recent projects, browses folders once the text starts with `~` or
//! `/`, and offers a repository's destinations (the Where list) before it starts an agent or a
//! terminal. With outposts attached, every recent, folder and start belongs to a machine, and
//! `<machine>:` in front of the text aims it at one.

mod address;
mod view;

use std::collections::{BTreeMap, BTreeSet};

use attn_protocol::{
    AgentCatalogMessage, AgentCatalogResultMessage, BrowseDirectoryMessage,
    BrowseDirectoryResultMessage, CreateWorktreeMessage, CreateWorktreeResultMessage,
    DeleteWorktreeMessage, DeleteWorktreeResultMessage, DirectoryEntry, GetRecentLocationsMessage,
    GetRepoInfoMessage, GetRepoInfoResultMessage, InspectPathMessage, InspectPathResultMessage,
    OutpostInfo, PathInspection, RecentLocation, RecentLocationsResultMessage, RepoInfo,
};
use rune::{Chord, Key, KeyEvent, KeyPlatform, TextEdit, Tx};

use crate::State;
use crate::picker::{
    Agents, Choice, Direction, Memory, Offer, Spawn, TERMINAL, Which, agent_key, agents, close,
    paths, remember, remembered, spawn_then, worktree_name,
};
pub(crate) use address::{Address, Machine, address_of, machine_for, parse, this_mac};
pub(crate) use view::frame;

const OUTPOSTS_ENABLED: &str = "outposts_enabled";
/// How often the launcher redraws while it waits on an outpost's listing, so the time it has
/// waited counts up as in the Tauri app.
const ASKING_TICK: f64 = 0.1;
/// How often the launcher redraws while a start on an outpost is under way, so a stall's
/// seconds count up.
const STARTING_TICK: f64 = 1.0;

pub(crate) struct Launcher {
    pub(crate) which: Which,
    pub(crate) opening: u64,
    /// The desktop a start lands on: the inline launcher's own, or the one the dialog opened on.
    pub(crate) desktop: Option<String>,
    anchor: Option<String>,
    direction: Direction,
    pub(crate) choice: Choice,
    pub(crate) query: TextEdit,
    /// Whether this opening's recents have arrived; until then no row is highlighted.
    fresh: bool,
    /// The row the user moved to. While searching, none means the top row.
    highlight: Option<usize>,
    /// Folders the daemon listed, by machine and normalized path.
    listings: BTreeMap<String, Listing>,
    /// Paths an aimed search asked its machine about, by machine and path.
    inspections: BTreeMap<String, Inspection>,
    /// How many listings and inspections have arrived, which orders rows by arrival.
    arrivals: u64,
    /// Each outpost's home folder, as it last reported it.
    homes: BTreeMap<String, String>,
    /// The harnesses each reachable outpost has installed.
    catalogs: BTreeMap<String, BTreeSet<String>>,
    /// The repository whose destinations show, once Tab opened them.
    pub(crate) place: Option<Where>,
    pub(crate) problem: Option<Problem>,
    /// A start is on its way; the launcher takes no input until it answers.
    pub(crate) busy: bool,
    pub(crate) starting: Option<Starting>,
    pub(crate) deletion: Option<Deletion>,
    /// Bumped by every change that makes answers to earlier requests stale.
    generation: u64,
    /// The worktree name offered for each repository, by machine, kept for this opening.
    names: BTreeMap<String, String>,
    /// Worktrees this opening made, by machine, repository, branch and starting point, so a
    /// start that failed after its worktree was made starts there again instead of making
    /// another.
    created: BTreeMap<String, String>,
    /// Wall-clock milliseconds when the launcher last counted how long it waits.
    pub(crate) now_ms: i64,
}

/// A repository's destinations: a new worktree from its default or current branch, its main
/// checkout, and its other worktrees.
pub(crate) struct Where {
    pub(crate) repo: String,
    pub(crate) host: Option<String>,
    pub(crate) info: Option<RepoInfo>,
    /// The name typed for a new worktree, which also filters the existing ones.
    pub(crate) branch: TextEdit,
    pub(crate) highlight: Option<usize>,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum WhereRow {
    New {
        current: bool,
        branch: String,
    },
    Main {
        branch: String,
    },
    Existing {
        path: String,
        branch: String,
        created_at: Option<String>,
    },
}

pub(crate) struct Deletion {
    pub(crate) path: String,
    pub(crate) force: bool,
}

/// Why the launcher could not do what was asked, and what made it so.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Problem {
    pub(crate) message: String,
    pub(crate) reason: Option<String>,
}

impl From<String> for Problem {
    fn from(message: String) -> Problem {
        Problem {
            message,
            reason: None,
        }
    }
}

/// A start under way, on which machine and since when.
pub(crate) struct Starting {
    pub(crate) host: Option<String>,
    pub(crate) at_ms: i64,
}

pub(crate) enum Listing {
    Pending {
        started_ms: i64,
    },
    Listed {
        directory: String,
        entries: Vec<DirectoryEntry>,
        at_ms: i64,
        order: u64,
    },
    /// The machine has no such folder.
    Missing,
    Failed(String),
}

pub(crate) enum Inspection {
    Pending,
    Inspected {
        inspection: PathInspection,
        order: u64,
    },
    Failed,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    Recent,
    Folder,
    /// An outpost whose name the text starts, to browse.
    Machine,
    /// The browsed folder on another machine, where the target lacks it.
    Elsewhere,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Row {
    pub(crate) kind: Kind,
    pub(crate) path: String,
    pub(crate) name: String,
    pub(crate) host: Option<String>,
    pub(crate) repo_root: Option<String>,
    pub(crate) last_seen: Option<String>,
}

/// A browsed path's folder and the start of the name typed in it.
pub(crate) fn split_browse(path: &str) -> (String, String) {
    match path.rfind('/') {
        None => ("~".into(), path.get(1..).unwrap_or_default().into()),
        Some(0) => ("/".into(), path[1..].into()),
        Some(slash) => (path[..slash].into(), path[slash + 1..].into()),
    }
}

/// The folders whose name holds `fragment`, those starting with it first.
fn matching_folders<'a>(entries: &'a [DirectoryEntry], fragment: &str) -> Vec<&'a DirectoryEntry> {
    let needle = fragment.to_lowercase();
    let (starts, rest): (Vec<_>, Vec<_>) = entries
        .iter()
        .filter(|e| e.is_dir && e.name.to_lowercase().contains(&needle))
        .partition(|e| e.name.to_lowercase().starts_with(&needle));
    starts.into_iter().chain(rest).collect()
}

/// The outposts a launcher offers: those attached, while the Outposts switch is on.
pub(crate) fn outposts(state: &State) -> &[OutpostInfo] {
    if crate::setting::on(&state.facts.settings, OUTPOSTS_ENABLED) {
        &state.facts.outposts
    } else {
        &[]
    }
}

pub(crate) fn outpost<'a>(outposts: &'a [OutpostInfo], host: &str) -> Option<&'a OutpostInfo> {
    outposts.iter().find(|o| o.id == host)
}

pub(crate) fn machine_name(outposts: &[OutpostInfo], host: Option<&str>) -> String {
    match host {
        None => this_mac().name,
        Some(host) => outpost(outposts, host).map_or_else(|| host.to_owned(), |o| o.name.clone()),
    }
}

pub(crate) fn machine_of(outposts: &[OutpostInfo], host: Option<&str>) -> Machine {
    match host {
        None => this_mac(),
        Some(host) => outpost(outposts, host).map_or_else(
            || Machine {
                host: Some(host.to_owned()),
                name: host.to_owned(),
                label: host.to_owned(),
            },
            |o| machine_for(o, outposts),
        ),
    }
}

pub(crate) fn reachable(outposts: &[OutpostInfo], host: Option<&str>) -> bool {
    host.is_none_or(|host| outpost(outposts, host).is_some_and(|o| o.reach == "reachable"))
}

/// Why an outpost cannot take a start now.
fn unreachable_reason(outpost: Option<&OutpostInfo>, now_ms: i64) -> String {
    let Some(outpost) = outpost else {
        return "It is no longer attached to this Mac.".into();
    };
    match outpost.state.as_str() {
        "refused" => outpost
            .last_error
            .clone()
            .filter(|e| !e.is_empty())
            .unwrap_or_else(|| "It refused the connection.".into()),
        "lost" => match view::age(&outpost.state_since, now_ms).as_str() {
            "now" | "" => "Lost just now.".into(),
            age => format!("Lost for {age}."),
        },
        _ => "Still connecting.".into(),
    }
}

fn key_on(host: Option<&str>, path: &str) -> String {
    format!("{}\n{path}", host.unwrap_or_default())
}

fn host_of(r: &RecentLocation) -> Option<String> {
    r.host.clone().filter(|h| !h.is_empty())
}

impl Launcher {
    pub(crate) fn open(
        which: Which,
        desktop: Option<String>,
        anchor: Option<String>,
        direction: Direction,
        agent: Option<&str>,
        state: &State,
    ) -> Launcher {
        let desktop = desktop.or_else(|| state.facts.current_desktop().map(|d| d.id.clone()));
        Launcher {
            which,
            opening: state.window.launching.openings() + 1,
            desktop,
            anchor,
            direction,
            choice: Choice::new(agent, &state.facts.settings),
            query: TextEdit::default(),
            fresh: false,
            highlight: None,
            listings: BTreeMap::new(),
            inspections: BTreeMap::new(),
            arrivals: 0,
            homes: BTreeMap::new(),
            catalogs: BTreeMap::new(),
            place: None,
            problem: None,
            busy: false,
            starting: None,
            deletion: None,
            generation: 0,
            names: BTreeMap::new(),
            created: BTreeMap::new(),
            now_ms: 0,
        }
    }

    pub(crate) fn field_key(&self) -> String {
        format!("{}.path.field", self.which.key())
    }

    pub(crate) fn address(&self, state: &State) -> Address {
        parse(&self.query.text, outposts(state))
    }

    /// The home folder of `host`, or of this machine: empty until it said.
    pub(crate) fn home(&self, state: &State, host: Option<&str>) -> String {
        match host {
            None => state.window.launching.home.clone(),
            Some(host) => self.homes.get(host).cloned().unwrap_or_default(),
        }
    }

    pub(crate) fn display(&self, state: &State, path: &str, host: Option<&str>) -> String {
        paths::display(path, &self.home(state, host))
    }

    /// The keys `dir` on `host` is listed under: with the machine's home, and as asked before
    /// the machine said its home.
    fn listing_keys(&self, state: &State, host: Option<&str>, dir: &str) -> [String; 2] {
        let home = self.home(state, host);
        [
            key_on(host, &paths::normalize(dir, &home)),
            key_on(host, &paths::normalize(dir, "")),
        ]
    }

    pub(crate) fn listing(&self, state: &State, host: Option<&str>, dir: &str) -> Option<&Listing> {
        let [known, asked] = self.listing_keys(state, host, dir);
        self.listings
            .get(&known)
            .or_else(|| self.listings.get(&asked))
    }

    /// Whether `host` listed `path` as a repository, when it listed its folder at all.
    pub(crate) fn known_repo(&self, state: &State, host: Option<&str>, path: &str) -> Option<bool> {
        let target = paths::normalize(path, &self.home(state, host));
        let parent = match target.rfind('/') {
            Some(0) | None => "/",
            Some(slash) => &target[..slash],
        };
        match self.listing(state, host, parent)? {
            Listing::Listed { entries, .. } => entries
                .iter()
                .find(|e| e.path == target)
                .map(|e| e.is_repo == Some(true)),
            _ => None,
        }
    }

    /// A recent's path written relative to its own machine's home, as another machine is
    /// asked about it.
    fn asked_path(&self, state: &State, item: &RecentLocation) -> String {
        let home = self.home(state, item.host.as_deref());
        if home.is_empty() {
            item.path.clone()
        } else {
            paths::display(&item.path, &home)
        }
    }

    /// The harnesses on offer: this machine's, and those any outpost has installed.
    pub(crate) fn agents(&self, state: &State) -> Agents {
        Agents::from_settings(&state.facts.settings)
            .with_installed(self.catalogs.values().flatten().cloned())
    }

    pub(crate) fn agent(&self, state: &State) -> String {
        self.choice.agent_among(&self.agents(state))
    }

    /// What `host` has installed, once any outpost said: for this machine, what it has.
    fn installed_on(&self, state: &State, host: Option<&str>) -> Option<BTreeSet<String>> {
        match host {
            Some(host) => self.catalogs.get(host).cloned(),
            None => (!self.catalogs.is_empty())
                .then(|| Agents::from_settings(&state.facts.settings).installed()),
        }
    }

    /// The machine a start would go to now.
    pub(crate) fn target(&self, state: &State) -> Option<String> {
        if let Some(place) = &self.place {
            return place.host.clone();
        }
        let rows = self.rows(state);
        if let Some(row) = self.selected(state, &rows).and_then(|i| rows.get(i)) {
            return row.host.clone();
        }
        match self.address(state) {
            Address::Browse { machine, .. } => machine.host,
            Address::Search { machine, .. } => machine.and_then(|m| m.host),
            Address::Ambiguous { .. } => None,
        }
    }

    /// The harness controls for the machine a start would go to.
    pub(crate) fn offer(&self, state: &State) -> Offer {
        let agents = self.agents(state);
        let target = self.target(state);
        let missing = self
            .installed_on(state, target.as_deref())
            .map(|installed| {
                let lacking = agents
                    .ids()
                    .filter(|id| *id != TERMINAL && !installed.contains(*id))
                    .cloned()
                    .collect();
                (machine_name(outposts(state), target.as_deref()), lacking)
            });
        Offer {
            agents,
            missing,
            outpost: target.is_some(),
        }
    }

    /// Why a start of `agent` on `host` cannot go now, if it cannot.
    fn refusal(&self, state: &State, host: Option<&str>, agent: &str) -> Option<Problem> {
        let outposts = outposts(state);
        let name = machine_name(outposts, host);
        if let Some(host) = host
            && !reachable(outposts, Some(host))
        {
            return Some(Problem {
                message: format!("{name} is unreachable; this needs it now"),
                reason: Some(unreachable_reason(
                    outpost(outposts, host),
                    state.window.now_ms,
                )),
            });
        }
        let installed = self.installed_on(state, host)?;
        (agent != TERMINAL && !installed.contains(agent)).then(|| {
            let keys = if KeyPlatform::current() == KeyPlatform::Mac {
                "⌥1–⌥9"
            } else {
                "Alt+1–Alt+9"
            };
            Problem::from(format!(
                "{} is not installed on {name}. Pick another harness ({keys}) or machine.",
                agents::label(agent)
            ))
        })
    }

    /// The rows the text asks for: recents and machines while searching, recents aimed at a
    /// machine and those it confirmed it has, or the recents and folders of a browsed path
    /// with the machines that have it when the target does not.
    pub(crate) fn rows(&self, state: &State) -> Vec<Row> {
        let outposts = outposts(state);
        let mut recents: Vec<&RecentLocation> = state.window.launching.recents.iter().collect();
        recents.sort_by(|a, b| b.last_seen.cmp(&a.last_seen));
        let recent_row = |r: &RecentLocation| Row {
            kind: Kind::Recent,
            path: r.path.clone(),
            name: paths::basename(&r.path).to_owned(),
            host: host_of(r),
            repo_root: r.repo_root.clone().filter(|root| !root.is_empty()),
            last_seen: Some(r.last_seen.clone()),
        };
        match self.address(state) {
            Address::Browse { path, machine, .. } => {
                let host = machine.host.as_deref();
                let needle = paths::expand(&path, &self.home(state, host)).to_lowercase();
                let bare = needle.trim_end_matches('/').to_owned();
                let (dir, fragment) = split_browse(&path);
                let mut rows: Vec<Row> = recents
                    .iter()
                    .filter(|r| {
                        let lower = r.path.to_lowercase();
                        host_of(r).as_deref() == host
                            && lower.starts_with(&needle)
                            && lower.trim_end_matches('/') != bare
                    })
                    .map(|r| recent_row(r))
                    .collect();
                let listed = self.listing(state, host, &dir);
                if let Some(Listing::Listed { entries, .. }) = listed {
                    rows.extend(
                        matching_folders(entries, &fragment)
                            .into_iter()
                            .map(|e| Row {
                                kind: Kind::Folder,
                                path: e.path.clone(),
                                name: e.name.clone(),
                                host: host.map(str::to_owned),
                                repo_root: (e.is_repo == Some(true)).then(|| e.path.clone()),
                                last_seen: None,
                            }),
                    );
                }
                if !outposts.is_empty() && matches!(listed, Some(Listing::Missing)) {
                    let mut elsewhere: Vec<(u64, Option<String>)> = std::iter::once(None)
                        .chain(outposts.iter().map(|o| Some(o.id.clone())))
                        .filter(|h| h.as_deref() != host && reachable(outposts, h.as_deref()))
                        .filter_map(|h| match self.listing(state, h.as_deref(), &dir) {
                            Some(Listing::Listed { order, .. }) => Some((*order, h)),
                            _ => None,
                        })
                        .collect();
                    elsewhere.sort_by_key(|(order, _)| *order);
                    rows.extend(elsewhere.into_iter().map(|(_, h)| Row {
                        kind: Kind::Elsewhere,
                        path: dir.clone(),
                        name: format!("{dir}/ is on {}", machine_name(outposts, h.as_deref())),
                        host: h,
                        repo_root: None,
                        last_seen: None,
                    }));
                }
                rows
            }
            Address::Search {
                text,
                machine: Some(machine),
                ..
            } => {
                let host = machine.host;
                let needle = text.to_lowercase();
                let mut rows: Vec<Row> = recents
                    .iter()
                    .filter(|r| host_of(r) == host && r.path.to_lowercase().contains(&needle))
                    .map(|r| recent_row(r))
                    .collect();
                let mut seen: BTreeSet<String> = rows.iter().map(|r| r.path.clone()).collect();
                let mut confirmed: Vec<(u64, &RecentLocation, &PathInspection)> = recents
                    .iter()
                    .filter(|r| host_of(r) != host && r.path.to_lowercase().contains(&needle))
                    .filter_map(|r| {
                        let asked = key_on(host.as_deref(), &self.asked_path(state, r));
                        match self.inspections.get(&asked)? {
                            Inspection::Inspected { inspection, order }
                                if inspection.exists && inspection.is_directory =>
                            {
                                Some((*order, *r, inspection))
                            }
                            _ => None,
                        }
                    })
                    .collect();
                confirmed.sort_by_key(|(order, ..)| *order);
                for (_, item, inspection) in confirmed {
                    let path = inspection.resolved_path.clone();
                    if !seen.insert(path.clone()) {
                        continue;
                    }
                    rows.push(Row {
                        kind: Kind::Recent,
                        name: paths::basename(&path).to_owned(),
                        host: host.clone(),
                        repo_root: inspection.repo_root.clone().filter(|root| *root == path),
                        last_seen: Some(item.last_seen.clone()),
                        path,
                    });
                }
                rows
            }
            address => {
                let searched = match &address {
                    Address::Search { text, .. } => text.clone(),
                    _ => String::new(),
                };
                let needle = paths::expand(&searched, &self.home(state, None)).to_lowercase();
                let named: Vec<&OutpostInfo> = match &address {
                    Address::Ambiguous { machines, .. } => machines
                        .iter()
                        .filter_map(|m| outpost(outposts, m.host.as_deref()?))
                        .collect(),
                    _ => address::machines_named(&searched, outposts),
                };
                let mut rows: Vec<Row> = named
                    .into_iter()
                    .map(|o| Row {
                        kind: Kind::Machine,
                        path: "~/".into(),
                        name: format!("{}:", o.name),
                        host: Some(o.id.clone()),
                        repo_root: None,
                        last_seen: None,
                    })
                    .collect();
                if !matches!(address, Address::Ambiguous { .. }) {
                    rows.extend(
                        recents
                            .iter()
                            .filter(|r| {
                                let shown = match host_of(r) {
                                    Some(h) => {
                                        format!("{}:{}", machine_name(outposts, Some(&h)), r.path)
                                    }
                                    None => r.path.clone(),
                                };
                                shown.to_lowercase().contains(&needle)
                            })
                            .map(|r| recent_row(r)),
                    );
                }
                rows
            }
        }
    }

    /// The highlighted row: while browsing, only one the user moved to; otherwise the one
    /// the user moved to or else the top one, once this opening's recents arrived.
    pub(crate) fn selected(&self, state: &State, rows: &[Row]) -> Option<usize> {
        let moved = self.highlight.filter(|i| *i < rows.len());
        match self.address(state) {
            Address::Browse { .. } => moved,
            _ if !self.fresh || rows.is_empty() => None,
            _ => Some(moved.unwrap_or(0)),
        }
    }

    /// The name a new worktree gets: the typed one, else the one offered.
    pub(crate) fn worktree_name(&self) -> String {
        let Some(place) = &self.place else {
            return String::new();
        };
        match place.branch.text.trim() {
            "" => self
                .names
                .get(&key_on(place.host.as_deref(), &place.repo))
                .cloned()
                .unwrap_or_default(),
            typed => typed.to_owned(),
        }
    }

    /// The outpost a start is waiting on, while it has stopped answering.
    pub(crate) fn stalled<'a>(&self, state: &'a State) -> Option<&'a OutpostInfo> {
        let host = self.starting.as_ref()?.host.as_deref()?;
        outpost(outposts(state), host).filter(|o| self.busy && o.state == "stalled")
    }

    /// Whether the launcher waits on a reachable outpost for the browsed folder.
    pub(crate) fn asking(&self, state: &State) -> bool {
        let Address::Browse { path, machine, .. } = self.address(state) else {
            return false;
        };
        self.place.is_none()
            && machine.host.is_some()
            && reachable(outposts(state), machine.host.as_deref())
            && matches!(
                self.listing(state, machine.host.as_deref(), &split_browse(&path).0),
                Some(Listing::Pending { .. })
            )
    }
}

impl Where {
    pub(crate) fn rows(&self) -> Vec<WhereRow> {
        let Some(info) = &self.info else {
            return Vec::new();
        };
        let typed = self.branch.text.trim().to_lowercase();
        let mut rows = vec![WhereRow::New {
            current: false,
            branch: info.default_branch.clone(),
        }];
        if info.current_branch != info.default_branch {
            rows.push(WhereRow::New {
                current: true,
                branch: info.current_branch.clone(),
            });
        }
        rows.push(WhereRow::Main {
            branch: info.current_branch.clone(),
        });
        rows.extend(
            info.worktrees
                .iter()
                .filter(|w| w.path != self.repo && w.branch.to_lowercase().contains(&typed))
                .map(|w| WhereRow::Existing {
                    path: w.path.clone(),
                    branch: w.branch.clone(),
                    created_at: w.created_at.clone(),
                }),
        );
        rows
    }

    /// The row a fresh look at the list lands on: the worktree named exactly as typed, else a
    /// new one; with nothing typed, where the repository's last start went.
    fn aim(&mut self, memory: Memory) {
        if self.info.is_none() {
            return;
        }
        let rows = self.rows();
        let typed = self.branch.text.trim();
        self.highlight = Some(if !typed.is_empty() {
            rows.iter()
                .position(|r| matches!(r, WhereRow::Existing { branch, .. } if branch == typed))
                .unwrap_or(0)
        } else if memory == Memory::NewWorktree {
            0
        } else {
            rows.iter()
                .position(|r| matches!(r, WhereRow::Main { .. }))
                .unwrap_or(0)
        });
    }
}

/// Where the repository's last start on `host` went: its main checkout unless it said.
pub(crate) fn memory(state: &State, repo: &str, host: Option<&str>) -> Memory {
    remembered(state, repo, host).unwrap_or(Memory::MainRepo)
}

fn this(state: &mut State, which: Which, opening: u64) -> Option<&mut Launcher> {
    state
        .window
        .launching
        .launcher_mut(which)
        .filter(|l| l.opening == opening)
}

fn this_ref(state: &State, which: Which, opening: u64) -> Option<&Launcher> {
    state
        .window
        .launching
        .launcher(which)
        .filter(|l| l.opening == opening)
}

/// The launcher the answer to request `generation` is for, while nothing made it stale.
fn current(
    state: &mut State,
    which: Which,
    opening: u64,
    generation: u64,
) -> Option<&mut Launcher> {
    this(state, which, opening).filter(|l| l.generation == generation)
}

fn learn_home(
    state: &mut State,
    which: Which,
    opening: u64,
    host: Option<&str>,
    home: Option<String>,
) {
    let Some(home) = home.filter(|h| !h.is_empty()) else {
        return;
    };
    match host {
        None => state.window.launching.home = home,
        Some(host) => {
            if let Some(launcher) = this(state, which, opening) {
                launcher.homes.insert(host.to_owned(), home);
            }
        }
    }
}

/// Asks for the recents, on every machine while outposts are attached, and for each reachable
/// outpost's harnesses, home and the folders its recents sit in.
pub(crate) fn opened(state: &mut State, which: Which, tx: &mut Tx) {
    let attached = outposts(state).to_vec();
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    let opening = launcher.opening;
    for o in &attached {
        if let Some(home) = o.home.clone().filter(|h| !h.is_empty()) {
            launcher.homes.insert(o.id.clone(), home);
        }
    }
    let reachable_hosts: Vec<String> = attached
        .iter()
        .filter(|o| o.reach == "reachable")
        .map(|o| o.id.clone())
        .collect();
    for host in reachable_hosts.clone() {
        let asked = host.clone();
        state.ask(
            |id| AgentCatalogMessage {
                host: asked,
                request_id: Some(id),
                ..AgentCatalogMessage::new()
            },
            move |state, reply, _| {
                let result: Option<AgentCatalogResultMessage> = reply.parse();
                let Some(catalog) = result.filter(|r| r.success).and_then(|r| r.catalog) else {
                    return;
                };
                if let Some(launcher) = this(state, which, opening) {
                    let installed = catalog
                        .harnesses
                        .into_iter()
                        .filter(|h| h.available)
                        .map(|h| h.id)
                        .collect();
                    launcher.catalogs.insert(host, installed);
                }
            },
            tx,
        );
    }
    state.ask(
        |id| GetRecentLocationsMessage {
            home_only: attached.is_empty().then_some(true),
            request_id: Some(id),
            ..GetRecentLocationsMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<RecentLocationsResultMessage> = reply.parse();
            let Some(result) = result.filter(|r| r.success) else {
                if let Some(launcher) = this(state, which, opening) {
                    launcher.problem = Some("Failed to load recent locations".to_owned().into());
                }
                return;
            };
            let launching = &mut state.window.launching;
            if let Some(home) = result.home_path.filter(|h| !h.is_empty()) {
                launching.home = home;
            }
            launching.recents = result.recent_locations;
            let Some(launcher) = this(state, which, opening) else {
                return;
            };
            launcher.fresh = true;
            let recents = state.window.launching.recents.clone();
            for host in &reachable_hosts {
                request_listing(state, which, opening, Some(host), "~", tx);
                for item in recents.iter().filter(|r| r.host.as_deref() == Some(host)) {
                    let trimmed = item.path.trim_end_matches('/');
                    let parent = match trimmed.rfind('/') {
                        Some(0) | None => "/",
                        Some(slash) => &trimmed[..slash],
                    };
                    request_listing(state, which, opening, Some(host), parent, tx);
                }
            }
            fetch_wanted(state, which, tx);
        },
        tx,
    );
}

/// Asks `host` to list `dir`, once per opening. A folder it cannot list is inspected, to tell
/// one it does not have from one it could not read.
fn request_listing(
    state: &mut State,
    which: Which,
    opening: u64,
    host: Option<&str>,
    dir: &str,
    tx: &mut Tx,
) {
    let now_ms = state.wall_ms(tx);
    let Some(launcher) = this_ref(state, which, opening) else {
        return;
    };
    if launcher.listing(state, host, dir).is_some() {
        return;
    }
    let [asked_key, _] = launcher.listing_keys(state, host, dir);
    let launcher = this(state, which, opening).expect("the launcher is open");
    launcher
        .listings
        .insert(asked_key.clone(), Listing::Pending { started_ms: now_ms });
    if host.is_some() {
        launcher.now_ms = now_ms;
        tx.wake_at(tx.clock() + ASKING_TICK);
    }
    let input_path = if dir == "/" {
        "/".to_owned()
    } else {
        format!("{}/", dir.trim_end_matches('/'))
    };
    let (host, dir) = (host.map(str::to_owned), dir.to_owned());
    let asked_host = host.clone();
    state.ask(
        |id| BrowseDirectoryMessage {
            input_path,
            host: asked_host,
            request_id: Some(id),
            ..BrowseDirectoryMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<BrowseDirectoryResultMessage> = reply.parse();
            learn_home(
                state,
                which,
                opening,
                host.as_deref(),
                result.as_ref().and_then(|r| r.home_path.clone()),
            );
            match result {
                Some(result) if result.success => {
                    let at_ms = state.wall_ms(tx);
                    let Some(launcher) = this_ref(state, which, opening) else {
                        return;
                    };
                    let [known_key, _] = launcher.listing_keys(state, host.as_deref(), &dir);
                    let [directory_key, _] =
                        launcher.listing_keys(state, host.as_deref(), &result.directory);
                    let launcher = this(state, which, opening).expect("the launcher is open");
                    launcher.arrivals += 1;
                    let order = launcher.arrivals;
                    let listed = || Listing::Listed {
                        directory: result.directory.clone(),
                        entries: result.entries.clone(),
                        at_ms,
                        order,
                    };
                    for key in [asked_key, known_key, directory_key] {
                        launcher.listings.insert(key, listed());
                    }
                    fetch_wanted(state, which, tx);
                }
                other => {
                    let error = other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Listing failed".into());
                    tell_missing(state, which, opening, host, dir, asked_key, error, tx);
                }
            }
        },
        tx,
    );
}

/// Settles a listing that failed: missing when the machine says the folder is not there.
#[allow(clippy::too_many_arguments)]
fn tell_missing(
    state: &mut State,
    which: Which,
    opening: u64,
    host: Option<String>,
    dir: String,
    key: String,
    error: String,
    tx: &mut Tx,
) {
    state.ask(
        |id| InspectPathMessage {
            path: dir,
            host,
            request_id: Some(id),
            ..InspectPathMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<InspectPathResultMessage> = reply.parse();
            let missing = result.and_then(|r| r.inspection).is_some_and(|i| !i.exists);
            let Some(launcher) = this(state, which, opening) else {
                return;
            };
            launcher.listings.insert(
                key,
                if missing {
                    Listing::Missing
                } else {
                    Listing::Failed(error)
                },
            );
            fetch_wanted(state, which, tx);
        },
        tx,
    );
}

/// Asks `host` whether it has `path`, once per opening.
fn request_inspection(
    state: &mut State,
    which: Which,
    opening: u64,
    host: Option<&str>,
    path: &str,
    tx: &mut Tx,
) {
    let key = key_on(host, path);
    let Some(launcher) = this(state, which, opening) else {
        return;
    };
    if launcher.inspections.contains_key(&key) {
        return;
    }
    launcher
        .inspections
        .insert(key.clone(), Inspection::Pending);
    let (host, path) = (host.map(str::to_owned), path.to_owned());
    let asked_host = host.clone();
    state.ask(
        |id| InspectPathMessage {
            path,
            host: asked_host,
            request_id: Some(id),
            ..InspectPathMessage::new()
        },
        move |state, reply, _| {
            let result: Option<InspectPathResultMessage> = reply.parse();
            let inspection = result.filter(|r| r.success).and_then(|r| r.inspection);
            learn_home(
                state,
                which,
                opening,
                host.as_deref(),
                inspection.as_ref().and_then(|i| i.home_path.clone()),
            );
            let Some(launcher) = this(state, which, opening) else {
                return;
            };
            let settled = match inspection {
                Some(inspection) => {
                    launcher.arrivals += 1;
                    Inspection::Inspected {
                        inspection,
                        order: launcher.arrivals,
                    }
                }
                None => Inspection::Failed,
            };
            launcher.inspections.insert(key, settled);
        },
        tx,
    );
}

/// Asks for what the text needs that no request asked yet: the browsed folder on its
/// machine, the same folder on the other machines when the target lacks it, and whether the
/// machine an aimed search targets has the other machines' matching recents.
fn fetch_wanted(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    if launcher.place.is_some() {
        return;
    }
    let opening = launcher.opening;
    let outposts = outposts(state).to_vec();
    let mut listings: Vec<(Option<String>, String)> = Vec::new();
    let mut inspections: Vec<(Option<String>, String)> = Vec::new();
    match launcher.address(state) {
        Address::Browse { path, machine, .. } => {
            let host = machine.host;
            let (dir, _) = split_browse(&path);
            if reachable(&outposts, host.as_deref()) {
                listings.push((host.clone(), dir.clone()));
            }
            if !outposts.is_empty()
                && matches!(
                    launcher.listing(state, host.as_deref(), &dir),
                    Some(Listing::Missing)
                )
            {
                listings.extend(
                    std::iter::once(None)
                        .chain(outposts.iter().map(|o| Some(o.id.clone())))
                        .filter(|h| *h != host && reachable(&outposts, h.as_deref()))
                        .map(|h| (h, dir.clone())),
                );
            }
        }
        Address::Search {
            text,
            machine: Some(machine),
            ..
        } if reachable(&outposts, machine.host.as_deref()) => {
            let needle = text.to_lowercase();
            inspections.extend(
                state
                    .window
                    .launching
                    .recents
                    .iter()
                    .filter(|r| {
                        host_of(r) != machine.host && r.path.to_lowercase().contains(&needle)
                    })
                    .map(|r| (machine.host.clone(), launcher.asked_path(state, r))),
            );
        }
        _ => {}
    }
    for (host, dir) in listings {
        request_listing(state, which, opening, host.as_deref(), &dir, tx);
    }
    for (host, path) in inspections {
        request_inspection(state, which, opening, host.as_deref(), &path, tx);
    }
}

/// Asks again what the open launchers need once the daemon's facts changed, such as an
/// outpost that became reachable.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    for which in [Which::Dialog, Which::Inline] {
        fetch_wanted(state, which, tx);
    }
}

/// Counts how long the launchers wait on an outpost, and wakes again while they do. Returns
/// whether any counted.
pub(crate) fn tick(state: &mut State, tx: &mut Tx) -> bool {
    let now_ms = state.wall_ms(tx);
    let mut counted = false;
    for which in [Which::Dialog, Which::Inline] {
        let Some(launcher) = state.window.launching.launcher(which) else {
            continue;
        };
        let every = if launcher.asking(state) {
            ASKING_TICK
        } else if launcher.busy && launcher.starting.as_ref().is_some_and(|s| s.host.is_some()) {
            STARTING_TICK
        } else {
            continue;
        };
        let launcher = state.window.launching.launcher_mut(which).expect("open");
        if now_ms > launcher.now_ms {
            launcher.now_ms = now_ms;
            counted = true;
        }
        tx.wake_at(tx.clock() + every);
    }
    counted
}

/// The text field the launcher shows: the path, or the new worktree's name while the Where
/// list shows.
pub(crate) fn field_mut(state: &mut State, which: Which) -> &mut TextEdit {
    let launching = &mut state.window.launching;
    if launching.launcher(which).is_none() {
        return launching.spare();
    }
    let launcher = launching.launcher_mut(which).expect("the launcher is open");
    match &mut launcher.place {
        Some(place) => &mut place.branch,
        None => &mut launcher.query,
    }
}

/// The user typed into the field.
pub(crate) fn edited(state: &mut State, which: Which, tx: &mut Tx) {
    let memory = match state
        .window
        .launching
        .launcher(which)
        .and_then(|l| l.place.as_ref())
    {
        Some(place) => memory(state, &place.repo, place.host.as_deref()),
        None => {
            query_changed(state, which, tx);
            return;
        }
    };
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    launcher.problem = None;
    if let Some(place) = &mut launcher.place {
        place.aim(memory);
    }
}

/// The path changed: earlier answers go stale, the highlight goes back to the top, and what
/// the new text needs is asked for.
fn query_changed(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    launcher.generation += 1;
    launcher.highlight = None;
    launcher.problem = None;
    fetch_wanted(state, which, tx);
}

/// `text` in place of what the field held, with the caret at its end. The field keeps the
/// keyboard: Rune moves focus with the edit's `focused` flag.
fn replaced(edit: &TextEdit, text: String) -> TextEdit {
    let mut new = TextEdit::new(text);
    new.anchor = new.text.len();
    new.focus = new.text.len();
    new.focused = edit.focused;
    new
}

fn set_query(state: &mut State, which: Which, text: String, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    launcher.query = replaced(&launcher.query, text);
    query_changed(state, which, tx);
}

/// Keys the launcher hears while it, or its field, has focus, after the keymap's shortcuts.
/// Returns whether it took the key.
pub(crate) fn on_key(state: &mut State, which: Which, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return false;
    };
    if launcher.busy {
        if ev.key == Key::Escape && launcher.stalled(state).is_some() {
            background(state, which, tx);
        }
        return true;
    }
    if launcher.deletion.is_some() {
        match &ev.key {
            Key::Text(t) if t.eq_ignore_ascii_case("y") => delete_worktree(state, which, tx),
            Key::Text(t) if t.eq_ignore_ascii_case("n") => cancel_deletion(state, which),
            Key::Escape => cancel_deletion(state, which),
            _ => {}
        }
        return true;
    }
    if agent_key(state, which, ev, tx) {
        return true;
    }
    let accel = |spec: &str| {
        Chord::parse(spec)
            .expect("a launcher chord parses")
            .matches_tx_on(ev, tx, KeyPlatform::current())
    };
    let launcher = state
        .window
        .launching
        .launcher(which)
        .expect("the launcher is open");
    let rows = launcher.rows(state);
    let choosing = launcher.place.as_ref().map(Where::rows);
    let branch_empty = launcher
        .place
        .as_ref()
        .is_some_and(|p| p.branch.text.is_empty());
    let selected = match &launcher.place {
        Some(place) => place.highlight,
        None => launcher.selected(state, &rows),
    };
    let browsing = matches!(launcher.address(state), Address::Browse { .. });
    match ev.key {
        Key::Down | Key::Up if ev.mods == rune::Mods::default() => {
            let len = choosing.as_ref().map_or(rows.len(), Vec::len);
            if len > 0 {
                let next = match (ev.key == Key::Down, selected) {
                    (true, None) => 0,
                    (true, Some(i)) => (i + 1) % len,
                    (false, None | Some(0)) => len - 1,
                    (false, Some(i)) => i - 1,
                };
                let launcher = state.window.launching.launcher_mut(which).expect("open");
                match &mut launcher.place {
                    Some(place) => place.highlight = Some(next),
                    None => launcher.highlight = Some(next),
                }
            }
            true
        }
        Key::Enter => {
            let terminal = accel("mod+enter");
            match choosing {
                Some(where_rows) => {
                    if let Some(row) = selected.and_then(|i| where_rows.get(i)).cloned() {
                        launch(state, which, None, terminal, Some(row), tx);
                    }
                }
                None => {
                    let row = selected.and_then(|i| rows.get(i)).cloned();
                    if row.is_some() || browsing {
                        launch(state, which, row, terminal, None, tx);
                    }
                }
            }
            true
        }
        Key::Tab if ev.mods == rune::Mods::default() => {
            tab(state, which, tx);
            true
        }
        Key::Escape => {
            step_back(state, which, tx);
            true
        }
        Key::Backspace if choosing.is_some() && accel("mod+backspace") => {
            let launcher = state.window.launching.launcher_mut(which).expect("open");
            launcher.problem = None;
            if let Some(WhereRow::Existing { path, .. }) =
                selected.and_then(|i| choosing.as_ref()?.get(i)).cloned()
            {
                launcher.deletion = Some(Deletion { path, force: false });
            }
            true
        }
        Key::Backspace if choosing.is_some() && branch_empty => {
            step_back(state, which, tx);
            true
        }
        _ => false,
    }
}

/// Escape: a start under way stays, unless its outpost stopped answering, when it finishes in
/// the background; then a deletion question goes, then the Where list, then the typed text,
/// and last the dialog. The inline launcher never goes.
fn step_back(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    if launcher.busy {
        if launcher.stalled(state).is_some() {
            background(state, which, tx);
        }
        return;
    }
    let launcher = state.window.launching.launcher_mut(which).expect("open");
    launcher.problem = None;
    if launcher.deletion.take().is_some() {
        return;
    }
    launcher.generation += 1;
    if let Some(place) = launcher.place.take() {
        launcher.query.focused = place.branch.focused;
        launcher.highlight = None;
    } else if !launcher.query.text.is_empty() {
        launcher.query = replaced(&launcher.query, String::new());
        launcher.highlight = None;
    } else if which == Which::Dialog {
        close(state, tx);
    }
}

/// Puts the launcher away while its start keeps going; the start says so if it fails.
fn background(state: &mut State, which: Which, tx: &mut Tx) {
    match which {
        Which::Dialog => close(state, tx),
        Which::Inline => state.window.launching.inline = None,
    }
}

/// Escape that reaches the dialog itself, as from its field.
pub(crate) fn escape(state: &mut State, tx: &mut Tx) {
    step_back(state, Which::Dialog, tx);
}

/// A press on the dialog's backdrop: the dialog closes unless a start is under way.
pub(crate) fn dismiss(state: &mut State, tx: &mut Tx) {
    if state
        .window
        .launching
        .launcher(Which::Dialog)
        .is_some_and(|l| !l.busy)
    {
        close(state, tx);
    }
}

/// Tab: a repository's Where list for an agent; otherwise the highlighted row, or the first
/// folder, completes into the field.
fn tab(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    if launcher.place.is_some() {
        return;
    }
    let rows = launcher.rows(state);
    let row = launcher
        .selected(state, &rows)
        .and_then(|i| rows.get(i))
        .cloned();
    let agent = launcher.agent(state) != TERMINAL;
    if agent && let Some(root) = row.as_ref().and_then(|r| r.repo_root.clone()) {
        let host = row.as_ref().and_then(|r| r.host.clone());
        open_where(state, which, root, host, tx);
        return;
    }
    if agent
        && row.is_none()
        && let Address::Browse { path, machine, .. } = launcher.address(state)
    {
        let host = machine.host;
        let path = paths::normalize(&path, &launcher.home(state, host.as_deref()));
        match launcher.known_repo(state, host.as_deref(), &path) {
            Some(true) => {
                open_where(state, which, path, host, tx);
                return;
            }
            Some(false) => {}
            None => {
                let (opening, generation) = (launcher.opening, launcher.generation);
                let asked_host = host.clone();
                state.ask(
                    |id| InspectPathMessage {
                        path,
                        host: asked_host,
                        request_id: Some(id),
                        ..InspectPathMessage::new()
                    },
                    move |state, reply, tx| {
                        let Some(launcher) = current(state, which, opening, generation) else {
                            return;
                        };
                        let result: Option<InspectPathResultMessage> = reply.parse();
                        match result {
                            Some(InspectPathResultMessage {
                                success: true,
                                inspection: Some(inspection),
                                ..
                            }) => match inspection.repo_root.filter(|r| !r.is_empty()) {
                                Some(root) if root == inspection.resolved_path => {
                                    open_where(state, which, root, host, tx);
                                }
                                _ => complete_first(state, which, None, tx),
                            },
                            other => {
                                launcher.problem = Some(
                                    other
                                        .and_then(|r| r.error)
                                        .filter(|e| !e.is_empty())
                                        .unwrap_or_else(|| "Failed to inspect path".into())
                                        .into(),
                                );
                            }
                        }
                    },
                    tx,
                );
                return;
            }
        }
    }
    complete_first(state, which, row, tx);
}

/// Puts `row`, or else the first folder listed, into the field as an address to browse.
fn complete_first(state: &mut State, which: Which, row: Option<Row>, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    let row = row.or_else(|| {
        launcher
            .rows(state)
            .into_iter()
            .find(|r| r.kind == Kind::Folder)
    });
    if let Some(row) = row {
        let text = completion(launcher, state, &row);
        set_query(state, which, text, tx);
    }
}

/// What Tab writes for `row`: its folder, as an address on its machine.
pub(crate) fn completion(launcher: &Launcher, state: &State, row: &Row) -> String {
    let path = match row.kind {
        Kind::Machine | Kind::Elsewhere => row.path.clone(),
        Kind::Recent | Kind::Folder => launcher.display(state, &row.path, row.host.as_deref()),
    };
    address_of(
        &machine_of(outposts(state), row.host.as_deref()),
        &format!("{}/", path.trim_end_matches('/')),
    )
}

fn open_where(state: &mut State, which: Which, repo: String, host: Option<String>, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    if let Some(refused) = launcher.refusal(state, host.as_deref(), TERMINAL) {
        let launcher = state.window.launching.launcher_mut(which).expect("open");
        launcher.problem = Some(refused);
        return;
    }
    let launcher = state.window.launching.launcher_mut(which).expect("open");
    launcher.generation += 1;
    launcher.problem = None;
    launcher.place = Some(Where {
        repo: repo.clone(),
        host: host.clone(),
        info: None,
        branch: replaced(&launcher.query, String::new()),
        highlight: None,
    });
    let (opening, generation) = (launcher.opening, launcher.generation);
    let (asked, asked_host) = (repo.clone(), host.clone());
    state.ask(
        |id| GetRepoInfoMessage {
            repo: asked,
            host: asked_host,
            request_id: Some(id),
            ..GetRepoInfoMessage::new()
        },
        move |state, reply, _| {
            let memory = memory(state, &repo, host.as_deref());
            let Some(launcher) = current(state, which, opening, generation) else {
                return;
            };
            let result: Option<GetRepoInfoResultMessage> = reply.parse();
            match result {
                Some(GetRepoInfoResultMessage {
                    success: true,
                    info: Some(info),
                    ..
                }) => {
                    let named = key_on(host.as_deref(), &repo);
                    launcher.names.entry(named).or_insert_with(|| {
                        let taken: Vec<String> =
                            info.worktrees.iter().map(|w| w.branch.clone()).collect();
                        worktree_name(&taken)
                    });
                    if let Some(place) = &mut launcher.place {
                        place.info = Some(info);
                        place.aim(memory);
                    }
                }
                other => {
                    launcher.problem = Some(
                        other
                            .and_then(|r| r.error)
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Failed to read repository".into())
                            .into(),
                    );
                }
            }
        },
        tx,
    );
}

/// What one start needs once it is under way, whether or not the launcher is still there.
#[derive(Clone)]
struct Start {
    which: Which,
    opening: u64,
    agent: String,
    yolo: bool,
    host: Option<String>,
    desktop: Option<String>,
    anchor: Option<String>,
    direction: Direction,
}

impl Start {
    fn spawn(&self, path: String) -> Spawn {
        Spawn {
            path,
            agent: self.agent.clone(),
            yolo: self.yolo,
            desktop: self.desktop.clone(),
            anchor: self.anchor.clone(),
            direction: self.direction,
            label: None,
            host: self.host.clone(),
        }
    }
}

/// Starts the picked row, the typed path, or the picked destination: an agent, or a terminal
/// when `terminal`, on the row's machine. A repository without a picked destination starts
/// where its last start on that machine went. A machine row, or a folder found on another
/// machine, completes into the field instead.
fn launch(
    state: &mut State,
    which: Which,
    row: Option<Row>,
    terminal: bool,
    destination: Option<WhereRow>,
    tx: &mut Tx,
) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    if launcher.busy {
        return;
    }
    if let Some(row) = row
        .as_ref()
        .filter(|r| matches!(r.kind, Kind::Machine | Kind::Elsewhere))
    {
        let text = completion(launcher, state, row);
        set_query(state, which, text, tx);
        return;
    }
    let address = launcher.address(state);
    let browsed = match &address {
        Address::Browse { path, machine, .. } => Some((path.clone(), machine.host.clone())),
        _ => None,
    };
    let host = match (&launcher.place, &row) {
        (Some(place), _) => place.host.clone(),
        (None, Some(row)) => row.host.clone(),
        (None, None) => browsed.as_ref().and_then(|(_, host)| host.clone()),
    };
    let agent = if terminal {
        TERMINAL.to_owned()
    } else {
        launcher.agent(state)
    };
    if let Some(refused) = launcher.refusal(state, host.as_deref(), &agent) {
        let launcher = state.window.launching.launcher_mut(which).expect("open");
        launcher.problem = Some(refused);
        return;
    }
    let start = Start {
        which,
        opening: launcher.opening,
        yolo: agent != TERMINAL && launcher.choice.yolo_for(state, &agent),
        agent,
        host: host.clone(),
        desktop: launcher.desktop.clone(),
        anchor: launcher.anchor.clone(),
        direction: launcher.direction,
    };
    let typed = match (&launcher.place, &row, browsed) {
        (None, None, Some((path, _))) => Some(paths::normalize(
            &path,
            &launcher.home(state, host.as_deref()),
        )),
        _ => None,
    };
    let at_ms = state.wall_ms(tx);
    let launcher = state.window.launching.launcher_mut(which).expect("open");
    launcher.generation += 1;
    launcher.busy = true;
    launcher.problem = None;
    launcher.now_ms = at_ms;
    launcher.starting = Some(Starting {
        host: host.clone(),
        at_ms,
    });
    if host.is_some() {
        tx.wake_at(tx.clock() + STARTING_TICK);
    }
    let Some(typed) = typed else {
        let (path, repo) = row.map_or((None, None), |r| (Some(r.path), r.repo_root));
        to_destination(state, start, path, repo, destination, tx);
        return;
    };
    let asked = typed.clone();
    state.ask(
        |id| InspectPathMessage {
            path: asked,
            host,
            request_id: Some(id),
            ..InspectPathMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<InspectPathResultMessage> = reply.parse();
            match result.and_then(|r| r.inspection) {
                Some(inspection) if inspection.exists && inspection.is_directory => {
                    let path = inspection.resolved_path;
                    let repo = inspection.repo_root.filter(|root| *root == path);
                    to_destination(state, start, Some(path), repo, None, tx);
                }
                _ => failed(state, &start, format!("Directory not found: {typed}"), tx),
            }
        },
        tx,
    );
}

fn to_destination(
    state: &mut State,
    start: Start,
    path: Option<String>,
    repo: Option<String>,
    destination: Option<WhereRow>,
    tx: &mut Tx,
) {
    let host = start.host.clone();
    let place = this_ref(state, start.which, start.opening).and_then(|l| {
        l.place
            .as_ref()
            .map(|p| (p.repo.clone(), l.worktree_name()))
    });
    let repo = place.as_ref().map(|(repo, _)| repo.clone()).or(repo);
    let destination = destination.or_else(|| {
        let repo = repo.as_ref()?;
        Some(
            if start.agent != TERMINAL
                && memory(state, repo, host.as_deref()) == Memory::NewWorktree
            {
                WhereRow::New {
                    current: false,
                    branch: String::new(),
                }
            } else {
                WhereRow::Main {
                    branch: String::new(),
                }
            },
        )
    });
    match (destination, repo) {
        (Some(WhereRow::New { branch, .. }), Some(repo)) => {
            if let Some((_, name)) = place {
                new_worktree(state, start, repo, name, branch, tx);
                return;
            }
            let name = match this(state, start.which, start.opening) {
                Some(launcher) => launcher
                    .names
                    .entry(key_on(host.as_deref(), &repo))
                    .or_insert_with(|| worktree_name(&[]))
                    .clone(),
                None => worktree_name(&[]),
            };
            let asked = repo.clone();
            state.ask(
                |id| GetRepoInfoMessage {
                    repo: asked,
                    host,
                    request_id: Some(id),
                    ..GetRepoInfoMessage::new()
                },
                move |state, reply, tx| {
                    let result: Option<GetRepoInfoResultMessage> = reply.parse();
                    match result {
                        Some(GetRepoInfoResultMessage {
                            success: true,
                            info: Some(info),
                            ..
                        }) => new_worktree(state, start, repo, name, info.default_branch, tx),
                        other => failed(
                            state,
                            &start,
                            other
                                .and_then(|r| r.error)
                                .filter(|e| !e.is_empty())
                                .unwrap_or_else(|| "Failed to read repository".into()),
                            tx,
                        ),
                    }
                },
                tx,
            );
        }
        (destination, repo) => {
            let target = match &destination {
                Some(WhereRow::Existing { path, .. }) => Some(path.clone()),
                Some(WhereRow::Main { .. }) => repo.clone(),
                _ => path,
            };
            let Some(target) = target else {
                failed(state, &start, String::new(), tx);
                return;
            };
            let memory =
                matches!(destination, Some(WhereRow::Main { .. })).then_some(Memory::MainRepo);
            begin(state, start, target, repo.zip(memory), tx);
        }
    }
}

fn new_worktree(
    state: &mut State,
    start: Start,
    repo: String,
    branch: String,
    from: String,
    tx: &mut Tx,
) {
    let made = format!("{}:{branch}:{from}", key_on(start.host.as_deref(), &repo));
    let existing =
        this_ref(state, start.which, start.opening).and_then(|l| l.created.get(&made).cloned());
    let remember = Some((repo.clone(), Memory::NewWorktree));
    if let Some(path) = existing {
        begin(state, start, path, remember, tx);
        return;
    }
    let host = start.host.clone();
    state.ask(
        |id| CreateWorktreeMessage {
            main_repo: repo,
            branch,
            starting_from: Some(from),
            host,
            request_id: Some(id),
            ..CreateWorktreeMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<CreateWorktreeResultMessage> = reply.parse();
            match result {
                Some(CreateWorktreeResultMessage {
                    success: true,
                    path: Some(path),
                    ..
                }) => {
                    if let Some(launcher) = this(state, start.which, start.opening) {
                        launcher.created.insert(made, path.clone());
                    }
                    begin(state, start, path, remember, tx);
                }
                other => failed(
                    state,
                    &start,
                    other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Failed to create worktree".into()),
                    tx,
                ),
            }
        },
        tx,
    );
}

/// Starts the session in `path`; once it started, writes where the repository's start on
/// its machine went and puts the launcher away.
fn begin(
    state: &mut State,
    start: Start,
    path: String,
    memory: Option<(String, Memory)>,
    tx: &mut Tx,
) {
    let spawn = start.spawn(path);
    spawn_then(
        state,
        spawn,
        move |state, result, tx| match result {
            Ok(()) => {
                if let Some((repo, memory)) = memory
                    && start.agent != TERMINAL
                {
                    remember(state, &repo, start.host.as_deref(), memory);
                }
                if this(state, start.which, start.opening).is_some() {
                    background(state, start.which, tx);
                }
            }
            Err(error) => failed(state, &start, error, tx),
        },
        tx,
    );
}

/// A start that did not get through: the launcher stays as it was, saying why. A start the
/// user left to finish in the background says why in a notice.
fn failed(state: &mut State, start: &Start, error: String, tx: &mut Tx) {
    let machine = machine_name(outposts(state), start.host.as_deref());
    match this(state, start.which, start.opening) {
        Some(launcher) => {
            launcher.busy = false;
            launcher.starting = None;
            launcher.problem = (!error.is_empty()).then(|| error.into());
        }
        None if !error.is_empty() => {
            state
                .window
                .notify(format!("Starting on {machine} failed: {error}"), tx);
        }
        None => {}
    }
}

fn cancel_deletion(state: &mut State, which: Which) {
    if let Some(launcher) = state.window.launching.launcher_mut(which) {
        launcher.deletion = None;
        launcher.problem = None;
    }
}

/// Deletes the worktree the user confirmed, on the repository's machine; a refusal that force
/// would get past asks again.
fn delete_worktree(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    let Some(deletion) = &launcher.deletion else {
        return;
    };
    let (path, force, opening) = (deletion.path.clone(), deletion.force, launcher.opening);
    let host = launcher.place.as_ref().and_then(|p| p.host.clone());
    launcher.busy = true;
    launcher.problem = None;
    let asked = path.clone();
    state.ask(
        |id| DeleteWorktreeMessage {
            path: asked,
            force: force.then_some(true),
            host,
            request_id: Some(id),
            ..DeleteWorktreeMessage::new()
        },
        move |state, reply, _| {
            let Some(launcher) = this(state, which, opening) else {
                return;
            };
            launcher.busy = false;
            let result: Option<DeleteWorktreeResultMessage> = reply.parse();
            match result {
                Some(r) if r.success => {
                    if let Some(place) = &mut launcher.place {
                        let remaining = place.rows().len().saturating_sub(1);
                        if let Some(info) = &mut place.info {
                            info.worktrees.retain(|w| w.path != path);
                        }
                        place.highlight =
                            place.highlight.map(|h| h.min(remaining.saturating_sub(1)));
                    }
                    launcher.deletion = None;
                }
                other => {
                    let forceable = other.as_ref().and_then(|r| r.forceable) == Some(true);
                    launcher.problem = Some(
                        other
                            .and_then(|r| r.error)
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Failed to delete worktree".into())
                            .into(),
                    );
                    launcher.deletion = match launcher.deletion.take() {
                        Some(d) if !d.force && forceable => Some(Deletion { force: true, ..d }),
                        _ => None,
                    };
                }
            }
        },
        tx,
    );
}

/// A press on a row of the locations: highlights it and starts it.
pub(crate) fn pick(state: &mut State, which: Which, index: usize, row: Row, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    if launcher.busy {
        return;
    }
    launcher.highlight = Some(index);
    launch(state, which, Some(row), false, None, tx);
}

/// A press on a row of the Where list: highlights it and starts there.
pub(crate) fn pick_destination(
    state: &mut State,
    which: Which,
    index: usize,
    row: WhereRow,
    tx: &mut Tx,
) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    if launcher.busy || launcher.deletion.is_some() {
        return;
    }
    if let Some(place) = &mut launcher.place {
        place.highlight = Some(index);
    }
    launch(state, which, None, false, Some(row), tx);
}
