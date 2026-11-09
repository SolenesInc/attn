use super::flags::FlagKind;

pub struct Verb {
    pub name: &'static str,
    pub synopsis: &'static str,
    pub about: &'static str,
    pub flags: &'static [&'static str],
    pub read_only: bool,
}

pub struct FlagDef {
    pub name: &'static str,
    pub argument: &'static str,
    pub about: &'static str,
    pub kind: FlagKind,
}

macro_rules! move_authority {
    () => {
        r#"
A claim yields to its tender, sessions above it in the delegation chain, or
the profile's chief. A crew member's claim lasts across the member's sessions;
a session's claim holds while the session is live and returns when it resumes.
The log records standing and forced moves and whose claim ended. Everyone else
needs --force. A call without a live session has no standing."#
    };
}

pub const VERBS: &[Verb] = &[
    Verb {
        name: r"plant",
        synopsis: r#"plant "<title>" [-m <body>] [--part-of <plot>] [--discovered-from <seed>] [--check "[C#=]<statement>"]... [flags]"#,
        about: r"plant a seed and print its id, slug and title. -m takes markdown, or - to read stdin —
if the seed gains children, that body is the plot's plan. --part-of
plants it under a plot. --check defines what the seed must prove;
attn numbers it unless C#= names the id, and prints each id.
Lists up to 3 similar open seeds in your profile, leaving out that plot.
Planting always proceeds. If one is the same work, wither the new seed.
--json prints the new seed alone.",
        flags: &["m", "part-of", "discovered-from", "check", "json"],
        read_only: false,
    },
    Verb {
        name: r"plot",
        synopsis: r"plot [-f <path>] [--json]",
        about: r#"plant a whole plot in one move from a JSON payload (-f, or stdin):
{"title": …, "body": …, "checks": [{"id": "C5", "text": …, "shared": true}],
"part_of": "<plot>", "discovered_from": "<seed>",
"children": [{"title": …, "body": …, "checks": […],
"after": ["<sibling step slug>"]}]}. Children are parallel by
default; after names the siblings a child waits for. part_of and
discovered_from are optional and take an id or a slug. A shared check is
proven by every child. The plot, its children and their edges commit
together. Prints the plot's id and each child's id and step slug, then
up to 3 similar open seeds across all planted titles, each naming the
new seed it resembles. Planting always proceeds; wither a new seed if a
suggestion is the same work.
--json prints the whole result, including similar seeds."#,
        flags: &["f", "json"],
        read_only: false,
    },
    Verb {
        name: r"ls",
        synopsis: r"ls [--plot <plot> | --stale [--window <duration>] | --standby] [--flat] [--json]",
        about: r"the garden, newest first, with children nested under their plot.
--flat prints one list. --plot lists every child of one plot, oldest
first, harvested and withered included. --stale narrows to open seeds
whose log has not moved for the window (default {stale_window}) — a query for
your judgment, never a reaper. --standby narrows to seeds on standby,
each with the pull requests it watches and their readiness, its
reminder, and why a resume is blocked when one is. --json prints the
table's rows, without bodies or checks; attn seed show <id> reads one
seed whole.",
        flags: &["plot", "stale", "window", "standby", "flat", "json"],
        read_only: true,
    },
    Verb {
        name: r"search",
        synopsis: r"search <words> [--limit <n>] [--json]",
        about: r"find seeds by words across your profile's garden, harvested and withered
included: titles, bodies and log entries. Words match as stemmed prefixes;
identifier parts match too. Seeds carrying some words also appear.
Ranks by title coverage, word coverage, relevance and recency, with a boost
for open seeds. Prints status, last active and an original matching line.
Shows the best 10 by default; --limit asks for more, up to 1000.
Search before you plant, so a duplicate is found instead of created.",
        flags: &["limit", "json"],
        read_only: true,
    },
    Verb {
        name: r"ready",
        synopsis: r"ready [--plot <plot> | --all] [--json]",
        about: r"what you can tend right now, oldest first: nothing open blocks it,
nobody is holding it, and it is not a plot — a plot's work is its
children. With no flags the scope is your profile's garden — unless this
session was dispatched at a plot, and then that plot; --all steps back
out to the garden. --json prints rows, without bodies or checks.",
        flags: &["plot", "all", "json"],
        read_only: true,
    },
    Verb {
        name: r"link",
        synopsis: r"link <a> <blocks|part-of|discovered-from> <b> [--json]",
        about: r#"relate two seeds. "a blocks b" keeps b out of ready until a closes;
"a part-of b" puts a under b; b is then a plot. "a discovered-from b"
records where work came from without ordering it. A structural cycle
is refused, naming both seeds and the edge to remove."#,
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"unlink",
        synopsis: r"unlink <a> <blocks|part-of|discovered-from> <b> [--json]",
        about: r"remove the edge. Every link has one.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"show",
        synopsis: r"show <id> [--since <bookmark> | --section <heading|id>] [--json]",
        about: r"one seed, news first: its state, who tends it and a bookmark; the newest
entries on its log, and where the freshest handoff is when it is older
than those; every edge that touches it, with seeds discovered from it
folded to a count and the open ones; the outline of the plot it is part
of; a plot's open children as a board, each with what it waits for, its
checks and its newest entry, closed children folded into one line;
checks and artifacts; the body last.
--since <bookmark> prints only the log entries saved after it, on this
seed and every seed below it, newest first and labeled by seed, then a
new bookmark. --section prints one part of the body: the section under a
heading, or every item that starts with an id like D3.",
        flags: &["since", "section", "json"],
        read_only: true,
    },
    Verb {
        name: r"review",
        synopsis: r"review <start|show|cancel|retry|keep>",
        about: r"start an advisory Garden review, inspect its progressive results,
cancel unfinished classification, retry one failed or changed item, or keep
one seed growing and review it again after seven quiet days.
The review never changes a seed's state.",
        flags: &[],
        read_only: false,
    },
    Verb {
        name: r"review start",
        synopsis: r"review start [--json]",
        about: r"Start an advisory Garden review. The review never changes a seed's state.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"review show",
        synopsis: r"review show [<review-id>] [--json]",
        about: r"Inspect the review and its progressive results. With no id, show the latest review.",
        flags: &["json"],
        read_only: true,
    },
    Verb {
        name: r"review cancel",
        synopsis: r"review cancel <review-id> [--json]",
        about: r"Cancel unfinished classification.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"review retry",
        synopsis: r"review retry <review-id> <seed-id> [--json]",
        about: r"Retry one failed or changed review item.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"review keep",
        synopsis: r"review keep <review-id> <seed-id> [--json]",
        about: r"Keep a seed growing and review it again after seven quiet days.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"watch",
        synopsis: r"watch <id> [--json]",
        about: r"watch this seed and its descendants. Delegation starts the same watch.
Tending a plot already covers its descendants until the claim ends.
unwatch removes this seed's subscription; separate child or ancestor
watches remain. show names the subscriptions that still cover a seed.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"unwatch",
        synopsis: r"unwatch <id> [--json]",
        about: r"Remove this seed's subscription. Separate child or ancestor watches remain.
show names the subscriptions that still cover a seed.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"edit",
        synopsis: r#"edit <id> -m - [--note "<what changed>"] [--json]"#,
        about: r"save an exported body after editing the file with any tool. Keep the first
stamp line. If the body changed since export, export again and reapply.
Empty bodies are refused. Each edit saves a numbered version and a log entry;
--note adds what changed to that entry.",
        flags: &["m", "note", "json"],
        read_only: false,
    },
    Verb {
        name: r"rollback",
        synopsis: r"rollback <id> [<version>] [--json]",
        about: r"restore the body before the newest version, or the numbered version.
The restored body becomes a new version, so rollback can itself be rolled back.
Edit entries in notes show the versions and size changes.",
        flags: &["json"],
        read_only: false,
    },
    Verb {
        name: r"authorization",
        synopsis: r#"authorization <id> ["<words>" | --clear] [--json]"#,
        about: r"set, clear or print the task's authorization: the steps agents take beyond
the project's guidance and the steps they stop before, written as
instructions. Set only what the user allowed.
Delegation openings deliver the words. When someone other than the seed's
tender changes the words, attn types the change into the tender's conversation
as the user's message, waiting while the tender needs approval. With
neither words nor --clear, prints them and the state of the last typed update.
Who may change them follows seed moves: the tender, sessions above it in
the delegation chain, and the profile's chief; anyone in the profile while
nobody holds the seed.",
        flags: &["clear", "json"],
        read_only: false,
    },
    Verb {
        name: r"remind",
        synopsis: r#"remind <id> (--in <duration> | --at <time>) -m "<message>" | remind <id> --clear [--json]"#,
        about: r#"set a reminder: at that time attn posts the message to the seed's mailbox,
which delivers it to whoever tends the seed then. When nobody tends it, the
message waits for the next tender, who reads it with attn agent inbox.
--in counts from now, as in 20m, 2h or 1h30m. --at takes an RFC3339 time.
attn checks once a minute, so a message can arrive up to a minute late.
A seed holds one reminder, and a new one replaces it. The reminder ends when
attn posts the message, when --clear removes it, and when the seed is
harvested or withered. show and ls print it until then.
Example: attn seed remind s-7k3f9m --in 20m -m "Check the rollout of the new build"
While someone tends the seed, only the tender, sessions above it in the
delegation chain, the profile's chief and the user can set or clear its
reminder. There is no --force."#,
        flags: &["in", "at", "m", "clear", "json"],
        read_only: false,
    },
    Verb {
        name: r"standby",
        synopsis: r#"standby <id> -m "<what to do next>" [--close [--compact]] | standby <id> --clear [--json]"#,
        about: r#"put a seed you tend on standby with a note saying what to do next. While
it is on standby, mail to the seed (attn agent msg <id>, a reminder, an ask
answer) reaches you: when your session is closed, attn resumes it as itself and
delivers the mail with the note. The setting garden.tender_resume chooses which
mail resumes a session: any (the default), watches or off. Standby ends once
you have read all of the seed's mail with attn agent inbox, and when the seed
is parked, harvested, withered or gets a new tender. An ask answer attn types
into your session counts as read. If your session closes before you read the
mail, the mail resumes it again at once. --close then closes this session as
attn agent close --self does; it refuses while the seed's mailbox holds mail
you have not read.
--close --compact first waits for your turn to end and compacts your
conversation, as attn agent close --self --compact does, so the session that
resumes picks up a shorter conversation. End your turn right after the command.
Mail you read before the compact ends the standby, and you stay open. Mail you
leave unread does not cancel the close: the session closes and the mail resumes it
again. Mail that arrives during the compact waits until the session has closed.
Only a Claude session with the Claude mod and a Codex session on the shared Codex
app server compact, on the home machine or an outpost: attn refuses --compact for
any other session and leaves standby unset. The refusal says why.
Only the seed's tender sets standby. --clear ends standby.
Example: attn seed standby s-7k3f9m -m "Waiting for review on #123. On approval: merge, then follow the deploy." --close"#,
        flags: &["m", "close", "compact", "clear", "json"],
        read_only: false,
    },
    Verb {
        name: r"send-to-chief",
        synopsis: r#"send-to-chief <id> [-m "<optional guidance>"] [--json]"#,
        about: r"give this seed to the Chief to decide its next working context. The
saved folder, branch and placement problem are recorded automatically;
-m adds an exception such as a special branch or directory.",
        flags: &["m", "json"],
        read_only: false,
    },
    Verb {
        name: r"tend",
        synopsis: r#"tend <id> [-m "<why>"] [--for <name>] [--force] [--json]"#,
        about: concat!(
            r#"claim the seed and start growing it. The freshest handoff prints on
the claim, so picking a seed up primes you. Tending your own claim again
changes nothing and rings nobody. --for <name> assigns the seed to a crew
member instead; the user never claims a seed, so a tend from outside an attn
session needs it."#,
            move_authority!()
        ),
        flags: &["m", "for", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"park",
        synopsis: r#"park <id> [-m "<where you left it>"] [--force] [--json]"#,
        about: concat!(
            r#"pause the seed deliberately — it is parked and lets go of its
tender. An optional comment lands on the log in the same move. Tending
it again picks it back up. Parking it again keeps the state and adds
only the optional comment. Moves print who they rang."#,
            move_authority!()
        ),
        flags: &["m", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"harvest",
        synopsis: r#"harvest <id> [-m "<what got done>"] [--when-merged [<pr-url>] [--clear]] [--force] [--json]"#,
        about: concat!(
            r#"close the seed as done, with a required -m reason. Repeating a harvest
succeeds with the earlier reason; a different reason goes on its log.
A withered seed must be replanted first. A plot refuses while any direct
child is open, including with --force or --when-merged; replant hands it
back if your part is done. Closing the last child leaves a ringing note
on the plot, names it here and keeps the plot's state. Moves print who they rang.
--when-merged releases your claim and harvests the seed with your -m
reason when the pull request merges. Without -m it uses "PR #N merged:
<title>". This command merges nothing. Use it only when the merge is
the last step. With no url it takes this session's single open pull
request; --when-merged --clear takes the arming back."#,
            move_authority!()
        ),
        flags: &["m", "force", "json", "when-merged", "clear"],
        read_only: false,
    },
    Verb {
        name: r"wither",
        synopsis: r#"wither <id> [-m "<why>"] [--force] [--json]"#,
        about: concat!(
            r#"close the seed as abandoned. Nobody is picking this up. Repeating it
keeps the earlier reason and adds a different reason to the log. A
harvested seed must be replanted first. A plot refuses while any direct
child is open, even with --force; replant hands it back if your part is done.
Closing its last child leaves a ringing note on the plot and names it here.
Moves print who they rang."#,
            move_authority!()
        ),
        flags: &["m", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"replant",
        synopsis: r#"replant <id> [-m "<why>"] [--force] [--json]"#,
        about: concat!(
            r#"put the seed back in the pool — planted, unclaimed, ready for whoever
is free. Reopens a closed seed, takes a parked one off hold, and hands back
one being grown. Replanting an already planted seed changes nothing
and rings nobody. Moves print who they rang."#,
            move_authority!()
        ),
        flags: &["m", "force", "json"],
        read_only: false,
    },
    Verb {
        name: r"note",
        synopsis: r#"note <id> -m "<what happened>" [--handoff] [--ring] [--json]"#,
        about: r"append to the seed's log — what happened and what you learned, for
whoever tends it next. - reads stdin. --handoff addresses it to your
successor on this seed: tend prints the freshest one on the claim, so
it is read before any work, and show points at it once newer entries
bury it.
--ring tells current and covering plot tenders and watchers to look;
ordinary notes stay quiet.",
        flags: &["m", "handoff", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check",
        synopsis: r"check <add|edit|remove|restore|report|confirm|reject|uncheck|reopen>",
        about: r"Define, report, or judge what a seed must prove. Each move has its own --help.
Checks never block a move. harvest lists the checks still open or rejected.",
        flags: &[],
        read_only: false,
    },
    Verb {
        name: r"check add",
        synopsis: r#"check add <id> [<C#>] -m "<statement>" [--shared]"#,
        about: r"Define a statement the seed must prove and print its id, which the body
cites. attn picks the next free id in the plot tree; <C#> names one
instead. On a plot, --shared makes every seed below it prove the check
with its own report. Removed ids are never reused.",
        flags: &["m", "ring", "json", "shared"],
        read_only: false,
    },
    Verb {
        name: r"check edit",
        synopsis: r#"check edit <id> <C#> -m "<statement>""#,
        about: r"Replace the check's statement.",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check remove",
        synopsis: r"check remove <id> <C#>",
        about: r"Remove a check. Its id stays reserved.",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check restore",
        synopsis: r"check restore <id> <C#>",
        about: r"Bring a removed check back with its result.",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check report",
        synopsis: r#"check report <id> <C#> -m "<summary line>[\n\n<detail>]" [--evidence <file|artifact|url>]... [--commit <rev>]"#,
        about: r"Record the proof for a check in one call. The first line of -m is the
summary line, at most 400 characters, which show prints. The lines after
it are detail, kept in the report's check note; the whole -m fits in
32 KiB. -m - reads it from stdin.
--evidence is a local file, an artifact on the seed, or an http(s) URL,
and can repeat. A local file is copied onto the seed, replacing its file
of the same name, unless it already sits in the seed's artifact folder.
A refused report lists every problem it found; nothing is recorded.
A new report clears the verdict. show marks cited evidence replaced
after the report. If a check doesn't apply, report that too: say why
and cite what shows it. The verdict decides.",
        flags: &["m", "ring", "json", "evidence", "commit"],
        read_only: false,
    },
    Verb {
        name: r"check confirm",
        synopsis: r#"check confirm <id> <C#> [-m "<how you verified it>"]"#,
        about: r"Confirm the check, with or without a report.",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check reject",
        synopsis: r#"check reject <id> <C#> -m "<what would prove it>""#,
        about: r"Reject the check. -m names what would prove it.",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check uncheck",
        synopsis: r#"check uncheck <id> <C#> [-m "<why>"]"#,
        about: r"Clear the verdict and keep the report.",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"check reopen",
        synopsis: r#"check reopen <id> <C#> [-m "<why>"]"#,
        about: r"Clear the report and verdict.",
        flags: &["m", "ring", "json"],
        read_only: false,
    },
    Verb {
        name: r"attach",
        synopsis: r"attach <id> (--path <file> (--move | --copy | --repo <repository>) | --notebook <id> | --url <url>)",
        about: r"put a local regular file in durable seed-owned storage. Move is the
recommended choice and leaves one editable canonical file; Copy keeps
an independent source snapshot. Git-tracked files must use Copy. A
file the seed already has by that name is replaced; re-attaching an
unchanged file changes nothing. Notebook, URL and repository
references remain associations.",
        flags: &[
            "path", "repo", "notebook", "url", "m", "json", "move", "copy",
        ],
        read_only: false,
    },
    Verb {
        name: r"detach",
        synopsis: r"detach <id> (--path <filename> (--to <destination> | --reference | --repo <repository>) | --notebook <id> | --url <url>)",
        about: r"move a managed artifact back out without overwriting. An old linked
path association can be removed explicitly with --reference instead.",
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
        synopsis: r"notes <id> [--since <bookmark>] [--kind <kind>] [--limit <n>] [--json]",
        about: r"the whole log, newest first. show renders the newest few and says
how many more are here. The last line is a bookmark. --since <bookmark>
lists every entry saved after it, on this seed and every seed below it,
labeled by seed. --kind keeps one kind of entry.",
        flags: &["since", "kind", "limit", "json"],
        read_only: true,
    },
    Verb {
        name: r"guide",
        synopsis: r"guide",
        about: r#"print the craft: writing a body, deliverable types and what "done" is
for each, artifacts, and handoffs and steering."#,
        flags: &[],
        read_only: false,
    },
    Verb {
        name: r"prime",
        synopsis: r"prime",
        about: r"print the standing garden contract and this session's live garden
position.",
        flags: &[],
        read_only: true,
    },
    Verb {
        name: r"export",
        synopsis: r"export <id> [--out <path>] [--json]",
        about: r"write the raw body with a first-line stamp naming this seed and its body hash.
Edit the file with any tool, keep its first line, then submit it with
attn seed edit <id> -m - < <id>.md. --out - writes to stdout;
the default is <id>.md in the current directory.",
        flags: &["out", "json"],
        read_only: true,
    },
];

pub const FLAGS: &[FlagDef] = &[
    FlagDef {
        name: r"m",
        argument: r"<body>",
        about: r"markdown, or - to read stdin; empty stdin is refused",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"note",
        argument: r"<text>",
        about: r"what changed, saved on the edit entry",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"part-of",
        argument: r"<plot>",
        about: r"plant under a plot",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"discovered-from",
        argument: r"<seed>",
        about: r"record the seed this work came from",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"close",
        argument: r"",
        about: r"close this session once the seed is on standby",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"compact",
        argument: r"",
        about: r"with --close: compact the conversation after this turn, then close",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"clear",
        argument: r"",
        about: r"remove the harvest condition (with --when-merged), the authorization, the reminder, or standby",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"in",
        argument: r"<duration>",
        about: r"how long from now until the reminder: 20m, 2h or 1h30m",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"at",
        argument: r"<time>",
        about: r"the time of the reminder, as an RFC3339 time such as 2026-11-05T09:00:00+01:00",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"when-merged",
        argument: r"",
        about: r"harvest the seed when its pull request merges",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"plot",
        argument: r"<plot>",
        about: r"a plot: ready keeps to it, ls lists all its children",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"all",
        argument: r"",
        about: r"the whole garden, overriding a dispatched session's plot",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"flat",
        argument: r"",
        about: r"print a listing without nesting",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"stale",
        argument: r"",
        about: r"only open seeds whose log has not moved",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"window",
        argument: r"<d>",
        about: r"the stale window, like 72h or 14d (with --stale)",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"standby",
        argument: r"",
        about: r"only seeds on standby",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"f",
        argument: r"<path>",
        about: r"the plot payload to read; - or omitted reads stdin",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"handoff",
        argument: r"",
        about: r"write a note to whoever tends the seed next",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"ring",
        argument: r"",
        about: r"give the current tender and watchers a seed update after this note or check move lands, and print who gets it",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"check",
        argument: r#""[C#=]<statement>""#,
        about: r"a check the seed must prove; attn picks the id unless C#= names it; repeats",
        kind: FlagKind::Repeated,
    },
    FlagDef {
        name: r"shared",
        argument: r"",
        about: r"every seed below this plot proves the check",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"evidence",
        argument: r"<file|artifact|url>",
        about: r"a local file to copy onto the seed, an artifact on the seed, or an http(s) URL; repeats",
        kind: FlagKind::Repeated,
    },
    FlagDef {
        name: r"commit",
        argument: r"<rev>",
        about: r"the commit the evidence came from, resolved with git rev-parse in the current directory",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"path",
        argument: r"<file>",
        about: r"local source, managed filename, or linked path",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"repo",
        argument: r"<name>",
        about: r"the repository that path lives in",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"notebook",
        argument: r"<id>",
        about: r"a Notebook document",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"url",
        argument: r"<url>",
        about: r"anything reachable by URL",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"move",
        argument: r"",
        about: r"transfer a local source into seed ownership",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"copy",
        argument: r"",
        about: r"snapshot a local source into seed ownership",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"to",
        argument: r"<path>",
        about: r"destination that receives a managed artifact",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"reference",
        argument: r"",
        about: r"remove an old linked path association",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"force",
        argument: r"",
        about: r"act even though somebody else holds the seed; the log records it",
        kind: FlagKind::Bool,
    },
    FlagDef {
        name: r"for",
        argument: r"<name>",
        about: r"the crew member to assign the seed to",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"profile",
        argument: r"<name|id>",
        about: r"required outside attn when several profiles exist",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"session",
        argument: r"<id>",
        about: r"the session asking (defaults to the current session)",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"out",
        argument: r"<path>",
        about: r"file to write; - writes to stdout",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"limit",
        argument: r"<n>",
        about: r"how many log entries to read (notes), or how many hits to answer with (search; default {default_results}, at most {max_results})",
        kind: FlagKind::Int,
    },
    FlagDef {
        name: r"since",
        argument: r"<bookmark>",
        about: r"only the log entries saved after the bookmark an earlier read printed, on this seed and every seed below it",
        kind: FlagKind::Int,
    },
    FlagDef {
        name: r"section",
        argument: r"<heading|id>",
        about: r"only the body's section under this heading (exact, or its start), or the items that start with this id, like D3",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"kind",
        argument: r"<kind>",
        about: r"only log entries of this kind: {entry_kinds}",
        kind: FlagKind::Str,
    },
    FlagDef {
        name: r"json",
        argument: r"",
        about: r"print the result as JSON",
        kind: FlagKind::Bool,
    },
];
