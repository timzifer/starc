# A small library for examples/report.star.

def mean(xs):
    """Returns the arithmetic mean of xs."""
    return sum_of(xs) / len(xs)

def sum_of(xs):
    total = 0
    for x in xs:
        total += x
    return total
