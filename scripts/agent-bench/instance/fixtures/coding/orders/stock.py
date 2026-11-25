"""Stock on hand. A reservation takes every line or none."""


class OutOfStock(Exception):
    pass


class Stock:
    def __init__(self, levels):
        self.levels = levels

    def reserve(self, orders):
        for order in orders:
            on_hand = self.levels[order["sku"]]
            if on_hand < order["qty"]:
                raise OutOfStock(order["sku"])
            self.levels[order["sku"]] = on_hand - order["qty"]
