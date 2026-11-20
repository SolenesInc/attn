use super::flags::FlagKind;

pub struct Verb {
    pub name: &'static str,
    pub flags: &'static [&'static str],
    pub read_only: bool,
}

pub struct FlagDef {
    pub name: &'static str,
    pub kind: FlagKind,
}

pub const VERBS: &[Verb] = &[
    Verb {
        name: r"plant",
        flags: &["m", "part-of", "discovered-from", "check", "json"],
        read_only: false,
    },
    Verb {
        name: r"plot",
        flags: &["f", "json"],
        read_only: false,
    },
    Verb {
        name: r"ls",
        flags: &["plot", "stale", "window", "standby", "flat", "json"],
        read_only: true,
    },
    Verb {
        name: r"search",
        flags: &["limit", "json"],
        read_only: true,
    },
    Verb {
        name: r"ready",
        flags: &["plot", "all", "json"],
        read_only: true,
    },
    Verb {
        name: r"link",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"unlink",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"show",
        flags: &["since", "section", "json"],
        read_only: true,
    },
    Verb {
        name: r"review",
        flags: &[],
        read_only: false,
    },
    Verb {
        name: r"review start",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"review show",
        flags: &["json"],
        read_only: true,
    },
    Verb {
        name: r"review cancel",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"review retry",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"review keep",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"watch",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"unwatch",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"edit",
        flags: &["m", "note", "json"],
        read_only: false,
    },
    Verb {
        name: r"rollback",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"authorization",
        flags: &["clear", "json"],
        read_only: false,
    },
    Verb {
        name: r"remind",
        flags: &["in", "at", "m", "clear", "json"],
        read_only: false,
    },
    Verb {
        name: r"standby",
        flags: &["m", "in", "at", "stay", "clear", "json"],
        read_only: false,
    },
    Verb {
        name: r"send-to-chief",
        flags: &["m", "json"],
        read_only: false,
    },
    Verb {
        name: r"tend",
        flags: &["m", "for", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"park",
        flags: &["m", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"harvest",
        flags: &["m", "force", "json", "when-merged", "clear"],
        read_only: false,
    },
    Verb {
        name: r"wither",
        flags: &["m", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"replant",
        flags: &["m", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"note",
        flags: &["m", "handoff", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check",
        flags: &[],
        read_only: false,
    },
    Verb {
        name: r"check add",
        flags: &["m", "ring", "json", "shared"],
        read_only: false,
    },
    Verb {
        name: r"check edit",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check remove",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check restore",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check report",
        flags: &["m", "ring", "json", "evidence", "commit"],
        read_only: false,
    },
    Verb {
        name: r"check confirm",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check reject",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check uncheck",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check reopen",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"attach",
        flags: &[
            "path", "repo", "notebook", "url", "m", "json", "move", "copy",
        ],
        read_only: false,
    },
    Verb {
        name: r"detach",
        flags: &[
            "path",
            "repo",
            "notebook",
            "url",
            "m",
            "json",
            "to",
            "reference",
        ],
        read_only: false,
    },
    Verb {
        name: r"notes",
        flags: &["since", "kind", "limit", "json"],
        read_only: true,
    },
    Verb {
        name: r"guide",
        flags: &[],
        read_only: false,
    },
    Verb {
        name: r"prime",
        flags: &[],
        read_only: true,
    },
    Verb {
        name: r"export",
        flags: &["out", "json"],
        read_only: true,
    },
];

pub const FLAGS: &[FlagDef] = &[
    FlagDef {
        name: r"m",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"note",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"part-of",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"discovered-from",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"stay",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"clear",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"in",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"at",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"when-merged",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"plot",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"all",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"flat",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"stale",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"window",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"standby",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"f",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"handoff",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"ring",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"check",
        kind: FlagKind::Repeated,
    },
    FlagDef {
        name: r"shared",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"evidence",
        kind: FlagKind::Repeated,
    },
    FlagDef {
        name: r"commit",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"path",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"repo",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"notebook",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"url",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"move",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"copy",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"to",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"reference",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"force",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"for",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"profile",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"session",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"out",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"limit",
        kind: FlagKind::Int,
    },
    FlagDef {
        name: r"since",
        kind: FlagKind::Int,
    },
    FlagDef {
        name: r"section",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"kind",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"json",
        kind: FlagKind::Bool,
    },
];
