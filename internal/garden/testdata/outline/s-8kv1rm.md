Let agents learn what changed on a seed or across a plot from one read that returns only that, and make every change to a seed leave a line in its log. Today agents re-read whole seeds after every bell (55% of `show` calls repeat a read the same session already made), poll plots seed by seed (one 9-child plot costs 21 calls and about 178 KB), and cannot see a new blocker or a park at all, because those changes write no log entry. Done when `attn seed show <id> --since <bookmark>` returns exactly what was saved after the bookmark, for one seed or a plot's whole tree; bells and delegation openings hand over that command; a plot's first read is a board of its open children; a child can read one part of its parent; and plot payloads take slugs, `part_of`, `discovered_from` and `after`. Out of scope: the daemon-to-app push (garden-push-sends-only-what-changed, s-k343pj), check ids and evidence (checks-without-coordination-copies, s-m3c7ve), tuning guidance prose (step 4 of s-bhb3t4), and measuring the result (harbor-eval-suite-seed-commands, s-7p2kya).

The design came from read-only-what-changed-seed-plot (s-6chqf8). Its notes hold the user's decisions, and its attachments hold the CLI prototype (bookmark-prototype.patch, bookmark-prototype-run.sh, bookmark-prototype-run.txt). The prototype rendered in the CLI from several reads; use it for the layout, not as code to build on (F5).

## Terms

- log entry: one line of a seed's log, stored as a document in the garden notes collection. Kinds: note, handoff, attach, detach, check, edit, and, new in this plan, move, link and answer. `attn seed notes` lists them.
- save number: the position the daemon gives every committed write in the event log (`bus_events.seq`), in commit order.
- bookmark: the save number a read prints on its last line. `--since <bookmark>` returns what was saved after it.
- since-read: a `show` or `notes` call with `--since`.
- board: a plot's list of its open children in `show`.
- outline: a body's `##` headings, each with its size and the D, F and C ids it holds.

## Decisions

- D1. The bookmark is a save number. A read takes the event log head H before it reads any document and prints H as its bookmark. `--since N` returns the entries saved in (N, H]. Nothing is skipped, because save numbers follow commit order (F1).
- D2. Every change to a seed writes one log entry in the same commit as the change. Each event in the garden event catalog declares the entry it writes, or why it writes none (F3, F4). Moves write `move` entries. Links and unlinks write a `link` entry on both seeds, each worded from its own side. A harvest that unblocks a seed writes a `move` entry on that seed ("ready: s-X harvested"). A park comment and a claim-takeover audit fold into the move's entry; the plot-closed note stays its own entry on the plot.
- D3. The daemon stores no per-reader read state. The bookmark lives in the reader's output, so a command gives the same answer whoever runs it, after compaction, and from a native subagent that shares a session id. Reading writes no garden document or event, which s-k343pj needs. `show` and `notes` still mark the reader's bell read (`consumeSeedBell`).
- D4. A since-read of a plot covers the plot and every seed below it, each entry labeled with its seed. The board lists direct children; a child that is itself a plot shows its own progress.
- D5. The first read leads with news, in this order: the header with the bookmark; the newest 5 entries, newest first; a one-line pointer when the freshest handoff is older than those entries; edges, with discovered-from folded to a count and its open seeds; the board; checks and artifacts; the body last. A since-read never prints the body; an `edit` entry says the body changed.
- D6. The board shows each open child's id, slug, state, tender, the seeds it waits for, its check tally and its newest entry. Closed children fold into one line that points at `attn seed ls --plot <id>`, which lists every child of the plot, closed ones included.
- D7. Bells hand over the command: "s-X moved: <change> — read what changed with `attn seed show s-X --since N`", where N is the save number just before the first event the bell covers. The delegation opening's first sentence drops "including its notes", because the first read already prints the newest entries and the bookmark.
- D8. A child's `show` prints its parent's outline, and `--section <heading|D3>` prints one part of a body.
- D9. Ask answers are written as kind `answer`, and existing answers are converted once, so `notes --kind answer` finds them.
- D10. A slug works wherever a seed id does. It resolves within the caller's profile. A slug that names more than one seed is refused, and the refusal lists each match.
- D11. A plot payload takes `part_of` and `discovered_from` on the plot, and `after` on each child: the siblings it waits for. A payload with `blocks` is refused, and the error names `after`.
- D12. When a bookmark is older than the event log keeps (30 days by default), a since-read prints the full read with a line that names the bookmark, the earliest kept save number and the retention window.

The user confirmed D10 and D11 on 2026-10-30, and approved building after seeing D9's one-time conversion, which is preferred over matching the answer text at read time.

## Tradeoffs accepted

- D2. We accept about 5,200 more log entries a month in exchange for "what changed" being one query over entries. Measured on the copy over 30 days: 1,894 moves, 1,576 links and unlinks (3,152 entries), 161 unblocks, next to 13,774 notes.
- D2. We accept two entries for a link inside a plot, one on each end, in exchange for each seed's own read being complete.
- D2. We accept that `ls --stale` counts moves as activity, so a seed tended or parked in the window is no longer stale (F10).
- D1. We accept that an entry saved while a full read runs can print in that read and again in the next since-read, in exchange for no multi-read transaction. It is never missed.
- D12. We accept a full read for bookmarks older than 30 days, in exchange for not adding a save-number column to every document table.
- D11. We accept that agents writing `blocks` from habit get one refusal, in exchange for one direction word in plot payloads.

## Findings

Read at origin/main bc7e0ce4f. The prototype ran on a copy of the production garden in a non-production instance.

- F1. Every seed change is already a typed event. `events.Declarations` (internal/garden/events/catalog.go) names planted, tended, parked, harvested, withered, replanted, body edited, note added, artifact changed, edge linked and unlinked, harvest-when configured and cleared, unblocked and work ready. Each is committed in the same transaction as its document write (`Store.CommitDocumentWritesWithEvents`), with the seed id as subject and an AUTOINCREMENT `bus_events.seq`, so save numbers follow commit order. Reads already report `AsOfSeq`. The design reads changes through these events (D1). This rules out times as the bookmark: `gardenTime()` is taken before the commit (`writeSeedMoveWithNotesProtected`), and no lock spans both, so two concurrent saves can commit out of time order (inferred from the code).
- F2. The event log keeps 30 days (`bus.DefaultRetention`). The copy holds save numbers 1,551,034 to 2,711,491, with garden events from 2026-09-30 to 2026-10-30. Document rows store no save number (`doc_N`: id, body, rev, created_at, updated_at). A since-read must therefore detect a bookmark older than the log (D12). The log's own order stays `created_at`.
- F3. Moves and links write events but no entry. A plain move goes through `writeSeedWithEventsProtected`; only a park comment, a claim-takeover audit and the plot-closed note write entries (`applySeedTransitionDetailedAsAtRevisionProtected`). `handleSeedLink` stores the edge on one seed (`next`) and makes that seed the event's subject, so the other end hears nothing. Prototype run: after `link s-37yjkj blocks s-evwt1k` and `park s-m3c7ve`, a since-read printed "0 new entries", showed the park only as a changed state, and never showed the blocker. Edit entries (internal/daemon/garden_body.go) are committed with `BodyEdited` and no `NoteAdded`. D2 therefore needs every entry committed with a `NoteAdded` event that names it, so one query finds every entry.
- F4. `artifact.changed` also fires when the artifact folder watcher sees a file change: 8,863 events in 30 days on the copy. Logging those would flood the log. Attach and detach already write entries; observed folder changes write none.
- F5. The prototype measured, on the same data: bell after one note, `show` 9,013 B and a since-read 358 B; "anything new on plot s-bhb3t4?" 23 calls and 263,651 B, then 1 call and 804 B; a child reading F3 of its parent 13,107 B, then 621 B with `--section`. The first 2 KB of `show s-2mgjx9` held a 6-week-old handoff before, and the header and newest entries after. On that plot (more than 30 children, 15 discovered-from edges), edge and child rows pushed the news out of the first 2 KB until the entries moved up and closed children and discovered-from edges folded (D5, D6). The prototype read seed by seed in the CLI, so a write between two reads could fall behind its bookmark; the daemon must take the bookmark itself (D1).
- F6. `SeedShowResult` already returns the newest 5 entries, their total and relations in both directions. The CLI prints the freshest handoff first and the entries last (`runSeedShow`, cmd/attn/seed.go), so the reorder is mostly CLI work.
- F7. Bells coalesce: `inbox_items` keeps one unread item per address, kind and `coalesce_key`, and stores the change name, not a position. The text is rendered at delivery (`mailboxItemContent` and content/session/garden-update.md). D7 needs a position stored on the item when it is created.
- F8. Slugs are refused: `garden.ValidateID` runs in `readSeed`, `handleSeedLink` and `handleSeedReady`. `PlotSpec` has title, body, checks and children; `PlotChildSpec` has `blocks` only, and the seed guide says `blocks` "names the siblings that wait for it". The transcript study found `blocks` written backwards and plots fixed up afterwards with `link` (findings-plots.md on s-bhb3t4).
- F9. Search (s-qymnpd) indexes every notes-collection document at the store level (internal/store/document_text.go), so new entries are searchable with no extra work.
- F10. Other readers of the log see the new entries: `ls --stale` (`newestNoteAt`), search's last active, the app's seed view (`SeedDocumentView` renders every kind as a list item with `data-kind`), the garden review (internal/daemon/garden_review.go) and `tend`'s freshest handoff.

## Design

Ownership:

    internal/garden/events/catalog.go   each event declares its log entry, or why it writes none
    internal/garden/entries.go          renders an event's entry for each seed it lands on (new)
    internal/garden/lifecycle.go        note kinds move, link, answer; --kind parsing
    internal/garden/outline.go          Outline(body) and Section(body, ref) (new)
    internal/garden/plot.go             PlotSpec part_of, discovered_from; PlotChildSpec after
    internal/garden/garden.go           slug resolution beside ValidateID
    internal/store/bus.go               BusEventsAbout: events by name and subjects in (after, through]
    internal/store/ (inbox, migration)  inbox_items.after_seq
    internal/daemon/garden.go           entries in every seed write; since-reads; board; ls plot; slugs in readSeed
    internal/daemon/garden_body.go      edit entry committed with NoteAdded
    internal/daemon/asks.go             answers written as kind answer; one-time conversion
    internal/daemon/garden_events.go    a new seed bell records after_seq
    internal/daemon/agent_inbox.go      renders --since in the bell
    internal/protocol/schema/main.tsp   since, bookmark, board, parent outline, list plot, plot payload
    cmd/attn/seed.go                    first-read layout, --since, --section, notes --since/--kind, ls --plot
    internal/prompts/content/           session/garden-update.md, delegation/seed.md (first sentence), garden.md,
                                        resources/seed-guide.md, skills/attn/references/garden.md, delegated-agent.md
    docs/inbox.md, docs/glossary.md     bell text; bookmark, save number, log entry

Interfaces. Call sites first:

    $ attn seed show s-bhb3t4
    s-bhb3t4  Make the Garden easy for agents to read and write
    ...
    bookmark  attn seed show s-bhb3t4 --since 2711491

    $ attn seed show s-bhb3t4 --since 2711491
    since 2711491: 3 new entries on 2 of 9 children
      16:05  af8e07b8  s-m3c7ve  note  C81 landed as 4c1e2a9
      16:02  b8ecdba2  s-m3c7ve  move  parked: waiting on review
      16:02  b8ecdba2  s-evwt1k  link  now waits for s-37yjkj
    bookmark  attn seed show s-bhb3t4 --since 2711530

    $ attn seed notes s-72djve --kind answer
    $ attn seed ls --plot s-bhb3t4
    $ attn seed show s-bhb3t4 --section F3

Wire:

```diff
 model SeedShowMessage {
   "seed_id": string;
+  "since"?: int64;
 }
 model SeedShowResult {
+  "bookmark": int64;
+  "since"?: SeedSince;
+  "board"?: SeedBoardRow[];
+  "closed_children"?: SeedClosedCount;
+  "parent_outline"?: SeedOutlinePart[];
 }
+model SeedSince { "after": int64; "entries": SeedNote[]; "expired"?: SeedSinceExpired; }
+model SeedSinceExpired { "earliest": int64; "retention_seconds": int64; }
+model SeedBoardRow { "seed": Seed; "waits_for": string[]; "newest"?: SeedNote; }
+model SeedOutlinePart { "heading": string; "bytes": int32; "ids": string[]; }
 model SeedNote {
+  "seq"?: int64;   // set on entries a since-read returns
 }
 model SeedNotesMessage {
+  "since"?: int64;
+  "kind"?: string;
 }
 model SeedNotesResult {
+  "bookmark": int64;
+  "expired"?: SeedSinceExpired;
 }
 model SeedListMessage {
+  "plot"?: string;
 }
```

Plot payload:

```diff
 type PlotSpec struct {
     Title    string          `json:"title"`
     Body     string          `json:"body,omitempty"`
     Checks   []Check         `json:"checks,omitempty"`
     Children []PlotChildSpec `json:"children"`
+    PartOf         string    `json:"part_of,omitempty"`
+    DiscoveredFrom string    `json:"discovered_from,omitempty"`
 }
 type PlotChildSpec struct {
     Title  string   `json:"title"`
     Body   string   `json:"body,omitempty"`
     Checks []Check  `json:"checks,omitempty"`
-    Blocks []string `json:"blocks,omitempty"`
+    After  []string `json:"after,omitempty"`
 }
```

Store and garden:

```go
// BusEventsAbout returns events named name whose subject is in subjects, with after < seq <= through, in seq order.
func (s *Store) BusEventsAbout(name string, subjects []string, after, through int64) ([]BusEvent, error)

type EntryRule struct {
    Kind string    // move or link
    Ends EntryEnds // the subject, both ends of an edge, or the unblocked seed
    None string    // set instead of Kind: why this event writes no entry
}
func EntryRuleFor(eventName string) EntryRule
func Outline(body string) []OutlinePart
func Section(body, ref string) (string, error) // ref: a heading prefix or an id like D3; the error lists the parts
```

New wire fields: edit main.tsp, run `make generate-types`, and commit the generated files and schema hash. `inbox_items.after_seq` is a schema change: follow docs/db-migrations.md.

Invariants:

- I1. Every log entry is committed with exactly one `NoteAdded` event naming it. One daemon function writes all entries, and C120 tests it.
- I2. Every catalog event has an entry rule. `events.BuildGardenModel` refuses an event without one (C121).
- I3. A since-read covers (N, H], with H read before any document (C123).
- I4. Reading writes no garden document or event. C123 asserts the event log head is unchanged by a read.

Behavior. A since-read:

    agent  -> CLI:    attn seed show P --since N
    CLI    -> daemon: SeedShowMessage{seed_id: P, since: N}
    daemon:           earliest, H := BusBounds()
                      N < earliest-1  -> full read with the expired line (D12)
                      tree := P and its descendants (part-of edges, readGarden)
                      events := BusEventsAbout("garden.seed.note.added", tree, N, H)
                      entries := the note documents those events name
    daemon -> CLI:    SeedShowResult{bookmark: H, since: {after: N, entries}, board, ...}
    CLI    -> agent:  header, since line, entries newest first, bookmark line

A seed change:

```text
writeSeedChange(seed, occurrences, entries the caller wrote)
  for each occurrence:
    rule := garden.EntryRuleFor(name)
    skip when rule.None is set, or a caller entry already records it (park comment, audit, harvest-when)
    entries += garden.RenderEntry(occurrence, rule)     one per seed that rule.Ends names
  commit: seed document + entries + occurrences + one NoteAdded per entry
          (NoteAdded rings only when the entry asked for attention)
```

State:

    bookmark (the reader's output; the daemon stores nothing)
      set by:     every show or notes read: the event log head before any document is read
      read by:    the next --since
      on restart: still valid; save numbers persist
      too old:    older than the event log keeps -> full read with the expired line (D12)

    log entries (garden notes collection)
      set by:     the commit that makes the change
      on failure: the change and its entries commit together or not at all
      twice:      a converged move writes nothing (s-37yjkj), so a retry adds no entry

    bell position (inbox_items.after_seq)
      set by:     creating a seed-update bell: the save number just before the event that rang
      coalesced:  a later event keeps the existing value
      old rows:   0, and the bell renders without --since

    answer kind (internal/daemon/asks.go)
      set by:     writing an answer; a one-time conversion of authorless notes that start "🙋 The user answered"
      twice:      a second conversion finds none

## Execution

Six children. Each lands on main with scripts/land.sh as docs/trunk-based-development.md describes; there are no pull requests.

- Start in parallel: every-seed-change-writes-log-entry (s-5p8jrh; D2, D9), show-notes-read-since-bookmark (s-h5rsb7; D1, D3, D4, D12) and plot-payloads-take-slugs-part-after (s-zx00jk; D10, D11).
- After the since-read child: first-read-leads-news-plots-show (s-43bhac; D5, D6) and bells-openings-hand-over-bookmark (s-3h472p; D7).
- After the first-read child, because both rewrite `runSeedShow`: children-read-their-parent-s-outline (s-sa03z4; D8).

How this fits the sibling plans under s-bhb3t4. checks-without-coordination-copies (s-m3c7ve) also changes cmd/attn/seed.go, main.tsp and delegation/seed.md: its child openings-list-checks-report (s-vmgjwy) owns the second sentence of delegation/seed.md, and bells-openings-hand-over-bookmark owns the first. scripts/land.sh regenerates protocol types when only they conflict. The closing plan (s-37yjkj, landed) wrote the move paths this plan extends; keep its converged-move rule and its plot-closed note. The search plan (s-qymnpd, landed) indexes entries automatically (F9) and prints similar seeds after plant and plot; keep that output. The garden push (s-k343pj) holds because reads write nothing (D3).

## Completion

The checks are on the seeds. C135 is shared: each child proves it with its own report. C134 runs on this plot after every child lands, on a fresh copy of the production garden; its script and output attach to the plot.

## Alternatives rejected

- D3. The daemon remembers what each session read: it breaks after compaction and when a native subagent reads under the same session id, and the same command answers differently by caller.
- D3. No "seen" at all, only a reordered show: agents still re-read whole seeds after every bell.
- D1. A time as the bookmark: it can skip a save made at the same moment (F1), and printed in UTC beside local times it reads an hour off.
- D1. An opaque token: the same number with encoding and decoding added, for nothing needed today.
- D2. Reading changes straight from events, without entries: events expire after 30 days (F2), so a seed's log would lose its moves as it ages, and events carry no reason or comment.
- D2. Comparing `state_changed_at` to find children that moved, as the prototype did: it cannot see a new edge.
- D6. `show <id> --closed`: the user chose `ls --plot`, the command agents already try.
- D12. A save-number column on every document table: a store-wide migration to serve bookmarks older than 30 days, which a full read already serves.
- F4. An entry for every artifact folder change: 8,863 a month, all noise.
