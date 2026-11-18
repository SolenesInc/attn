---
name: rune-upstream-review
description:
  Use only when the weekly upstream Rune review automation starts you, or the
  user asks which upstream Rune commits are worth picking.
---

# Review upstream Rune for picks

attn's Rune fork takes upstream changes only by cherry-pick, as
[The Rune fork](../../../docs/internal/rune-fork.md) says. Each week you read
the commits upstream (`anomalyco/rune`) added to `dev` since the last review,
and ask the user which ones attn should pick. You don't pick. The Orchestrator
picks with `scripts/rune-bump --pick`.

Never open an issue, a pull request or a discussion on `anomalyco/rune`, and
never comment there. The only change you make to the fork is moving the "Last
upstream commit reviewed" line in its `FORK.md`, with
`scripts/rune-upstream reviewed`.

Your stake is the seed named in the automation's opening message. Each weekly
run comes to you on the same stake, so leave it open between runs.

## Read the new commits

1. Run `git pull --ff-only`. If this file changed, read it again.
2. Run `attn ask list`. If your ask from an earlier week is still open, close
   it with `attn ask close <id> -m "This week's list replaces it"`. The line in
   `FORK.md` moves only after the user answers, so this week's list still
   holds that ask's commits.
3. Run `scripts/rune-upstream list`. It fetches upstream's `dev` and the fork's
   `main` into the Rune clone at `~/projects/rune`. Its first line names the
   commit the list reads through. Record that commit as `<through>` in a note
   on your stake.
4. Read `FORK.md` on the fork's `main`
   (`git -C ~/projects/rune show refs/rune-upstream/fork/main:FORK.md`). Its
   "Our changes" rows tell you what attn changed in Rune and why.

## Choose the candidates

Keep a commit when it touches an area attn cares about:

- text and IME: shaping, editing, carets, selection, input methods, dead keys;
- Linux and Wayland;
- frame pacing: when frames are scheduled, presented or skipped, and idle CPU;
- the code an "Our changes" row in `FORK.md` changed, because a fix there may
  replace or break our change.

Drop the rest, such as Windows, web and wasm, and the commits the list shows
apart because they change only files `main` has deleted. To judge a commit,
read its message and diff with `git -C ~/projects/rune show <commit>`.

For each candidate, write one line on what it would give attn's native app,
in the user's terms: what they would see or stop seeing. Say whether it
applies alone, as the list shows. A commit that conflicts often builds on an
earlier upstream commit; name that commit when you find it, so the pick takes
both.

## Ask the user

Ask once, with one `:::question-multi` that lists the candidates in upstream
order:

```sh
attn ask --subject "Upstream Rune picks through <through>" <<'ASK'
I read <n> upstream Rune commits after `<last reviewed>`, through `<through>`.
<k> touch text and IME, Linux, frame pacing or our changes; the rest are
Windows, web or code the fork deleted.

:::question-multi
Which commits should attn pick?
- [ ] `<hash>` <subject> — <what it gives attn>; applies alone
- [ ] `<hash>` <subject> — <what it gives attn>; needs `<earlier hash>` first
:::
ASK
```

When nothing qualifies, skip the ask: say so in a note on your stake, and go
on to the next section as if the user had picked nothing.

End your turn. attn types the answer into this conversation.

## Act on the answer

1. Run `scripts/rune-upstream reviewed <through>`, with the commit from step 3,
   not upstream's newest. It commits the moved line to the fork's `main` and
   pushes it.
2. When the user picked commits, plant a seed for the Orchestrator with
   `attn seed plant "Pick upstream Rune <hashes>" --discovered-from <stake> -m "<body>"`.
   Write the body as a work prompt. It names each commit with its line from the
   ask, the command `scripts/rune-bump --pick <commits, oldest first>`, and
   which commits conflict. It says to port a conflicting commit on a fork branch
   as [The Rune fork](../../../docs/internal/rune-fork.md) describes.
3. Note on your stake: the range you read, the commits the user picked, the
   seed you planted, and the fork commit that moved the line.
