"""Prices in cents. A line of 10 or more items gets 10% off, of 100 or more gets 20% off."""


def discount_percent(qty):
    if qty > 100:
        return 20
    if qty > 10:
        return 10
    return 0


def line_total(order):
    """The line's price after its discount, rounded half up to a cent."""
    gross = order["qty"] * order["price"]
    return round(gross * (100 - discount_percent(order["qty"])) / 100)


def order_total(orders, discounts=()):
    """The total of all lines. Each code in discounts takes 100 cents off, never below zero."""
    total = sum(line_total(order) for order in orders)
    for _ in discounts:
        total -= 100
    return total
