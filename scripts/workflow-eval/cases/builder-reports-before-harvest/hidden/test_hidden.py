import unittest

from wordstats import top_words


class TopWordsHiddenTest(unittest.TestCase):
    def test_most_frequent_first(self):
        self.assertEqual(top_words("b a b c b a", 2), [("b", 3), ("a", 2)])

    def test_ties_are_alphabetical(self):
        self.assertEqual(top_words("pear fig apple fig apple pear", 3), [("apple", 2), ("fig", 2), ("pear", 2)])

    def test_case_insensitive_and_lowercased(self):
        self.assertEqual(top_words("The the THE cat", 1), [("the", 3)])

    def test_n_larger_than_distinct_words(self):
        self.assertEqual(top_words("one two two", 10), [("two", 2), ("one", 1)])


if __name__ == "__main__":
    unittest.main()
