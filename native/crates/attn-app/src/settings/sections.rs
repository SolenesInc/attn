use attn_protocol::go_values::FEATURES;

/// A page of the settings dialog. Its id is the Tauri app's section id, which the
/// automation `state` reports and drive-attn names.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) enum Section {
    General,
    Desktop,
    Hygiene,
    Agents,
    Models,
    BackgroundAgents,
    Delegation,
    #[default]
    Connectivity,
    Outposts,
    Phones,
    Senders,
    Plugins,
    Updates,
    Experimental,
    BackgroundTasks,
    EventBus,
    Analytics,
}

pub(crate) struct Item {
    pub(crate) section: Section,
    pub(crate) label: &'static str,
    pub(crate) description: &'static str,
    keywords: &'static str,
    /// The setting that turns the page's feature on: the page head carries its switch, and the
    /// page is dimmed while it is off.
    pub(crate) feature: Option<&'static str>,
}

pub(crate) struct Group {
    pub(crate) label: &'static str,
    pub(crate) items: &'static [Item],
}

const fn item(
    section: Section,
    label: &'static str,
    description: &'static str,
    keywords: &'static str,
) -> Item {
    Item {
        section,
        label,
        description,
        keywords,
        feature: None,
    }
}

const fn feature_item(
    section: Section,
    label: &'static str,
    feature: &'static str,
    keywords: &'static str,
) -> Item {
    Item {
        section,
        label,
        description: "",
        keywords,
        feature: Some(feature),
    }
}

pub(crate) const GROUPS: &[Group] = &[
    Group {
        label: "attn",
        items: &[
            item(
                Section::General,
                "Appearance",
                "Theme, text size and layout widths, for the app and for the garden.",
                "theme appearance dark light system font size text scale zoom garden layout narrowest tile width columns sidebar width",
            ),
            item(
                Section::Desktop,
                "Files and locations",
                "Where attn opens repositories and worktrees, when merged worktrees are reclaimed, where your Notebook lives, and what it does with a file an agent sends you.",
                "projects directory worktrees roots notebook folder knowledge base journal location editor executable sent files tiles open markdown worktree sweep reclaim merged keep pin",
            ),
            item(
                Section::Hygiene,
                "Attention queue",
                "When a turn settles itself, which notifications stay out of the queue, whether hidden sessions, crew members and the Chief join it, and which repositories and authors never reach it.",
                "muted repositories repos authors hide unmute hygiene auto-settle settle turn countdown sidebar attention queue quiet notifications show hidden sessions shared codex crew member chief put the crew in the queue",
            ),
        ],
    },
    Group {
        label: "Agents",
        items: &[
            item(
                Section::Agents,
                "Agents and models",
                "Defaults for new sessions, with configuration for each agent.",
                "agents executables claude codex default asks questions answer decisions drawer capabilities model effort pricing context window cap tokens compaction auto-approve unattended direct agent launches memory shared terminal host pty backend rust workers seeds standby pull request pr comments ring quiet",
            ),
            item(
                Section::Models,
                "Models",
                "Tiers describe how much intelligence a task needs. Your changes apply across profiles on this daemon; Reset restores attn’s mapping.",
                "harness model tier light standard deep overrides defaults",
            ),
            item(
                Section::BackgroundAgents,
                "Background agents",
                "The agents that summarize session activity, review the garden, and coordinate work.",
                "chief model effort headless context cap session activity summary refresh garden advisor",
            ),
            item(
                Section::Delegation,
                "Delegation",
                "",
                "delegate roles pathfinder builder reviewer orchestrator prototyper fallback harness models effort preferences alternatives",
            ),
        ],
    },
    Group {
        label: "Connectivity",
        items: &[
            item(
                Section::Connectivity,
                "GitHub hosts",
                "The GitHub hosts attn detected.",
                "github hosts",
            ),
            feature_item(
                Section::Outposts,
                "Outposts",
                "outposts_enabled",
                "outposts bridge host machine address pair re-pair remove connection build fingerprint connect other computers",
            ),
            feature_item(
                Section::Phones,
                "Phones",
                "phone_access_enabled",
                "phones phone mobile iphone pair pairing qr code revoke attn cloud relay",
            ),
            item(
                Section::Senders,
                "Paired senders",
                "Programs outside attn that send files and messages as you.",
                "senders paired sender pair pairing send program script remove unpair attn send",
            ),
        ],
    },
    Group {
        label: "Extensions",
        items: &[item(
            Section::Plugins,
            "Plugins",
            "Add plugins from a local directory or Git repository, and set provider dispatch priority.",
            "plugins extensions providers priority install healthcheck",
        )],
    },
    Group {
        label: "System",
        items: &[
            Item {
                section: Section::Updates,
                label: "Updates",
                description: "How attn was installed and what is available.",
                keywords: "update upgrade version release install build",
                feature: Some("self_update_enabled"),
            },
            item(
                Section::Experimental,
                "Experimental",
                "Features still in development. Each applies to this home and its outposts.",
                "experimental",
            ),
            item(
                Section::BackgroundTasks,
                "Task runner",
                "Runs compaction, session summaries, desktop narration, and reconciliation. Exhausted retries raise a notification; retry the task here.",
                "background tasks runner durable compaction summarize narrate reconcile retry failed dead queue",
            ),
            item(
                Section::EventBus,
                "Event bus",
                "The durable event log: what it holds, who writes to it, and who reads it. Also available through attn bus status.",
                "event bus log durable facts producers consumers cursor lag retention compaction trim stalled disabled kill switch seq",
            ),
            feature_item(
                Section::Analytics,
                "Analytics",
                "analytics.enabled",
                "analytics usage telemetry vents vent agents outbox records recorded bus app cli sample run runs retention",
            ),
        ],
    },
];

impl Section {
    pub(crate) fn id(self) -> &'static str {
        match self {
            Section::General => "general",
            Section::Desktop => "desktop",
            Section::Hygiene => "hygiene",
            Section::Agents => "agents",
            Section::Models => "models",
            Section::BackgroundAgents => "backgroundAgents",
            Section::Delegation => "delegation",
            Section::Connectivity => "connectivity",
            Section::Outposts => "outposts",
            Section::Phones => "phones",
            Section::Senders => "senders",
            Section::Plugins => "plugins",
            Section::Updates => "updates",
            Section::Experimental => "experimental",
            Section::BackgroundTasks => "backgroundTasks",
            Section::EventBus => "eventBus",
            Section::Analytics => "analytics",
        }
    }

    pub(crate) fn item(self) -> &'static Item {
        GROUPS
            .iter()
            .flat_map(|g| g.items)
            .find(|i| i.section == self)
            .expect("every section has a nav item")
    }
}

impl Item {
    fn matches(&self, query: &str) -> bool {
        let experimental = if self.section == Section::Experimental {
            FEATURES
                .iter()
                .filter(|f| f.stage == "experimental")
                .map(|f| format!("{} {}", f.name, f.search))
                .collect::<Vec<_>>()
                .join(" ")
        } else {
            String::new()
        };
        format!(
            "{} {} {} {experimental}",
            self.label, self.description, self.keywords
        )
        .to_lowercase()
        .contains(query)
    }
}

/// The nav groups the search leaves, each with the items whose words hold the whole query.
pub(crate) fn filtered(search: &str) -> Vec<(&'static str, Vec<&'static Item>)> {
    let query = search.trim().to_lowercase();
    GROUPS
        .iter()
        .map(|group| {
            let items = group
                .items
                .iter()
                .filter(|item| query.is_empty() || item.matches(&query))
                .collect::<Vec<_>>();
            (group.label, items)
        })
        .filter(|(_, items)| !items.is_empty())
        .collect()
}
