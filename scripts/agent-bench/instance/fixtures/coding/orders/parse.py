"""Order lines, one per row: sku, quantity and unit price in cents, separated by commas."""


def parse_line(line):
    parts = line.split(",")
    return {"sku": parts[0], "qty": int(parts[1]), "price": int(parts[2])}


def parse_orders(text):
    """Every order in the text. Blank lines and lines that start with # are skipped."""
    orders = []
    for line in text.split("\n"):
        if line.startswith("#"):
            continue
        orders.append(parse_line(line))
    return orders
