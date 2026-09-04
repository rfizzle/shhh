"""A shelf of parts, counted as deliveries arrive."""


def restock(deliveries, shelf={}):
    """Count deliveries onto shelf and return it.

    A delivery is a (part, count) pair. Passing a shelf adds to it; leaving it
    out starts a new one.
    """
    for part, count in deliveries:
        shelf[part] = shelf.get(part, 0) + count
    return shelf


def short(shelf, wanted):
    """The parts wanted holds more of than shelf does, and by how many."""
    return {
        part: count - shelf.get(part, 0)
        for part, count in wanted.items()
        if count > shelf.get(part, 0)
    }
