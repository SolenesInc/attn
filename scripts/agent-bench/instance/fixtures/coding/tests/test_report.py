import unittest

from orders.parse import parse_orders
from orders.report import report


class ReportTest(unittest.TestCase):
    def test_lists_the_largest_total_first_then_by_sku(self):
        text = "pear,1,200\napple,1,200\nfig,1,900\n"
        self.assertEqual(report(parse_orders(text)), "fig 9.00\napple 2.00\npear 2.00\ntotal 13.00")

    def test_lines_of_one_sku_add_up(self):
        text = "apple,1,200\npear,1,300\napple,2,200\n"
        self.assertEqual(report(parse_orders(text)), "apple 6.00\npear 3.00\ntotal 9.00")

    def test_an_empty_order_has_a_zero_total(self):
        self.assertEqual(report([]), "total 0.00")


if __name__ == "__main__":
    unittest.main()
