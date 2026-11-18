# Sourced after wordstats.sh: a cut plan with two independent children, each a few hundred lines in its own files.
plant plot "wordstats: a command line and an HTML report" <<'BODY'
## Goal

`wordstats` gets two ways to use it beside the library: a command line, `wordstats_cli.py`, and an HTML report, `wordstats_report.py`. Each reads text files and works from the word statistics below.

## Decisions

- D1. Words are split on whitespace, as `count_words` splits them, and compared case-insensitively. Words are reported in lower case.
- D2. Ties are broken alphabetically, so every output is deterministic and testable byte for byte.
- D3. No punctuation handling: "cat," and "cat" are different words.
- D4. Standard library only. The tool runs where Python 3.11 runs, with no install step.
- D5. Each child owns its own module, test file and section of `docs/`, so the two can be built at the same time without touching each other's files. Neither changes `wordstats.py`.

## Design

`wordstats_cli.py` (child 1): `python3 wordstats_cli.py count|top|longest [--n N] [--json] [FILE...]`. Reads the files, or standard input when none are given. `count` prints the word count per file and a total; `top` prints the N most frequent words with their counts (default 10); `longest` prints the longest word, the alphabetically first among equals. `--json` prints the same as one JSON object. A missing or unreadable file prints `wordstats: <path>: <reason>` to standard error and exits 2; the other files are still counted. Tests in `test_wordstats_cli.py` run the script as a subprocess. Documented in `docs/cli.md`.

`wordstats_report.py` (child 2): `python3 wordstats_report.py OUT.html FILE...` writes one self-contained HTML page: per file, the word count, a table of the 20 most frequent words, and an inline SVG bar chart of them. Every word is HTML-escaped. The page has no scripts and no external resources. Tests in `test_wordstats_report.py` parse the output with `html.parser` and check the table, the bars and the escaping. Documented in `docs/report.md`.

## Execution

The two children are independent and can be built in parallel. Each lands on `main` once its checks pass. The repository has no remote, so landing is a commit on `main`.

## Completion

Both children harvested with their checks confirmed.
BODY
plant child1 "Command line for word statistics" --part-of "$plot" \
  --check "python3 -m unittest passes, with subprocess tests for each command, --json, standard input, and a missing file among readable ones (exit 2, others still counted)." \
  --check "docs/cli.md shows each command with output that the script prints for that input." <<'BODY'
Build `wordstats_cli.py` as the plot's Design (child 1) and D1–D5 say. Read them with `attn seed show <plot> --section Design` and `--section Decisions`.

Out of scope: the HTML report, and any change to `wordstats.py`.
BODY
plant child2 "HTML report of word statistics" --part-of "$plot" \
  --check "python3 -m unittest passes, with tests that parse the page and check the table, the SVG bars, and that a word like <b>&amp; is escaped." \
  --check "docs/report.md shows the command and describes each part of the page the script writes." <<'BODY'
Build `wordstats_report.py` as the plot's Design (child 2) and D1–D5 say. Read them with `attn seed show <plot> --section Design` and `--section Decisions`.

Out of scope: the command line, and any change to `wordstats.py`.
BODY
