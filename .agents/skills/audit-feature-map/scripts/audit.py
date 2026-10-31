#!/usr/bin/env python3
import argparse
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
FEATURES = ROOT / ".agents/skills/drive-attn/features"
MAP_PROSE = re.compile(r"^\.agents/skills/drive-attn/(features/[^/]+\.md|SKILL\.md)$")
OUTCOMES = {"pass", "fixed", "drift", "regression", "harness", "blocked", "ambiguous"}
NEEDS_FOLLOW_UP = {"fixed", "drift", "regression", "harness", "ambiguous"}
COVERED = {"pass", "fixed"}
COMMIT = re.compile(r"\b[0-9a-f]{7,40}\b")
HEADER = "| door | way in | outcome | evidence | follow-up |"


def feature_files(selected):
    files = sorted(p for p in FEATURES.glob("*.md") if p.name != "README.md")
    if not selected:
        return files
    known = {p.stem: p for p in files}
    unknown = [f for f in selected if f not in known]
    if unknown:
        sys.exit(f"unknown feature ids: {', '.join(unknown)}; known: {', '.join(known)}")
    return [known[f] for f in selected]


def entry_section(path):
    text = path.read_text()
    match = re.search(r"^## How to get to it\n(.*?)^## ", text, re.M | re.S)
    if not match:
        sys.exit(f"{path.name}: no '## How to get to it' section")
    return match.group(1)


def doors(path):
    audience, count, found = None, 0, []
    for line in entry_section(path).splitlines():
        heading = re.match(r"^### (\w+)", line)
        if heading:
            audience, count = heading.group(1).lower(), 0
        elif line.startswith("- ") and audience:
            count += 1
            found.append((f"{path.stem}.{audience}.{count}", line[2:].strip()))
    return found


def summary(text, width=90):
    text = text.replace("|", "\\|")
    return text if len(text) <= width else text[: width - 1] + "…"


def cmd_doors(args):
    print(HEADER)
    print("| --- | --- | --- | --- | --- |")
    for path in feature_files(args.features):
        for door, text in doors(path):
            print(f"| {door} | {summary(text)} |  |  |  |")


def report_rows(report):
    rows = {}
    for line in Path(report).read_text().splitlines():
        cells = [c.strip() for c in re.split(r"(?<!\\)\|", line.strip())[1:-1]]
        if len(cells) != 5 or cells[0] in ("door", "---"):
            continue
        rows.setdefault(cells[0], []).append(cells)
    return rows


def cmd_check(args):
    rows = report_rows(args.report)
    expected = [door for path in feature_files(args.features) for door, _ in doors(path)]
    problems = []
    for door in expected:
        found = rows.get(door, [])
        if len(found) != 1:
            problems.append(f"{door}: {len(found)} rows, want 1")
            continue
        _, _, outcome, evidence, follow_up = found[0]
        if outcome not in OUTCOMES:
            problems.append(f"{door}: outcome {outcome!r} is not one of {', '.join(sorted(OUTCOMES))}")
        if not evidence:
            problems.append(f"{door}: no evidence")
        if outcome in NEEDS_FOLLOW_UP and not follow_up:
            problems.append(f"{door}: outcome {outcome} needs a follow-up (commit, seed or ask)")
        unlanded = [c for c in cited_commits(evidence + " " + follow_up) if not on_main(c)]
        if unlanded:
            problems.append(f"{door}: cites {', '.join(unlanded)}, not on origin/main; cite the landed commit, or none when the fix did not land")
        if outcome == "fixed" and not cited_commits(follow_up):
            problems.append(f"{door}: outcome fixed needs the landed map commit in its follow-up")
    extra = sorted(set(rows) - set(expected))
    problems += [f"{door}: not a door in the map at this revision" for door in extra]
    tally = {}
    for door in expected:
        for row in rows.get(door, [])[:1]:
            tally[row[2]] = tally.get(row[2], 0) + 1
    print(f"{len(expected)} doors; " + ", ".join(f"{k} {v}" for k, v in sorted(tally.items())))
    if problems:
        print("\n".join(problems), file=sys.stderr)
        sys.exit(1)


def git(*args):
    return subprocess.check_output(["git", "-C", str(ROOT), *args], text=True).rstrip("\n")


def cited_commits(text):
    return [t for t in COMMIT.findall(text) if subprocess.run(["git", "-C", str(ROOT), "cat-file", "-e", f"{t}^{{commit}}"], capture_output=True).returncode == 0]


def on_main(commit):
    return subprocess.run(["git", "-C", str(ROOT), "merge-base", "--is-ancestor", commit, "origin/main"], capture_output=True).returncode == 0


def cmd_relink(args):
    landed = {}
    for line in git("log", "--format=%h %s", f"{args.since}..origin/main").splitlines():
        sha, subject = line.split(" ", 1)
        landed.setdefault(subject, []).append(sha)
    report = Path(args.report)
    text = report.read_text()
    moved = {}
    for commit in sorted(set(cited_commits(text))):
        if on_main(commit):
            continue
        matches = landed.get(git("log", "-1", "--format=%s", commit), [])
        if len(matches) != 1:
            sys.exit(f"{commit}: {len(matches)} commits in {args.since}..origin/main share its subject, want 1; cite the landed commit by hand")
        moved[commit] = matches[0]
    for old, new in moved.items():
        text = re.sub(rf"\b{old}\b", new, text)
    report.write_text(text)
    print(f"relinked {len(moved)} commits: " + ", ".join(f"{o}->{n}" for o, n in moved.items()))


def cmd_gaps(args):
    rows = report_rows(args.report)
    for path in feature_files(args.features):
        for door, _ in doors(path):
            for _, _, outcome, evidence, follow_up in rows.get(door, [])[:1]:
                if outcome not in COVERED:
                    print(f"- {door}: {outcome}. {evidence}" + (f" Follow-up: {follow_up}" if follow_up.strip("—- ") else ""))


def cmd_sources(args):
    for path in feature_files(args.features):
        rel = path.relative_to(ROOT).as_posix()
        described = git("log", "-1", "--format=%h", "--", rel)
        named = sorted({p.rstrip("/") for p in re.findall(r"`([\w.@-]+(?:/[\w.@-]+)+/?)`", entry_section(path)) if (ROOT / p).exists()})
        changes = git("log", "--format=  %h %s", f"{described}..HEAD", "--", *named) if named else ""
        print(f"{path.stem}: described at {described}; {len(named)} named sources; {len(changes.splitlines())} commits since")
        if changes:
            print(changes)


def cmd_scope(args):
    changed = set(git("diff", "--name-only", "--no-renames", f"{args.base}...HEAD").splitlines())
    changed |= set(git("diff", "--name-only", "--no-renames", "HEAD").splitlines())
    changed |= set(git("ls-files", "--others", "--exclude-standard").splitlines())
    changed.discard("")
    if not changed:
        sys.exit(f"nothing to land: no paths changed in {ROOT} since {args.base}")
    outside = sorted(p for p in changed if not MAP_PROSE.match(p))
    if outside:
        print(f"refusing: map repairs change only .agents/skills/drive-attn/features/*.md and .agents/skills/drive-attn/SKILL.md; {len(outside)} other paths changed since {args.base}:", file=sys.stderr)
        print("\n".join(f"  {p}" for p in outside), file=sys.stderr)
        sys.exit(1)
    print(f"map-only: {len(changed)} paths changed in {ROOT} since {args.base}, all map prose")


parser = argparse.ArgumentParser(description="Helpers for the feature-map audit.")
sub = parser.add_subparsers(dest="command", required=True)
for name, func, helptext in [
    ("doors", cmd_doors, "print a report table with one row per door"),
    ("check", cmd_check, "check a report has one valid row per door"),
    ("gaps", cmd_gaps, "list each door that is not pass or fixed, with its evidence and follow-up"),
    ("sources", cmd_sources, "list commits to each feature's named sources since its file last changed"),
]:
    p = sub.add_parser(name, help=helptext)
    if name in ("check", "gaps"):
        p.add_argument("report")
    p.add_argument("--features", type=lambda s: s.split(","), default=[], help="comma-separated feature ids; default all")
    p.set_defaults(func=func)
p = sub.add_parser("relink", help="replace commits that landing rebased with the landed commit of the same subject")
p.add_argument("report")
p.add_argument("since", help="origin/main before landing, such as the pass's revision")
p.set_defaults(func=cmd_relink)
p = sub.add_parser("scope", help="refuse when anything but map prose changed since BASE")
p.add_argument("base")
p.set_defaults(func=cmd_scope)
args = parser.parse_args()
args.func(args)
