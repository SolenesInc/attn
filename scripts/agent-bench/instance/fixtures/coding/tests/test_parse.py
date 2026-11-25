import unittest

from orders.parse import parse_line, parse_orders


class ParseTest(unittest.TestCase):
    def test_reads_one_line(self):
        self.assertEqual(parse_line("apple,3,120"), {"sku": "apple", "qty": 3, "price": 120})

    def test_spaces_around_fields_are_ignored(self):
        self.assertEqual(parse_line(" apple , 3 , 120 "), {"sku": "apple", "qty": 3, "price": 120})

    def test_blank_lines_and_comments_are_skipped(self):
        text = "# a comment\n\napple,1,100\n   \n  # another\npear,2,50\n"
        self.assertEqual([order["sku"] for order in parse_orders(text)], ["apple", "pear"])

    def test_a_negative_quantity_is_refused_with_the_line(self):
        with self.assertRaisesRegex(ValueError, "apple,-1,100"):
            parse_line("apple,-1,100")


if __name__ == "__main__":
    unittest.main()
