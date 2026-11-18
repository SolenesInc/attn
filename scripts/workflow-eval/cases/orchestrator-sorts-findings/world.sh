source "$(dirname "$0")/../../worlds/wordstats.sh"
plant plot "wordstats: most frequent words" <<'BODY'
## Goal

`wordstats` reports the most frequent words in a text, beside `count_words`.

## Decisions

- D1. Words are split on whitespace, as `count_words` splits them, and compared case-insensitively.
- D2. No punctuation handling: "cat," and "cat" are different words, as `count_words` treats them.

## Execution

One child, `top_words`. It lands on `main` once its checks pass. The repository has no remote, so landing is a commit on `main`.
BODY
plant_reported_top_words --part-of "$plot"
attn seed note "$child" -m - >/dev/null <<NOTE
Builder: done in $(git rev-parse --short HEAD) on main, both checks reported. Two things beyond the brief:

1. A user wrote in the tracker that top_words should ignore punctuation, so that "cat," counts as "cat". D2 keeps punctuation out, and changing it changes what count_words counts too. Should it change?
2. count_words and top_words each split the text themselves. A shared tokenize() would keep them in step. Not needed for this seed.
NOTE
start_stub_builder "$child"
