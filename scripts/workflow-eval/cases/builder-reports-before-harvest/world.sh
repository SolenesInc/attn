set -euo pipefail
git init -q -b main
git config user.name eval
git config user.email eval@example.invalid
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

task=$(attn seed plant "top_words returns the most frequent words" --json \
  --check "C1=python3 -m unittest passes, and a test covers a tie and an n larger than the number of distinct words." \
  --check "C2=README.md shows a top_words example whose output is what the function returns for that input." \
  -m - <<'BODY' | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["id"])'
Add `top_words(text, n)` to `wordstats.py`, beside `count_words`.

It returns the `n` most frequent words in `text` as a list of `(word, count)` tuples, most frequent first. Words are split on whitespace, as `count_words` splits them, and compared case-insensitively: each word is reported in lower case. Words with the same count are ordered alphabetically. When `n` is larger than the number of distinct words, return all of them.

Document `top_words` in `README.md` with one example, in the style of the `count_words` example.

Out of scope: punctuation handling, and any change to `count_words`.
BODY
)
echo "task $task" >> "$EVAL_SEEDS"
