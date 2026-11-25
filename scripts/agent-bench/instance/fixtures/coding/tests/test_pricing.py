import unittest

from orders.pricing import discount_percent, line_total, order_total


def line(qty, price):
    return {"sku": "x", "qty": qty, "price": price}


class PricingTest(unittest.TestCase):
    def test_discounts_start_at_10_and_at_100(self):
        self.assertEqual([discount_percent(q) for q in (9, 10, 99, 100)], [0, 10, 10, 20])

    def test_a_half_cent_rounds_up(self):
        self.assertEqual(line_total(line(10, 5)), 45)
        self.assertEqual(line_total(line(15, 3)), 41)
        self.assertEqual(line_total(line(11, 5)), 50)

    def test_each_code_takes_a_dollar_off_but_never_below_zero(self):
        self.assertEqual(order_total([line(1, 500)], ["A", "B"]), 300)
        self.assertEqual(order_total([line(1, 50)], ["A"]), 0)

    def test_no_codes_means_no_discount_every_time(self):
        self.assertEqual(order_total([line(1, 500)]), 500)
        self.assertEqual(order_total([line(1, 500)]), 500)


if __name__ == "__main__":
    unittest.main()
