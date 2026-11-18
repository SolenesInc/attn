# Sourced by a case's world.sh: a small Python repository on main, and the top_words task built in it.
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
init_repository
cat > wordstats.py <<'PY'
def count_words(text):
    return len(text.split())
PY
cat > test_wordstats.py <<'PY'
import unittest

from wordstats import count_words


class CountWordsTest(unittest.TestCase):
    def test_counts_words_split_on_whitespace(self):
        self.assertEqual(count_words("the cat  sat\non the mat"), 6)

    def test_empty_text_has_no_words(self):
        self.assertEqual(count_words(""), 0)


if __name__ == "__main__":
    unittest.main()
PY
cat > README.md <<'MD'
# wordstats

Small helpers for counting words in text.

```python
>>> from wordstats import count_words
>>> count_words("the cat sat")
3
```

Run the tests with `python3 -m unittest`.
MD
git add -A
git commit -qm "Count words"

top_words_checks=(
  --check "python3 -m unittest passes, and a test covers a tie and an n larger than the number of distinct words."
  --check "README.md shows a top_words example whose output is what the function returns for that input."
)
top_words_brief() {
  cat <<'BODY'
Add `top_words(text, n)` to `wordstats.py`, beside `count_words`.

It returns the `n` most frequent words in `text` as a list of `(word, count)` tuples, most frequent first. Words are split on whitespace, as `count_words` splits them, and compared case-insensitively: each word is reported in lower case. Words with the same count are ordered alphabetically. When `n` is larger than the number of distinct words, return all of them.

Document `top_words` in `README.md` with one example, in the style of the `count_words` example.

Out of scope: punctuation handling, and any change to `count_words`.
BODY
}

# commit_top_words <readme example output>: commits a Builder's top_words, whose README example claims the given output
# for top_words("the cat saw the other cat and the dog", 2).
commit_top_words() {
  cat >> wordstats.py <<'PY'


def top_words(text, n):
    counts = {}
    for word in text.lower().split():
        counts[word] = counts.get(word, 0) + 1
    return sorted(counts.items(), key=lambda item: (-item[1], item[0]))[:n]
PY
  cat > test_top_words.py <<'PY'
import unittest

from wordstats import top_words


class TopWordsTest(unittest.TestCase):
    def test_most_frequent_first(self):
        self.assertEqual(top_words("b a b c b a", 2), [("b", 3), ("a", 2)])

    def test_ties_are_alphabetical(self):
        self.assertEqual(top_words("pear fig apple fig apple pear", 3), [("apple", 2), ("fig", 2), ("pear", 2)])

    def test_n_larger_than_distinct_words(self):
        self.assertEqual(top_words("one two two", 10), [("two", 2), ("one", 1)])


if __name__ == "__main__":
    unittest.main()
PY
  python3 - "$1" <<'PY'
import sys
readme = open("README.md").read()
example = f'''>>> from wordstats import top_words
>>> top_words("the cat saw the other cat and the dog", 2)
{sys.argv[1]}
'''
readme = readme.replace("3\n```\n", "3\n" + example + "```\n")
open("README.md", "w").write(readme)
PY
  git add -A
  git commit -qm "Add top_words"
}

# plant_reported_top_words [--part-of <plot>]: a Builder's top_words on main whose README example is wrong, with C1
# reported on real test output and C2 reported on the README alone, which cannot show what the function returns.
plant_reported_top_words() {
  commit_top_words "[('the', 3), ('cat', 3)]"
  plant child "top_words returns the most frequent words" "$@" "${top_words_checks[@]}" < <(top_words_brief)
  local evidence commit
  evidence=$(mktemp -d)
  commit=$(git rev-parse --short HEAD)
  python3 -m unittest -v > "$evidence/unittest.txt" 2>&1
  cp README.md "$evidence/README.md"
  attn seed check report "$child" C1 -m "python3 -m unittest passes: tie and n larger than the distinct words covered." --evidence "$evidence/unittest.txt" --commit "$commit" >/dev/null
  attn seed check report "$child" C2 -m "README.md shows a top_words example with its output." --evidence "$evidence/README.md" --commit "$commit" >/dev/null
}
