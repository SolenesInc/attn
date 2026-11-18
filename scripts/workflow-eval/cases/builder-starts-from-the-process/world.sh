source "$(dirname "$0")/../../worlds/wordstats.sh"
plant task "top_words returns the most frequent words" "${top_words_checks[@]}" < <(top_words_brief)
