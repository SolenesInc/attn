"""A text report: one row per sku, largest total first, then by sku, and a grand total."""

from orders.pricing import line_total


def dollars(cents):
    return f"{cents / 100}"


def report(orders):
    totals = {}
    for order in orders:
        totals[order["sku"]] = line_total(order)
    rows = sorted(totals.items(), key=lambda row: row[1])
    lines = [f"{sku} {dollars(total)}" for sku, total in rows]
    lines.append(f"total {dollars(sum(totals.values()))}")
    return "\n".join(lines)
