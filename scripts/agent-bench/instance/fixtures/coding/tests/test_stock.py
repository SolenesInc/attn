import unittest

from orders.stock import OutOfStock, Stock


class StockTest(unittest.TestCase):
    def test_takes_what_is_ordered(self):
        stock = Stock({"apple": 5})
        stock.reserve([{"sku": "apple", "qty": 2}])
        self.assertEqual(stock.levels, {"apple": 3})

    def test_a_refused_reservation_takes_nothing(self):
        stock = Stock({"apple": 5, "pear": 1})
        with self.assertRaises(OutOfStock):
            stock.reserve([{"sku": "apple", "qty": 2}, {"sku": "pear", "qty": 2}])
        self.assertEqual(stock.levels, {"apple": 5, "pear": 1})

    def test_two_lines_of_one_sku_count_together(self):
        stock = Stock({"apple": 3})
        with self.assertRaises(OutOfStock):
            stock.reserve([{"sku": "apple", "qty": 2}, {"sku": "apple", "qty": 2}])
        self.assertEqual(stock.levels, {"apple": 3})

    def test_an_unknown_sku_is_out_of_stock(self):
        with self.assertRaises(OutOfStock):
            Stock({}).reserve([{"sku": "kiwi", "qty": 1}])

    def test_the_callers_levels_are_left_alone(self):
        levels = {"apple": 5}
        Stock(levels).reserve([{"sku": "apple", "qty": 1}])
        self.assertEqual(levels, {"apple": 5})


if __name__ == "__main__":
    unittest.main()
